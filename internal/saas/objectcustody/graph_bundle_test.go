package objectcustody

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphObjectStoreFixture struct{ values map[string][]byte }

func (s *graphObjectStoreFixture) PutImmutable(_ context.Context, key string, value []byte, _ time.Time) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	if _, exists := s.values[key]; exists {
		return ErrGraphObjectAlreadyExists
	}
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *graphObjectStoreFixture) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func (s *graphObjectStoreFixture) DeletePrefix(_ context.Context, prefix string) error {
	for key := range s.values {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(s.values, key)
		}
	}
	return nil
}

func (s *graphObjectStoreFixture) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestGraphProjectionCustodyUsesTenantWorkspacePrefixAndManifestLast(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	local, err := application.NewLocalGraphBundleStore(t.TempDir(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	objects := &graphObjectStoreFixture{}
	store := NewGraphBundleObjectStore(objects, publicKey)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	files := map[string][]byte{"projection.jsonl": []byte("projection"), "correlation.jsonl": []byte("correlation")}
	bundle, err := local.Create(context.Background(), application.GraphBundleInput{Scope: core.GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-a", Projection: files["projection.jsonl"], Correlation: files["correlation.jsonl"], CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = local.CleanupExpired(context.Background(), now.Add(25*time.Hour)) })
	manifest := bundle.Manifest
	prefix, err := store.Put(context.Background(), core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, "revision-a", files, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "graph-projections/tenant-a/workspace-a/revision-a/" || len(objects.values) != 3 {
		t.Fatalf("prefix=%q objects=%v", prefix, objects.values)
	}
	if _, ok := objects.values[prefix+"manifest.json"]; !ok {
		t.Fatal("finalization manifest missing")
	}
	if _, err := store.Put(context.Background(), core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, "revision-a", map[string][]byte{"projection.jsonl": []byte("replace")}, manifest); err == nil {
		t.Fatal("immutable object bundle was replaced")
	}
}

func TestGraphProjectionCustodyRejectsCrossWorkspaceManifest(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := NewGraphBundleObjectStore(&graphObjectStoreFixture{}, publicKey)
	now := time.Now().UTC()
	_, err = store.Put(context.Background(), core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, "revision-a", map[string][]byte{"projection.jsonl": []byte("x")}, application.GraphBundleManifest{WorkspaceID: "workspace-b", RevisionID: "revision-a", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err == nil {
		t.Fatal("cross-workspace manifest accepted")
	}
}
