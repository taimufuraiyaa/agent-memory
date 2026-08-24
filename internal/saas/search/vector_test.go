package search

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
)

type vectorRepositoryFixture struct {
	candidate *VectorCandidate
	completed []VectorRecord
	failed    bool
	reset     bool
}

func (*vectorRepositoryFixture) ActiveTenantIDs(context.Context) ([]string, error) {
	return []string{"tenant-a"}, nil
}
func (r *vectorRepositoryFixture) ClaimNextVector(context.Context, string, string, time.Time, time.Duration) (*VectorCandidate, error) {
	value := r.candidate
	r.candidate = nil
	return value, nil
}
func (r *vectorRepositoryFixture) CompleteVectorProjection(_ context.Context, _ VectorCandidate, _, _ string, _ int, records []VectorRecord, _ time.Time) error {
	r.completed = append([]VectorRecord(nil), records...)
	return nil
}
func (r *vectorRepositoryFixture) FailVectorProjection(context.Context, VectorCandidate, string, time.Time) error {
	r.failed = true
	return nil
}
func (*vectorRepositoryFixture) PurgeUnauthorizedVectors(context.Context, string) (int64, error) {
	return 0, nil
}
func (r *vectorRepositoryFixture) ResetVectorProjection(context.Context, string, string) error {
	r.reset = true
	return nil
}

type vectorEmbedderFixture struct {
	calls int
	fail  bool
}

func (e *vectorEmbedderFixture) Embed(_ context.Context, request modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error) {
	e.calls++
	if e.fail {
		return modelgateway.EmbedResponse{}, errors.New("provider unavailable")
	}
	vectors := make([][]float32, len(request.Texts))
	for index := range vectors {
		vectors[index] = make([]float32, VectorDimensions)
		vectors[index][0] = 1
	}
	return modelgateway.EmbedResponse{Provider: request.Provider, Model: request.Model, Dimensions: VectorDimensions, Vectors: vectors}, nil
}

func TestVectorProjectorBatchesThroughGatewayAndPreservesPassageIdentity(t *testing.T) {
	passages := make([]VectorPassage, 33)
	for index := range passages {
		passages[index] = VectorPassage{ID: "passage-" + string(rune('a'+index)), StructuralNodeID: "node", Text: "text"}
	}
	repository := &vectorRepositoryFixture{candidate: &VectorCandidate{TenantID: "tenant-a", SourceID: "source-a", SourceVersion: 7, ClaimToken: "claim", Passages: passages}}
	embedder := &vectorEmbedderFixture{}
	projector, err := NewVectorProjector(repository, embedder, "private-model", "embed-v1", func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	processed, err := projector.ProcessOnce(context.Background())
	if err != nil || processed != 1 || embedder.calls != 2 || len(repository.completed) != len(passages) {
		t.Fatalf("processed=%d calls=%d records=%d err=%v", processed, embedder.calls, len(repository.completed), err)
	}
	for index, record := range repository.completed {
		if record.PassageID != passages[index].ID || record.StructuralNodeID != passages[index].StructuralNodeID {
			t.Fatalf("record identity changed at %d: %+v", index, record)
		}
	}
	if err := projector.Rebuild(context.Background(), "tenant-a", "source-a"); err != nil || !repository.reset {
		t.Fatalf("rebuild reset=%v err=%v", repository.reset, err)
	}
}

func TestVectorProjectorRetainsFailedClaimForBoundedRetry(t *testing.T) {
	repository := &vectorRepositoryFixture{candidate: &VectorCandidate{TenantID: "tenant-a", SourceID: "source-a", SourceVersion: 1, ClaimToken: "claim", Passages: []VectorPassage{{ID: "passage", StructuralNodeID: "node", Text: "text"}}}}
	projector, err := NewVectorProjector(repository, &vectorEmbedderFixture{fail: true}, "private-model", "embed-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := projector.ProcessOnce(context.Background()); err == nil || processed != 0 || !repository.failed {
		t.Fatalf("processed=%d failed=%v err=%v", processed, repository.failed, err)
	}
}

func TestVectorLiteralRejectsInvalidShapeAndNonFiniteValues(t *testing.T) {
	if _, err := vectorLiteral([]float32{1}); err == nil {
		t.Fatal("invalid dimensions were accepted")
	}
	values := make([]float32, VectorDimensions)
	values[1] = float32NaN()
	if _, err := vectorLiteral(values); err == nil {
		t.Fatal("NaN embedding was accepted")
	}
}

func float32NaN() float32 { return math.Float32frombits(0x7fc00000) }
