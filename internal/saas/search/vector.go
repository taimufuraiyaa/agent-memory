package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
)

const VectorDimensions = 384

type VectorPassage struct {
	ID               string
	StructuralNodeID string
	Text             string
}

type VectorCandidate struct {
	TenantID      string
	SourceID      string
	SourceVersion int64
	ClaimToken    string
	Passages      []VectorPassage
}

type VectorRecord struct {
	PassageID        string
	StructuralNodeID string
	Embedding        []float32
}

type VectorHit struct {
	SourceID      string  `json:"source_id"`
	SourceVersion int64   `json:"source_version"`
	PassageID     string  `json:"passage_id"`
	Text          string  `json:"text"`
	Score         float64 `json:"score"`
}

type VectorRepository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	ClaimNextVector(context.Context, string, string, time.Time, time.Duration) (*VectorCandidate, error)
	CompleteVectorProjection(context.Context, VectorCandidate, string, string, int, []VectorRecord, time.Time) error
	FailVectorProjection(context.Context, VectorCandidate, string, time.Time) error
	PurgeUnauthorizedVectors(context.Context, string) (int64, error)
	ResetVectorProjection(context.Context, string, string) error
}

type VectorEmbedder interface {
	Embed(context.Context, modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error)
}

type VectorProjector struct {
	repository VectorRepository
	embedder   VectorEmbedder
	provider   string
	model      string
	batchSize  int
	lease      time.Duration
	now        func() time.Time
}

func NewVectorProjector(repository VectorRepository, embedder VectorEmbedder, provider, model string, now func() time.Time) (*VectorProjector, error) {
	if repository == nil || embedder == nil || provider == "" || model == "" {
		return nil, errors.New("vector projector repository, embedder, provider, and model are required")
	}
	if now == nil {
		now = time.Now
	}
	return &VectorProjector{repository: repository, embedder: embedder, provider: provider, model: model, batchSize: 32, lease: 2 * time.Minute, now: now}, nil
}

func (p *VectorProjector) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := p.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	projectionVersion := vectorProjectionVersion(p.provider, p.model, VectorDimensions)
	for _, tenantID := range tenants {
		if _, err := p.repository.PurgeUnauthorizedVectors(ctx, tenantID); err != nil {
			failures = append(failures, err)
			continue
		}
		candidate, err := p.repository.ClaimNextVector(ctx, tenantID, projectionVersion, p.now().UTC(), p.lease)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if candidate == nil {
			continue
		}
		records, response, err := p.embedCandidate(ctx, *candidate)
		if err != nil {
			_ = p.repository.FailVectorProjection(ctx, *candidate, "model_unavailable", p.now().UTC().Add(time.Minute))
			failures = append(failures, err)
			continue
		}
		if err := p.repository.CompleteVectorProjection(ctx, *candidate, response.Provider, response.Model, response.Dimensions, records, p.now().UTC()); err != nil {
			failures = append(failures, err)
			continue
		}
		processed++
	}
	return processed, errors.Join(failures...)
}

func (p *VectorProjector) embedCandidate(ctx context.Context, candidate VectorCandidate) ([]VectorRecord, modelgateway.EmbedResponse, error) {
	records := make([]VectorRecord, 0, len(candidate.Passages))
	var contract modelgateway.EmbedResponse
	for start := 0; start < len(candidate.Passages); start += p.batchSize {
		end := min(start+p.batchSize, len(candidate.Passages))
		texts := make([]string, end-start)
		for index := start; index < end; index++ {
			texts[index-start] = candidate.Passages[index].Text
		}
		response, err := p.embedder.Embed(ctx, modelgateway.EmbedRequest{TenantID: candidate.TenantID, SourceID: candidate.SourceID, SourceVersion: candidate.SourceVersion, Provider: p.provider, Model: p.model, Texts: texts})
		if err != nil {
			return nil, modelgateway.EmbedResponse{}, err
		}
		if response.Dimensions != VectorDimensions {
			return nil, modelgateway.EmbedResponse{}, fmt.Errorf("vector projection requires %d dimensions", VectorDimensions)
		}
		contract = response
		for index, embedding := range response.Vectors {
			passage := candidate.Passages[start+index]
			records = append(records, VectorRecord{PassageID: passage.ID, StructuralNodeID: passage.StructuralNodeID, Embedding: embedding})
		}
	}
	return records, contract, nil
}

func (p *VectorProjector) Rebuild(ctx context.Context, tenantID, sourceID string) error {
	if tenantID == "" || sourceID == "" {
		return errors.New("tenant and source are required")
	}
	return p.repository.ResetVectorProjection(ctx, tenantID, sourceID)
}

func (p *VectorProjector) Run(ctx context.Context, poll time.Duration, report func(error)) {
	if poll <= 0 {
		poll = time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := p.ProcessOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func vectorProjectionVersion(provider, model string, dimensions int) string {
	return fmt.Sprintf("%s/%s/%d", provider, model, dimensions)
}
