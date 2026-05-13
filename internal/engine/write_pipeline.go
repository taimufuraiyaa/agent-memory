package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/markdown"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

// ExtractMode selects extraction strategy.
type ExtractMode string

const (
	ExtractFast        ExtractMode = "fast"
	ExtractLLMAssisted ExtractMode = "llm-assisted"
)

// WriteInput represents write pipeline input.
type WriteInput struct {
	Workspace string
	Type      core.MemoryType
	Content   string
	Source    core.MemorySource
	Entities  []string
	Tags      []string
	Outcome   *core.Outcome
	Mode      ExtractMode
}

// WriteResult reports final write status.
type WriteResult struct {
	ID           string
	Deduplicated bool
	Rejected     bool
	RejectReason string
	StorageTier  core.StorageTier
	RouteRule    string
	RouteReason  string
	ContentHash  string
	Confidence   float64
}

// WritePipeline executes ordered write stages.
type WritePipeline struct {
	store      *sqlite.Store
	extractors map[ExtractMode]Extractor
	filter     SecurityFilter
	router     HybridRouter
	markdown   *markdown.Adapter
}

// Extractor performs extraction/transformation of input content.
type Extractor interface {
	Extract(ctx context.Context, content string) (string, error)
}

// SecurityFilter rejects unsafe content.
type SecurityFilter interface {
	Validate(ctx context.Context, in SecurityValidationInput) error
}

// FastExtractor performs deterministic cleanup.
type FastExtractor struct{}

func (e FastExtractor) Extract(_ context.Context, content string) (string, error) {
	clean := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if clean == "" {
		return "", errors.New("content is empty")
	}
	return clean, nil
}

// LLMAssistedExtractor placeholder uses fast mode for now.
type LLMAssistedExtractor struct {
	fallback Extractor
}

func (e LLMAssistedExtractor) Extract(ctx context.Context, content string) (string, error) {
	return e.fallback.Extract(ctx, content)
}

// NewWritePipeline constructs the default write pipeline.
func NewWritePipeline(store *sqlite.Store) *WritePipeline {
	fast := FastExtractor{}
	return &WritePipeline{
		store:  store,
		filter: NewRegexSecurityFilter(),
		router: NewHybridRouter(),
		extractors: map[ExtractMode]Extractor{
			ExtractFast:        fast,
			ExtractLLMAssisted: LLMAssistedExtractor{fallback: fast},
		},
	}
}

// NewWritePipelineWithMarkdown creates a write pipeline with markdown tier adapter.
func NewWritePipelineWithMarkdown(store *sqlite.Store, markdownFilePath string) *WritePipeline {
	p := NewWritePipeline(store)
	if strings.TrimSpace(markdownFilePath) != "" {
		p.markdown = markdown.NewAdapter(markdownFilePath, 4000)
	}
	return p
}

// Write executes stages: security -> extract -> dedup -> route/store.
func (p *WritePipeline) Write(ctx context.Context, in WriteInput) (*WriteResult, error) {
	if strings.TrimSpace(in.Workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("content is required")
	}
	if p.filter == nil || p.store == nil {
		return nil, errors.New("pipeline is not initialized")
	}

	if err := p.filter.Validate(ctx, SecurityValidationInput{
		Workspace: in.Workspace,
		Content:   in.Content,
		Tags:      in.Tags,
	}); err != nil {
		return &WriteResult{Rejected: true, RejectReason: err.Error()}, nil
	}

	mode := in.Mode
	if mode == "" {
		mode = ExtractFast
	}
	extractor, ok := p.extractors[mode]
	if !ok {
		return nil, fmt.Errorf("unsupported extract mode: %s", mode)
	}
	content, err := extractor.Extract(ctx, in.Content)
	if err != nil {
		return nil, err
	}

	in.Content = content

	// Confidence gate — failures always bypass, everything else is scored.
	var confidence float64
	if isFailureOutcome(in) {
		confidence = 1.0 // failures are always stored at full confidence
	} else {
		confidence = EstimateConfidence(ctx, in, p.store)
		band := ClassifyConfidence(confidence)
		if band == ConfidenceLow {
			return &WriteResult{
				Rejected:     true,
				RejectReason: fmt.Sprintf("confidence too low (%.2f < %.2f)", confidence, thresholdMedium),
			}, nil
		}
		if band == ConfidenceMedium {
			in.Tags = appendUnique(in.Tags, tagLowConfidence)
		}
	}

	hash := contentHash(in.Workspace, in.Type, content)
	decision := p.router.Decide(in)
	tier := decision.Tier
	entry := &core.MemoryEntry{
		ID:          uuid.NewString(),
		Type:        in.Type,
		Content:     content,
		Workspace:   in.Workspace,
		Source:      in.Source,
		Entities:    in.Entities,
		Tags:        in.Tags,
		Outcome:     in.Outcome,
		Pinned:      containsPinned(in.Tags),
		Confidence:  confidence,
		StorageTier: tier,
		Importance:  decision.Importance,
	}

	if err := p.store.InsertMemoryByHash(ctx, entry, hash); err != nil {
		if errors.Is(err, sqlite.ErrDuplicateContent) {
			existing, getErr := p.store.GetMemoryByHash(ctx, in.Workspace, hash)
			if getErr != nil {
				return nil, getErr
			}
			return &WriteResult{
				ID:           existing.ID,
				Deduplicated: true,
				StorageTier:  existing.StorageTier,
				RouteRule:    decision.Rule,
				RouteReason:  decision.Reason,
				ContentHash:  hash,
			}, nil
		}
		return nil, err
	}
	if p.markdown != nil && tier == core.TierMarkdown {
		if err := p.markdown.Upsert(entry.ID, entry.Content); err != nil {
			return nil, err
		}
	}

	return &WriteResult{
		ID:          entry.ID,
		StorageTier: tier,
		RouteRule:   decision.Rule,
		RouteReason: decision.Reason,
		ContentHash: hash,
		Confidence:  confidence,
	}, nil
}

func appendUnique(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func contentHash(workspace string, mt core.MemoryType, content string) string {
	sum := sha256.Sum256([]byte(workspace + "|" + string(mt) + "|" + strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}
