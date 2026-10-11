package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
)

type GraphImportRequest struct {
	Batch                      contracts.GraphRevisionImportBatch
	EvidenceResolved           bool
	AdmissionPassed            bool
	ReviewCarryForwardComplete bool
	EvaluationPassed           bool
}

type GraphImportService struct {
	store contracts.GraphRevisionBatchStore
}

func NewGraphImportService(store contracts.GraphRevisionBatchStore) *GraphImportService {
	return &GraphImportService{store: store}
}

func (s *GraphImportService) Import(ctx context.Context, request GraphImportRequest) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("graph revision batch store is required")
	}
	if err := validateGraphImportRequest(request); err != nil {
		return err
	}
	return s.store.ImportGraphRevisionBatch(ctx, request.Batch)
}

func validateGraphImportRequest(request GraphImportRequest) error {
	batch := request.Batch
	if err := batch.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(batch.ConfigurationID) == "" || strings.TrimSpace(batch.RevisionID) == "" {
		return fmt.Errorf("graph import configuration and revision are required")
	}
	if batch.ExpectedEntities != len(batch.Entities) || batch.ExpectedEdges != len(batch.Edges) || batch.ExpectedCommunities != len(batch.Communities) {
		return fmt.Errorf("graph import normalized counts do not match validated artifact")
	}
	if !request.EvidenceResolved || !request.AdmissionPassed || !request.ReviewCarryForwardComplete || !request.EvaluationPassed {
		return fmt.Errorf("graph import preconditions are incomplete")
	}
	for _, record := range batch.Entities {
		if record.Entity.Scope != batch.Scope || record.Version.RevisionID != batch.RevisionID || record.Entity.LastRevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("graph entity import record scope, revision, or evidence mismatch")
		}
	}
	for _, record := range batch.Edges {
		if record.Edge.Scope != batch.Scope || record.Version.RevisionID != batch.RevisionID || record.Edge.LastRevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("graph edge import record scope, revision, or evidence mismatch")
		}
	}
	for _, record := range batch.Communities {
		if record.Community.Scope != batch.Scope || record.Community.RevisionID != batch.RevisionID || record.Report.RevisionID != batch.RevisionID || record.Report.Scope != batch.Scope {
			return fmt.Errorf("graph community import record scope or revision mismatch")
		}
	}
	return nil
}
