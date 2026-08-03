package library_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestPrivateAnnotationOnSharedEditionRequiresExplicitPromotion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "annotations.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work-1", Title: "Book", NormalizedTitle: "book"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookEdition(ctx, library.BookEdition{ID: "edition-1", WorkID: "work-1", Label: "Edition", Language: "en", ContentFingerprint: "sha256:text"}); err != nil {
		t.Fatal(err)
	}
	service := library.NewAnnotationService(store)
	user1 := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilityAnnotate, core.CapabilityProposeKnowledge}, PolicyVersion: "membership-v1",
	}
	user2 := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-2", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilityAnnotate}, PolicyVersion: "membership-v1",
	}
	annotation := library.Annotation{
		ID: "annotation-1", EditionID: "edition-1", Content: "My private interpretation",
		Owner: user1.Principal, Visibility: core.VisibilityPrivate, CreatedAt: time.Now().UTC(),
	}
	if err := service.Create(ctx, user1, annotation); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Get(ctx, user2, annotation.ID); !errors.Is(err, library.ErrLibraryResourceNotFound) {
		t.Fatalf("other user must not discover private annotation: %v", err)
	}
	if err := service.PromoteToOrganization(ctx, user1, annotation.ID, "org-1"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	listed, err := service.ListEdition(ctx, user2, annotation.EditionID)
	if err != nil || len(listed) != 1 || listed[0].Visibility != core.VisibilityOrganization {
		t.Fatalf("promoted annotation should be visible to organization peer: %+v err=%v", listed, err)
	}
}
