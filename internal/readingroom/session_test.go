package readingroom_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestStudySessionPersistsAttributedTurnsWithoutCreatingMemory(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "study.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := core.Principal{ID: "reader-1", Kind: core.PrincipalUser}
	policy := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: owner, Visibility: core.VisibilityPrivate}}
	session := readingroom.StudySession{ID: "session-1", Workspace: "books", Owner: owner, Scope: readingroom.StudyScope{LibraryID: "library-1", EditionIDs: []string{"edition-1"}}, Policy: policy, Retention: readingroom.SessionRetentionRaw, CreatedAt: time.Now().UTC()}
	service := readingroom.NewStudySessionService(store)
	if err := service.Start(ctx, session); err != nil {
		t.Fatal(err)
	}
	ownerScope := core.AuthorizationScope{Principal: owner, Capabilities: []core.Capability{core.CapabilityDiscuss}, PolicyVersion: "v1"}
	turn := readingroom.StudyTurn{ID: "turn-1", SessionID: session.ID, Principal: owner, Content: "Does the proverb imply that outcomes are invariant?", EvidencePacketFingerprint: "packet-1", CreatedAt: time.Now().UTC()}
	if err := service.AddTurn(ctx, ownerScope, turn); err != nil {
		t.Fatal(err)
	}
	turns, err := service.Turns(ctx, ownerScope, session.ID)
	if err != nil || len(turns) != 1 || turns[0].Principal != owner {
		t.Fatalf("unexpected turns: %+v err=%v", turns, err)
	}
	count, err := store.CountMemories(ctx)
	if err != nil || count != 0 {
		t.Fatalf("raw turn became durable memory: count=%d err=%v", count, err)
	}
	peerScope := core.AuthorizationScope{Principal: core.Principal{ID: "peer", Kind: core.PrincipalUser}, Capabilities: []core.Capability{core.CapabilityDiscuss}, PolicyVersion: "v1"}
	if _, err := service.Turns(ctx, peerScope, session.ID); err == nil {
		t.Fatal("private session leaked to peer")
	}
}
