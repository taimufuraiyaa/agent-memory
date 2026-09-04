package objectcustody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphBundleObjects interface {
	PutImmutable(context.Context, string, []byte, time.Time) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
	DeletePrefix(context.Context, string) error
}

type GraphBundleObjectStore struct {
	objects   GraphBundleObjects
	publicKey ed25519.PublicKey
}

func NewGraphBundleObjectStore(objects GraphBundleObjects, publicKey ed25519.PublicKey) *GraphBundleObjectStore {
	return &GraphBundleObjectStore{objects: objects, publicKey: append(ed25519.PublicKey(nil), publicKey...)}
}

func (s *GraphBundleObjectStore) Put(ctx context.Context, scope core.GraphScope, revisionID string, files map[string][]byte, manifest application.GraphBundleManifest) (string, error) {
	if s == nil || s.objects == nil {
		return "", fmt.Errorf("graph bundle object store is required")
	}
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if scope.TenantID == "" || !graphCustodyIdentity(scope.TenantID) || !graphCustodyIdentity(scope.WorkspaceID) || !graphCustodyIdentity(revisionID) || manifest.WorkspaceID != scope.WorkspaceID || manifest.RevisionID != revisionID || manifest.ExpiresAt.Sub(manifest.CreatedAt) > application.GraphProjectionRetention || !manifest.ExpiresAt.After(manifest.CreatedAt) {
		return "", fmt.Errorf("graph projection custody identity or retention is invalid")
	}
	if err := application.VerifyGraphBundleManifest(manifest, s.publicKey); err != nil {
		return "", err
	}
	prefix := path.Join("graph-projections", scope.TenantID, scope.WorkspaceID, revisionID) + "/"
	claimKey := prefix + ".claim"
	if err := s.objects.PutImmutable(ctx, claimKey, []byte("graph-projection-claim-v1\n"), manifest.ExpiresAt); err != nil {
		return "", err
	}
	created := []string{claimKey}
	committed := false
	defer func() {
		if !committed {
			for index := len(created) - 1; index >= 0; index-- {
				_ = s.objects.Delete(ctx, created[index])
			}
		}
	}()
	manifestFiles := make(map[string]application.GraphBundleFile, len(manifest.Files))
	for _, file := range manifest.Files {
		manifestFiles[file.Name] = file
	}
	if len(files) != len(manifestFiles) {
		return "", fmt.Errorf("graph projection object count does not match manifest")
	}
	names := make([]string, 0, len(files))
	for name, contents := range files {
		if name != "projection.jsonl" && name != "correlation.jsonl" {
			return "", fmt.Errorf("graph projection object name is not allowed")
		}
		file, ok := manifestFiles[name]
		digest := fmt.Sprintf("%x", sha256.Sum256(contents))
		if !ok || file.Bytes != int64(len(contents)) || file.SHA256 != digest {
			return "", fmt.Errorf("graph projection object does not match manifest")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		wasCreated, err := s.putCoalesced(ctx, prefix+name, files[name], manifest.ExpiresAt)
		if err != nil {
			return "", err
		}
		if wasCreated {
			created = append(created, prefix+name)
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifestKey := prefix + "manifest.json"
	wasCreated, err := s.putCoalesced(ctx, manifestKey, append(encoded, '\n'), manifest.ExpiresAt)
	if err != nil {
		return "", err
	}
	if wasCreated {
		created = append(created, manifestKey)
	}
	if err := s.objects.Delete(ctx, claimKey); err != nil {
		return "", err
	}
	committed = true
	return prefix, nil
}

func (s *GraphBundleObjectStore) putCoalesced(ctx context.Context, key string, value []byte, expires time.Time) (bool, error) {
	err := s.objects.PutImmutable(ctx, key, value, expires)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrGraphObjectAlreadyExists) {
		return false, err
	}
	existing, getErr := s.objects.Get(ctx, key)
	if getErr != nil {
		return false, getErr
	}
	if !bytes.Equal(existing, value) {
		return false, fmt.Errorf("graph projection replay digest mismatch")
	}
	return false, nil
}

func graphCustodyIdentity(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || (index > 0 && (character == '.' || character == '_' || character == '-')) {
			continue
		}
		return false
	}
	return true
}
