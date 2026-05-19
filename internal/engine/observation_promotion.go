package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type PromoteRequest struct {
	Workspace  string
	SessionID  string
	From       *time.Time
	To         *time.Time
	MaxItems   int
	MemoryType core.MemoryType
	Outcome    *core.Outcome
}

type PromoteResult struct {
	Workspace      string          `json:"workspace"`
	SessionID      string          `json:"session_id"`
	RequestedType  core.MemoryType `json:"requested_type"`
	Observations   int             `json:"observations"`
	CreatedID      string          `json:"created_id"`
	Deduplicated   bool            `json:"deduplicated"`
	Rejected       bool            `json:"rejected"`
	RejectReason   string          `json:"reject_reason,omitempty"`
	StorageTier    core.StorageTier `json:"storage_tier"`
	RouteRule      string          `json:"route_rule,omitempty"`
	RouteReason    string          `json:"route_reason,omitempty"`
	ContentHash    string          `json:"content_hash,omitempty"`
	Confidence     float64         `json:"confidence"`
	PromotionChars int             `json:"promotion_chars"`
}

type ObservationPromoter struct {
	store  *sqlite.Store
	writer *WritePipeline
}

func NewObservationPromoter(store *sqlite.Store, writer *WritePipeline) *ObservationPromoter {
	return &ObservationPromoter{store: store, writer: writer}
}

func (p *ObservationPromoter) Promote(ctx context.Context, req PromoteRequest) (*PromoteResult, error) {
	if strings.TrimSpace(req.Workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	if req.MaxItems <= 0 {
		req.MaxItems = 200
	}
	if req.MaxItems > 200 {
		req.MaxItems = 200
	}
	if req.MemoryType == "" {
		req.MemoryType = core.EpisodicMemory
	}
	if !core.IsMemoryType(req.MemoryType) {
		return nil, errors.New("invalid memory type")
	}

	obs, err := p.store.ListObservations(ctx, req.Workspace, req.SessionID, req.From, req.To, req.MaxItems)
	if err != nil {
		return nil, err
	}
	if len(obs) == 0 {
		return &PromoteResult{
			Workspace:     req.Workspace,
			SessionID:     req.SessionID,
			RequestedType: req.MemoryType,
			Observations:  0,
		}, nil
	}

	sort.SliceStable(obs, func(i, j int) bool {
		return obs[i].OccurredAt.Before(obs[j].OccurredAt)
	})

	content := BuildPromotionText(req.SessionID, obs, 1800)
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("promotion content is empty")
	}

	out, err := p.writer.Write(ctx, WriteInput{
		Workspace: req.Workspace,
		Type:      req.MemoryType,
		Content:   content,
		Tags:      []string{"promoted"},
		Source:    core.MemorySource{Type: core.SourceConsolidation, SessionID: req.SessionID},
		Outcome:   req.Outcome,
		Mode:      ExtractFast,
	})
	if err != nil {
		return nil, err
	}

	return &PromoteResult{
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		RequestedType:  req.MemoryType,
		Observations:   len(obs),
		CreatedID:      out.ID,
		Deduplicated:   out.Deduplicated,
		Rejected:       out.Rejected,
		RejectReason:   out.RejectReason,
		StorageTier:    out.StorageTier,
		RouteRule:      out.RouteRule,
		RouteReason:    out.RouteReason,
		ContentHash:    out.ContentHash,
		Confidence:     out.Confidence,
		PromotionChars: len(content),
	}, nil
}

func BuildPromotionText(sessionID string, observations []core.Observation, maxChars int) string {
	var b strings.Builder

	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		sid = "unknown"
	}
	b.WriteString("Session observations: ")
	b.WriteString(sid)
	b.WriteString("\n")

	uniq := make(map[string]struct{}, len(observations))
	for _, o := range observations {
		line := fmt.Sprintf("- %s %s", o.OccurredAt.UTC().Format(time.RFC3339), strings.TrimSpace(o.Summary))
		line = RedactPrivateAndSecrets(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := uniq[line]; ok {
			continue
		}
		uniq[line] = struct{}{}
		if b.Len()+len(line)+1 > maxChars {
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	return ClipString(b.String(), maxChars)
}

