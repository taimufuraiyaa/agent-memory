package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/observability"
	"github.com/time/timebooks/agent-memory/internal/storage/markdown"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
	"github.com/time/timebooks/agent-memory/internal/validation"
)

// ExtractMode selects extraction strategy.
type ExtractMode string

const (
	ExtractFast        ExtractMode = "fast"
	ExtractLLMAssisted ExtractMode = "llm-assisted"
)

// entityInferenceCandidateLimit bounds how many of the most recently created
// non-cold memories are considered for entity co-occurrence relationship
// inference on each write (see inferRelationships and
// Store.ListMemoryLightweightForInferenceRecent). Without a bound, this scan
// is O(N) in the size of the workspace and runs synchronously on every
// Write().
const entityInferenceCandidateLimit = 500

// WriteInput represents write pipeline input.
type WriteInput struct {
	Workspace string
	Type      core.MemoryType
	Content   string
	Diagram   *core.Diagram
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
	embedder   embeddings.Provider
	cache      *QueryCache
}

// WritePipelineOptions customizes pipeline behavior for production entry points.
type WritePipelineOptions struct {
	MarkdownFilePath string
	Embedder         embeddings.Provider
	Cache            *QueryCache
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
	clean := normalizePreservingFences(content)
	if strings.TrimSpace(clean) == "" {
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
	return NewWritePipelineWithOptions(store, WritePipelineOptions{})
}

// NewWritePipelineWithEmbedder creates a pipeline that eagerly persists vectors.
func NewWritePipelineWithEmbedder(store *sqlite.Store, embedder embeddings.Provider) *WritePipeline {
	return NewWritePipelineWithOptions(store, WritePipelineOptions{Embedder: embedder})
}

// NewWritePipelineWithOptions creates a pipeline with optional markdown and embedder hooks.
func NewWritePipelineWithOptions(store *sqlite.Store, opt WritePipelineOptions) *WritePipeline {
	fast := FastExtractor{}
	p := &WritePipeline{
		store:  store,
		filter: NewRegexSecurityFilter(),
		router: NewHybridRouter(),
		extractors: map[ExtractMode]Extractor{
			ExtractFast:        fast,
			ExtractLLMAssisted: LLMAssistedExtractor{fallback: fast},
		},
	}
	p.embedder = opt.Embedder
	p.cache = opt.Cache
	if strings.TrimSpace(opt.MarkdownFilePath) != "" {
		p.markdown = markdown.NewAdapter(opt.MarkdownFilePath, 4000)
	}
	return p
}

// NewWritePipelineWithMarkdown creates a write pipeline with markdown tier adapter.
func NewWritePipelineWithMarkdown(store *sqlite.Store, markdownFilePath string) *WritePipeline {
	return NewWritePipelineWithOptions(store, WritePipelineOptions{MarkdownFilePath: markdownFilePath})
}

// Write executes stages: security -> extract -> dedup -> route/store.
func (p *WritePipeline) Write(ctx context.Context, in WriteInput) (res *WriteResult, writeErr error) {
	// Start trace span
	ctx, span := observability.StartSpan(ctx, "agent-memory.write")
	defer span.End()

	// Initial span attributes
	observability.SetSpanAttributes(ctx,
		observability.WorkspaceAttr(in.Workspace),
		observability.MemoryTypeAttr(string(in.Type)),
		observability.OperationAttr("write"),
	)

	timer := observability.NewTimer()
	defer func() {
		metrics := observability.GetRegistry()
		status := "success"
		if writeErr != nil {
			status = "error"
			errType := "runtime_error"
			errStr := writeErr.Error()
			if strings.Contains(errStr, "invalid workspace") ||
				strings.Contains(errStr, "invalid content") ||
				strings.Contains(errStr, "invalid diagram") ||
				strings.Contains(errStr, "required") ||
				strings.Contains(errStr, "unsupported extract mode") {
				errType = "validation_error"
			} else if strings.Contains(errStr, "persist eager vector") {
				errType = "embedding_error"
			}
			metrics.WriteErrors.WithLabelValues(in.Workspace, string(in.Type), errType).Inc()
			observability.RecordSpanError(ctx, writeErr)
		} else if res != nil {
			if res.Rejected {
				status = "rejected"
				metrics.WriteErrors.WithLabelValues(in.Workspace, string(in.Type), "rejected").Inc()
				observability.SetSpanAttributes(ctx, attribute.Bool("agent_memory.rejected", true))
			} else {
				if res.Deduplicated {
					observability.SetSpanAttributes(ctx, attribute.Bool("agent_memory.deduplicated", true))
				}
				observability.SetSpanAttributes(ctx,
					observability.MemoryIDAttr(res.ID),
					observability.StorageTierAttr(string(res.StorageTier)),
				)
				metrics.WriteBytes.WithLabelValues(in.Workspace, string(in.Type)).Observe(float64(len(in.Content)))
			}
		}
		metrics.WriteTotal.WithLabelValues(in.Workspace, string(in.Type), status).Inc()
		timer.ObserveDuration(metrics.WriteDuration.WithLabelValues(in.Workspace, string(in.Type)))
	}()

	// Validate workspace name
	if err := validation.ValidateWorkspaceName(in.Workspace); err != nil {
		return nil, fmt.Errorf("invalid workspace: %w", err)
	}

	// Validate content length
	if err := validation.ValidateContentLength(in.Content); err != nil {
		return nil, fmt.Errorf("invalid content: %w", err)
	}

	// Validate diagram code if present
	if in.Diagram != nil && in.Diagram.Code != "" {
		if err := validation.ValidateDiagramCode(in.Diagram.Code); err != nil {
			return nil, fmt.Errorf("invalid diagram: %w", err)
		}
	}

	if strings.TrimSpace(in.Workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("content is required")
	}
	if p.filter == nil || p.store == nil {
		return nil, errors.New("pipeline is not initialized")
	}

	if in.Diagram == nil {
		withoutDiagram, d := extractDiagramFence(in.Content)
		if d != nil {
			in.Diagram = d
			in.Content = withoutDiagram
		}
	}
	if in.Diagram != nil {
		in.Tags = appendUnique(in.Tags, "diagram")
		if strings.TrimSpace(in.Diagram.Lang) != "" {
			in.Tags = appendUnique(in.Tags, strings.ToLower(strings.TrimSpace(in.Diagram.Lang)))
		}
	}

	validationContent := in.Content
	if in.Diagram != nil && strings.TrimSpace(in.Diagram.Code) != "" {
		validationContent = strings.TrimSpace(validationContent) + "\n" + in.Diagram.Code
	}
	if err := p.filter.Validate(ctx, SecurityValidationInput{
		Workspace: in.Workspace,
		Content:   validationContent,
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
		if in.Diagram != nil && strings.TrimSpace(in.Diagram.Code) != "" {
			lang := strings.TrimSpace(in.Diagram.Lang)
			if lang == "" {
				lang = "diagram"
			}
			content = "Diagram (" + lang + ")"
		} else {
			return nil, err
		}
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

	hash := contentHash(in.Workspace, in.Type, content, in.Diagram)
	decision := p.router.Decide(in)
	tier := decision.Tier
	entry := &core.MemoryEntry{
		ID:          uuid.NewString(),
		Type:        in.Type,
		Content:     content,
		Diagram:     in.Diagram,
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

	// Compute the embedding (if any) before writing the memory row. Doing
	// this first means an embedding failure never leaves an orphaned memory
	// row behind, and a successful embedding can be persisted in the same
	// transaction as the memory insert (see InsertMemoryByHashWithVector).
	var (
		vec           []float32
		haveVec       bool
		embedProvider string
		embedModelVer string
	)
	if p.embedder != nil {
		text := memoryVectorText(*entry)
		if strings.TrimSpace(text) != "" {
			embedTimer := observability.NewTimer()
			provider := p.embedder.Name()
			v, err := p.embedder.Embed(ctx, text)
			metrics := observability.GetRegistry()
			if err != nil {
				metrics.EmbeddingTotal.WithLabelValues(provider, "error").Inc()
				metrics.EmbeddingErrors.WithLabelValues(provider, "embed_failed").Inc()
				metrics.WriteEmbeddingErrors.WithLabelValues(entry.Workspace, provider, "embed_failed").Inc()
				return nil, fmt.Errorf("persist eager vector: embed memory %s: %w", entry.ID, err)
			}
			metrics.EmbeddingTotal.WithLabelValues(provider, "success").Inc()
			embedTimer.ObserveDuration(metrics.EmbeddingDuration.WithLabelValues(provider))
			embedTimer.ObserveDuration(metrics.WriteEmbeddingDuration.WithLabelValues(entry.Workspace, provider))
			metrics.EmbeddingBatchSize.WithLabelValues(provider).Observe(1.0)
			metrics.WriteEmbeddingSuccess.WithLabelValues(entry.Workspace, provider).Inc()
			vec = v
			haveVec = true
			embedProvider = provider
			embedModelVer = p.embedder.ModelVersion()
		}
	}

	// dedupResult builds the WriteResult for a content-hash collision, shared
	// by both the with-vector and without-vector insert paths below.
	dedupResult := func() (*WriteResult, error) {
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

	if haveVec {
		// Insert the memory and its embedding atomically: either both are
		// persisted, or neither is, so there's never a window where a memory
		// row exists without its vector.
		if err := p.store.InsertMemoryByHashWithVector(ctx, entry, hash, embedProvider, embedModelVer, vec); err != nil {
			if errors.Is(err, sqlite.ErrDuplicateContent) {
				return dedupResult()
			}
			metrics := observability.GetRegistry()
			metrics.WriteEmbeddingErrors.WithLabelValues(entry.Workspace, embedProvider, "db_upsert_failed").Inc()
			return nil, fmt.Errorf("persist eager vector: insert memory %s: %w", entry.ID, err)
		}
	} else {
		if err := p.store.InsertMemoryByHash(ctx, entry, hash); err != nil {
			if errors.Is(err, sqlite.ErrDuplicateContent) {
				return dedupResult()
			}
			return nil, err
		}
	}

	if p.markdown != nil && tier == core.TierMarkdown {
		if err := p.markdown.Upsert(entry.ID, entry.Content); err != nil {
			_ = p.store.DeleteByIDs(ctx, []string{entry.ID})
			return nil, err
		}
	}

	// Infer and persist relationships for the new memory entry
	p.inferRelationships(ctx, entry)

	// Invalidate query cache after successful write to ensure fresh results
	if p.cache != nil {
		p.cache.InvalidateWorkspace(entry.Workspace)
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

// inferRelationships implements FR-SDO-12 automatic relationship inference on write
func (p *WritePipeline) inferRelationships(ctx context.Context, entry *core.MemoryEntry) {
	// 1. Temporal relationships (same session)
	if entry.Source.SessionID != "" {
		pastMemories, err := p.store.GetSessionMemories(ctx, entry.Workspace, entry.Source.SessionID)
		if err == nil {
			for _, m := range pastMemories {
				if m.ID == entry.ID {
					continue
				}
				timeDiff := entry.CreatedAt.Sub(m.CreatedAt)
				if timeDiff < 0 {
					timeDiff = -timeDiff
				}
				if timeDiff <= time.Hour {
					// Weight by time proximity: 1.0 - (time_diff_seconds / 3600.0)
					weight := 1.0 - (timeDiff.Seconds() / 3600.0)
					if weight < 0.1 {
						weight = 0.1
					}
					// If m is older than entry, m -> entry (chronological order)
					if m.CreatedAt.Before(entry.CreatedAt) {
						_ = p.store.AddRelation(ctx, m.ID, entry.ID, core.RelCalls, weight, map[string]string{
							"session_id": entry.Source.SessionID,
							"subtype":    "temporal",
						})
					} else {
						_ = p.store.AddRelation(ctx, entry.ID, m.ID, core.RelCalls, weight, map[string]string{
							"session_id": entry.Source.SessionID,
							"subtype":    "temporal",
						})
					}
				}
			}
		}
	}

	// 2. Entity co-occurrence (Jaccard similarity > 0)
	if len(entry.Entities) > 0 {
		workspaceMemories, err := p.store.ListMemoryLightweightForInferenceRecent(ctx, entry.Workspace, entityInferenceCandidateLimit)
		if err == nil {
			for _, m := range workspaceMemories {
				if m.ID == entry.ID || m.StorageTier == core.TierCold {
					continue
				}
				if len(m.Entities) == 0 {
					continue
				}
				// Find intersection of entities
				intersectCount := 0
				shared := []string{}
				for _, e1 := range entry.Entities {
					for _, e2 := range m.Entities {
						if strings.EqualFold(e1, e2) {
							intersectCount++
							shared = append(shared, e1)
							break
						}
					}
				}
				if intersectCount > 0 {
					unionCount := len(entry.Entities) + len(m.Entities) - intersectCount
					weight := float64(intersectCount) / float64(unionCount)
					// Add bidirectional depends_on relations
					meta := map[string]string{
						"shared_entities": strings.Join(shared, ","),
						"subtype":         "co_occurrence",
					}
					_ = p.store.AddRelation(ctx, entry.ID, m.ID, core.RelDependsOn, weight, meta)
					_ = p.store.AddRelation(ctx, m.ID, entry.ID, core.RelDependsOn, weight, meta)
				}
			}
		}
	}

	// 3. Outcome chains (failed attempt -> successful approach)
	if entry.Outcome != nil && entry.Outcome.Result == core.OutcomeSuccess {
		if entry.Source.SessionID != "" {
			pastMemories, err := p.store.GetSessionMemories(ctx, entry.Workspace, entry.Source.SessionID)
			if err == nil {
				for _, m := range pastMemories {
					if m.ID == entry.ID {
						continue
					}
					// Check if m is a failed outcome
					isFailed := false
					if m.Type == core.OutcomeMemory {
						isFailed = true
					} else if m.Outcome != nil && m.Outcome.Result == core.OutcomeFailure {
						isFailed = true
					}
					if isFailed {
						// Add m -> entry relation (RelLedTo)
						_ = p.store.AddRelation(ctx, m.ID, entry.ID, core.RelLedTo, 1.0, map[string]string{
							"subtype": "outcome_chain",
						})
					}
				}
			}
		}
	}
}

func appendUnique(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

func contentHash(workspace string, mt core.MemoryType, content string, diagram *core.Diagram) string {
	lang := ""
	code := ""
	if diagram != nil {
		lang = strings.TrimSpace(diagram.Lang)
		code = diagram.Code
	}
	sum := sha256.Sum256([]byte(workspace + "|" + string(mt) + "|" + strings.TrimSpace(content) + "|" + lang + "|" + code))
	return hex.EncodeToString(sum[:])
}

func normalizePreservingFences(content string) string {
	s := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	outside := make([]string, 0, 64)
	inFence := false
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "```") {
			if len(outside) > 0 {
				out = append(out, strings.Join(outside, " "))
				outside = outside[:0]
			}
			out = append(out, trim)
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, ln)
			continue
		}
		outside = append(outside, strings.Fields(ln)...)
	}
	if len(outside) > 0 {
		out = append(out, strings.Join(outside, " "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func extractDiagramFence(content string) (string, *core.Diagram) {
	s := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trim, "```") {
			lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trim, "```")))
			switch lang {
			case "mermaid", "plantuml", "dot", "graphviz":
				codeLines := make([]string, 0, 32)
				for i+1 < len(lines) {
					if strings.TrimSpace(lines[i+1]) == "```" {
						i++
						break
					}
					codeLines = append(codeLines, lines[i+1])
					i++
				}
				cleaned := strings.TrimSpace(strings.Join(out, "\n") + "\n" + strings.Join(lines[i+1:], "\n"))
				cleaned = strings.TrimSpace(cleaned)
				code := strings.TrimRight(strings.Join(codeLines, "\n"), "\n")
				return cleaned, &core.Diagram{Lang: lang, Code: code}
			}
		}
		out = append(out, lines[i])
	}
	return content, nil
}

func memoryVectorText(memory core.MemoryEntry) string {
	text := strings.TrimSpace(memory.Content)
	if memory.Diagram == nil || strings.TrimSpace(memory.Diagram.Code) == "" {
		return text
	}
	if text == "" {
		return strings.TrimSpace(memory.Diagram.Code)
	}
	return text + "\n" + strings.TrimSpace(memory.Diagram.Code)
}
