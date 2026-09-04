package objectcustody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrGraphObjectAlreadyExists = errors.New("graph object already exists")

type GraphArtifactObjects interface {
	PutImmutable(context.Context, string, []byte, time.Time) error
	Get(context.Context, string) ([]byte, error)
}

type GraphArtifactCustody struct {
	objects         GraphArtifactObjects
	now             func() time.Time
	bundlePublicKey ed25519.PublicKey
}

func NewVerifiedGraphArtifactCustody(objects GraphArtifactObjects, bundlePublicKey ed25519.PublicKey, now func() time.Time) (*GraphArtifactCustody, error) {
	if objects == nil || len(bundlePublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("verified graph artifact custody dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &GraphArtifactCustody{objects: objects, now: now, bundlePublicKey: append(ed25519.PublicKey(nil), bundlePublicKey...)}, nil
}

func NewGraphArtifactCustody(objects GraphArtifactObjects, now func() time.Time) *GraphArtifactCustody {
	if now == nil {
		now = time.Now
	}
	return &GraphArtifactCustody{objects: objects, now: now}
}

func GraphProjectionPrefix(scope core.GraphScope, revisionID string) (string, error) {
	if err := validateHostedGraphObjectIdentity(scope, revisionID); err != nil {
		return "", err
	}
	return path.Join("graph-projections", scope.TenantID, scope.WorkspaceID, revisionID) + "/", nil
}

func GraphArtifactStagingPrefix(scope core.GraphScope, jobID, revisionID string) (string, error) {
	if err := validateHostedGraphObjectIdentity(scope, jobID, revisionID); err != nil {
		return "", err
	}
	return path.Join("graph-artifacts", "staging", scope.TenantID, scope.WorkspaceID, jobID, revisionID) + "/", nil
}

func GraphAdapterStatePrefix(scope core.GraphScope, revisionID string) (string, error) {
	if err := validateHostedGraphObjectIdentity(scope, revisionID); err != nil {
		return "", err
	}
	return path.Join("graph-artifacts", "state", scope.TenantID, scope.WorkspaceID, revisionID) + "/", nil
}

func (s *GraphArtifactCustody) ReadAdapterState(ctx context.Context, scope core.GraphScope, revisionID string) (map[string][]byte, contracts.GraphAdapterStateManifest, error) {
	if s == nil || s.objects == nil {
		return nil, contracts.GraphAdapterStateManifest{}, fmt.Errorf("graph artifact custody is required")
	}
	prefix, err := GraphAdapterStatePrefix(scope, revisionID)
	if err != nil {
		return nil, contracts.GraphAdapterStateManifest{}, err
	}
	encoded, err := s.objects.Get(ctx, prefix+"manifest.json")
	if err != nil {
		return nil, contracts.GraphAdapterStateManifest{}, err
	}
	var manifest contracts.GraphAdapterStateManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.Scope != scope || manifest.RevisionID != revisionID {
		return nil, contracts.GraphAdapterStateManifest{}, fmt.Errorf("graph adapter state identity is invalid")
	}
	files := make(map[string][]byte, len(manifest.Files))
	for _, file := range manifest.Files {
		value, getErr := s.objects.Get(ctx, prefix+file.Name)
		if getErr != nil {
			return nil, contracts.GraphAdapterStateManifest{}, getErr
		}
		files[file.Name] = value
	}
	if err := manifest.Validate(files); err != nil {
		return nil, contracts.GraphAdapterStateManifest{}, err
	}
	return files, manifest, nil
}

func (s *GraphArtifactCustody) StageAdapterState(ctx context.Context, scope core.GraphScope, revisionID string, files map[string][]byte, manifest contracts.GraphAdapterStateManifest) (bool, error) {
	if s == nil || s.objects == nil || manifest.Scope != scope || manifest.RevisionID != revisionID {
		return false, fmt.Errorf("graph adapter state custody identity is invalid")
	}
	if err := manifest.Validate(files); err != nil {
		return false, err
	}
	prefix, err := GraphAdapterStatePrefix(scope, revisionID)
	if err != nil {
		return false, err
	}
	expires, coalesced := s.now().UTC().Add(30*24*time.Hour), false
	for _, file := range manifest.Files {
		existing, putErr := s.putCoalesced(ctx, prefix+file.Name, files[file.Name], expires)
		if putErr != nil {
			return false, putErr
		}
		coalesced = coalesced || existing
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return false, err
	}
	existing, err := s.putCoalesced(ctx, prefix+"manifest.json", append(encoded, '\n'), expires)
	return coalesced || existing, err
}

func (s *GraphArtifactCustody) ReadProjection(ctx context.Context, scope core.GraphScope, revisionID string) ([]byte, []byte, []byte, error) {
	if s == nil || s.objects == nil {
		return nil, nil, nil, fmt.Errorf("graph artifact custody is required")
	}
	prefix, err := GraphProjectionPrefix(scope, revisionID)
	if err != nil {
		return nil, nil, nil, err
	}
	manifest, err := s.objects.Get(ctx, prefix+"manifest.json")
	if err != nil {
		return nil, nil, nil, err
	}
	projection, err := s.objects.Get(ctx, prefix+"projection.jsonl")
	if err != nil {
		return nil, nil, nil, err
	}
	correlations, err := s.objects.Get(ctx, prefix+"correlation.jsonl")
	if err != nil {
		return nil, nil, nil, err
	}
	if len(s.bundlePublicKey) > 0 {
		var bundle application.GraphBundleManifest
		if err := json.Unmarshal(manifest, &bundle); err != nil || bundle.WorkspaceID != scope.WorkspaceID || bundle.RevisionID != revisionID {
			return nil, nil, nil, fmt.Errorf("graph projection bundle identity is invalid")
		}
		if err := application.VerifyGraphBundleManifest(bundle, s.bundlePublicKey); err != nil {
			return nil, nil, nil, err
		}
		files := map[string][]byte{"projection.jsonl": projection, "correlation.jsonl": correlations}
		if len(bundle.Files) != len(files) {
			return nil, nil, nil, fmt.Errorf("graph projection bundle file count mismatch")
		}
		for _, file := range bundle.Files {
			value, ok := files[file.Name]
			digest := sha256.Sum256(value)
			if !ok || file.Bytes != int64(len(value)) || file.SHA256 != hex.EncodeToString(digest[:]) {
				return nil, nil, nil, fmt.Errorf("graph projection bundle digest mismatch")
			}
		}
	}
	return projection, correlations, manifest, nil
}

func (s *GraphArtifactCustody) Stage(ctx context.Context, scope core.GraphScope, jobID, revisionID string, files map[string][]byte, manifest contracts.GraphArtifactManifest) (string, bool, error) {
	if s == nil || s.objects == nil {
		return "", false, fmt.Errorf("graph artifact custody is required")
	}
	if err := manifest.Validate(); err != nil {
		return "", false, err
	}
	if manifest.Scope != scope || manifest.JobID != jobID || manifest.RevisionID != revisionID || manifest.Status != contracts.GraphArtifactCompleted {
		return "", false, fmt.Errorf("graph artifact manifest scope or job mismatch")
	}
	prefix, err := GraphArtifactStagingPrefix(scope, jobID, revisionID)
	if err != nil {
		return "", false, err
	}
	if len(files) != len(manifest.Outputs) {
		return "", false, fmt.Errorf("graph artifact output count mismatch")
	}
	outputByName := make(map[string]contracts.GraphArtifactFile, len(manifest.Outputs))
	for _, output := range manifest.Outputs {
		outputByName[output.Name] = output
	}
	names := make([]string, 0, len(files))
	for name, value := range files {
		output, ok := outputByName[name]
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(value))
		if !ok || int64(len(value)) != output.Bytes || digest != output.ContentHash {
			return "", false, fmt.Errorf("graph artifact digest mismatch")
		}
		if path.Base(name) != name || name == "manifest.json" {
			return "", false, fmt.Errorf("graph artifact name is not allowed")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	expires := s.now().UTC().Add(30 * 24 * time.Hour)
	coalesced := false
	for _, name := range names {
		existing, putErr := s.putCoalesced(ctx, prefix+name, files[name], expires)
		if putErr != nil {
			return "", false, putErr
		}
		coalesced = coalesced || existing
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		return "", false, err
	}
	existing, err := s.putCoalesced(ctx, prefix+"manifest.json", append(encoded, '\n'), expires)
	if err != nil {
		return "", false, err
	}
	coalesced = coalesced || existing
	return prefix, coalesced, nil
}

func (s *GraphArtifactCustody) putCoalesced(ctx context.Context, key string, value []byte, expires time.Time) (bool, error) {
	err := s.objects.PutImmutable(ctx, key, value, expires)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, ErrGraphObjectAlreadyExists) {
		return false, err
	}
	existing, getErr := s.objects.Get(ctx, key)
	if getErr != nil {
		return false, getErr
	}
	if !bytes.Equal(existing, value) {
		return false, fmt.Errorf("graph object replay digest mismatch")
	}
	return true, nil
}

func validateHostedGraphObjectIdentity(scope core.GraphScope, values ...string) error {
	if err := scope.Validate(); err != nil || scope.TenantID == "" || !graphCustodyIdentity(scope.TenantID) || !graphCustodyIdentity(scope.WorkspaceID) {
		return fmt.Errorf("invalid hosted graph object scope")
	}
	for _, value := range values {
		if !graphCustodyIdentity(value) {
			return fmt.Errorf("invalid hosted graph object identity")
		}
	}
	return nil
}
