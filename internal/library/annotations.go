package library

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type Annotation struct {
	ID             string          `json:"id"`
	EditionID      string          `json:"edition_id"`
	CitationID     string          `json:"citation_id,omitempty"`
	Content        string          `json:"content"`
	Owner          core.Principal  `json:"owner"`
	Visibility     core.Visibility `json:"visibility"`
	OrganizationID string          `json:"organization_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (a Annotation) Validate() error {
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.EditionID) == "" || strings.TrimSpace(a.Content) == "" || a.CreatedAt.IsZero() {
		return errors.New("annotation identity, edition, content, and creation time are required")
	}
	if err := a.Owner.Validate(); err != nil {
		return err
	}
	return (core.ResourceOwnership{Owner: a.Owner, Visibility: a.Visibility, OrganizationID: a.OrganizationID}).Validate()
}

type AnnotationRepository interface {
	PutAnnotation(context.Context, Annotation) error
	GetAnnotation(context.Context, string) (Annotation, error)
	ListAnnotations(context.Context, string) ([]Annotation, error)
	UpdateAnnotationVisibility(context.Context, string, core.Visibility, string) error
}

type AnnotationService struct {
	repository AnnotationRepository
}

func NewAnnotationService(repository AnnotationRepository) *AnnotationService {
	return &AnnotationService{repository: repository}
}

func (s *AnnotationService) Create(ctx context.Context, scope core.AuthorizationScope, annotation Annotation) error {
	if s == nil || s.repository == nil || scope.Validate() != nil || !containsCapability(scope.Capabilities, core.CapabilityAnnotate) {
		return ErrLibraryResourceNotFound
	}
	if annotation.Owner != scope.Principal || annotation.Visibility != core.VisibilityPrivate || annotation.OrganizationID != "" {
		return errors.New("new annotations must be private and owned by the active principal")
	}
	if err := annotation.Validate(); err != nil {
		return err
	}
	return s.repository.PutAnnotation(ctx, annotation)
}

func (s *AnnotationService) Get(ctx context.Context, scope core.AuthorizationScope, id string) (Annotation, error) {
	if s == nil || s.repository == nil {
		return Annotation{}, ErrLibraryResourceNotFound
	}
	annotation, err := s.repository.GetAnnotation(ctx, id)
	if err != nil || !annotationVisible(scope, annotation) {
		return Annotation{}, ErrLibraryResourceNotFound
	}
	return annotation, nil
}

func (s *AnnotationService) ListEdition(ctx context.Context, scope core.AuthorizationScope, editionID string) ([]Annotation, error) {
	if s == nil || s.repository == nil || scope.Validate() != nil {
		return []Annotation{}, nil
	}
	annotations, err := s.repository.ListAnnotations(ctx, editionID)
	if err != nil {
		return nil, err
	}
	visible := make([]Annotation, 0, len(annotations))
	for _, annotation := range annotations {
		if annotationVisible(scope, annotation) {
			visible = append(visible, annotation)
		}
	}
	return visible, nil
}

func (s *AnnotationService) PromoteToOrganization(ctx context.Context, scope core.AuthorizationScope, id, organizationID string) error {
	annotation, err := s.Get(ctx, scope, id)
	if err != nil {
		return err
	}
	if annotation.Owner != scope.Principal || !containsCapability(scope.Capabilities, core.CapabilityProposeKnowledge) ||
		!containsValue(scope.OrganizationIDs, organizationID) {
		return ErrLibraryResourceNotFound
	}
	return s.repository.UpdateAnnotationVisibility(ctx, id, core.VisibilityOrganization, organizationID)
}

func annotationVisible(scope core.AuthorizationScope, annotation Annotation) bool {
	policy := core.AccessPolicy{
		Version:   "annotation-v1",
		Ownership: core.ResourceOwnership{Owner: annotation.Owner, Visibility: annotation.Visibility, OrganizationID: annotation.OrganizationID},
	}
	return core.Authorize(scope, policy, core.CapabilityAnnotate).Allowed
}

func containsCapability(values []core.Capability, target core.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
