package library

import (
	"context"
	"errors"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrLibraryResourceNotFound = errors.New("library resource not found")

type ProtectedEditionStore interface {
	GetBookEdition(context.Context, string) (BookEdition, error)
	ListBookEditions(context.Context) ([]BookEdition, error)
	GetLibraryResourcePolicy(context.Context, ResourceType, string) (LibraryResourcePolicy, error)
}

type AuthorizedRepository struct {
	store ProtectedEditionStore
}

func NewAuthorizedRepository(store ProtectedEditionStore) *AuthorizedRepository {
	return &AuthorizedRepository{store: store}
}

func (r *AuthorizedRepository) GetEdition(ctx context.Context, scope core.AuthorizationScope, id string) (BookEdition, error) {
	if r == nil || r.store == nil {
		return BookEdition{}, ErrLibraryResourceNotFound
	}
	edition, err := r.store.GetBookEdition(ctx, id)
	if err != nil {
		return BookEdition{}, ErrLibraryResourceNotFound
	}
	resource, err := r.store.GetLibraryResourcePolicy(ctx, ResourceEdition, id)
	if err != nil || !core.Authorize(scope, resource.Policy, core.CapabilityReadSource).Allowed {
		return BookEdition{}, ErrLibraryResourceNotFound
	}
	return edition, nil
}

func (r *AuthorizedRepository) ListEditions(ctx context.Context, scope core.AuthorizationScope) ([]BookEdition, error) {
	if r == nil || r.store == nil || scope.Validate() != nil {
		return []BookEdition{}, nil
	}
	editions, err := r.store.ListBookEditions(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]BookEdition, 0, len(editions))
	for _, edition := range editions {
		resource, err := r.store.GetLibraryResourcePolicy(ctx, ResourceEdition, edition.ID)
		if err == nil && core.Authorize(scope, resource.Policy, core.CapabilityReadSource).Allowed {
			visible = append(visible, edition)
		}
	}
	return visible, nil
}

func (r *AuthorizedRepository) CountEditions(ctx context.Context, scope core.AuthorizationScope) (int, error) {
	editions, err := r.ListEditions(ctx, scope)
	return len(editions), err
}
