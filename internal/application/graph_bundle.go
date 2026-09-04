package application

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const GraphProjectionRetention = 24 * time.Hour

var graphBundleIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type GraphBundleInput struct {
	Scope       core.GraphScope
	RevisionID  string
	Projection  []byte
	Correlation []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type GraphBundleFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type GraphBundleManifest struct {
	Schema      string            `json:"schema"`
	WorkspaceID string            `json:"workspace_id"`
	RevisionID  string            `json:"revision_id"`
	Files       []GraphBundleFile `json:"files"`
	CreatedAt   time.Time         `json:"created_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	Signature   string            `json:"signature"`
}

type GraphBundle struct {
	Path     string
	Manifest GraphBundleManifest
}

type LocalGraphBundleStore struct {
	root       string
	signingKey ed25519.PrivateKey
}

func NewLocalGraphBundleStore(root string, signingKey ed25519.PrivateKey) (*LocalGraphBundleStore, error) {
	if !filepath.IsAbs(root) || len(signingKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("absolute graph bundle root and Ed25519 key are required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("graph bundle root must be a real directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	return &LocalGraphBundleStore{root: root, signingKey: append(ed25519.PrivateKey(nil), signingKey...)}, nil
}

func (s *LocalGraphBundleStore) Create(ctx context.Context, input GraphBundleInput) (GraphBundle, error) {
	if err := validateGraphBundleInput(input); err != nil {
		return GraphBundle{}, err
	}
	if err := ctx.Err(); err != nil {
		return GraphBundle{}, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return GraphBundle{}, err
	}
	defer root.Close()
	workspace := filepath.Join("bundles", input.Scope.WorkspaceID)
	if err := rejectGraphBundleSymlink(root, workspace); err != nil {
		return GraphBundle{}, err
	}
	if err := root.MkdirAll(workspace, 0o700); err != nil {
		return GraphBundle{}, err
	}
	finalName := filepath.Join(workspace, input.RevisionID)
	if _, err := root.Lstat(finalName); err == nil {
		return GraphBundle{}, fmt.Errorf("graph bundle already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return GraphBundle{}, err
	}
	temporaryName := filepath.Join(workspace, ".pending-"+uuid.NewString())
	if err := root.Mkdir(temporaryName, 0o700); err != nil {
		return GraphBundle{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.RemoveAll(temporaryName)
		}
	}()
	files := map[string][]byte{"projection.jsonl": input.Projection, "correlation.jsonl": input.Correlation}
	manifest, err := BuildGraphBundleManifest(input, s.signingKey)
	if err != nil {
		return GraphBundle{}, err
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return GraphBundle{}, err
	}
	files["manifest.json"] = append(manifestBytes, '\n')
	for name, contents := range files {
		file, err := root.OpenFile(filepath.Join(temporaryName, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return GraphBundle{}, err
		}
		if _, err = file.Write(contents); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return GraphBundle{}, err
		}
		if closeErr != nil {
			return GraphBundle{}, closeErr
		}
		if err := root.Chmod(filepath.Join(temporaryName, name), 0o400); err != nil {
			return GraphBundle{}, err
		}
	}
	if err := root.Chmod(temporaryName, 0o500); err != nil {
		return GraphBundle{}, err
	}
	if err := root.Rename(temporaryName, finalName); err != nil {
		return GraphBundle{}, err
	}
	committed = true
	return GraphBundle{Path: filepath.Join(s.root, finalName), Manifest: manifest}, nil
}

// BuildGraphBundleManifest signs the same immutable projection contract for
// local filesystem and hosted object custody.
func BuildGraphBundleManifest(input GraphBundleInput, signingKey ed25519.PrivateKey) (GraphBundleManifest, error) {
	if err := validateGraphBundleInput(input); err != nil {
		return GraphBundleManifest{}, err
	}
	if len(signingKey) != ed25519.PrivateKeySize {
		return GraphBundleManifest{}, fmt.Errorf("graph bundle signing key is invalid")
	}
	files := map[string][]byte{"projection.jsonl": input.Projection, "correlation.jsonl": input.Correlation}
	manifest := GraphBundleManifest{Schema: "agent-memory-graph-projection-bundle/v1", WorkspaceID: input.Scope.WorkspaceID, RevisionID: input.RevisionID, CreatedAt: input.CreatedAt.UTC(), ExpiresAt: input.ExpiresAt.UTC()}
	for name, contents := range files {
		digest := sha256.Sum256(contents)
		manifest.Files = append(manifest.Files, GraphBundleFile{Name: name, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(contents))})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Name < manifest.Files[j].Name })
	unsigned, err := graphBundleManifestBytes(manifest)
	if err != nil {
		return GraphBundleManifest{}, err
	}
	manifest.Signature = hex.EncodeToString(ed25519.Sign(signingKey, unsigned))
	return manifest, nil
}

func VerifyGraphBundleManifest(manifest GraphBundleManifest, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("graph bundle public key is invalid")
	}
	signature, err := hex.DecodeString(manifest.Signature)
	if err != nil {
		return err
	}
	unsigned, err := graphBundleManifestBytes(manifest)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, unsigned, signature) {
		return fmt.Errorf("graph bundle signature is invalid")
	}
	return nil
}

// CleanupExpired removes only finalized bundles whose manifest expiry has
// passed. It never accepts a caller-selected path.
func (s *LocalGraphBundleStore) CleanupExpired(ctx context.Context, now time.Time) (int, error) {
	if s == nil || now.IsZero() {
		return 0, fmt.Errorf("graph bundle cleanup is not configured")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	workspaces, err := os.ReadDir(filepath.Join(s.root, "bundles"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !workspace.IsDir() || !graphBundleIdentity.MatchString(workspace.Name()) {
			continue
		}
		workspaceName := filepath.Join("bundles", workspace.Name())
		revisions, err := os.ReadDir(filepath.Join(s.root, workspaceName))
		if err != nil {
			return removed, err
		}
		for _, revision := range revisions {
			if !revision.IsDir() || !graphBundleIdentity.MatchString(revision.Name()) {
				continue
			}
			bundleName := filepath.Join(workspaceName, revision.Name())
			if err := rejectGraphBundleSymlink(root, bundleName); err != nil {
				return removed, err
			}
			encoded, err := root.ReadFile(filepath.Join(bundleName, "manifest.json"))
			if err != nil {
				return removed, err
			}
			var manifest GraphBundleManifest
			if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.WorkspaceID != workspace.Name() || manifest.RevisionID != revision.Name() {
				return removed, fmt.Errorf("graph bundle cleanup encountered invalid manifest")
			}
			if err := VerifyGraphBundleManifest(manifest, s.signingKey.Public().(ed25519.PublicKey)); err != nil {
				return removed, err
			}
			if now.UTC().Before(manifest.ExpiresAt) {
				continue
			}
			if err := root.Chmod(bundleName, 0o700); err != nil {
				return removed, err
			}
			for _, name := range []string{"projection.jsonl", "correlation.jsonl", "manifest.json"} {
				if err := root.Chmod(filepath.Join(bundleName, name), 0o600); err != nil {
					return removed, err
				}
			}
			if err := root.RemoveAll(bundleName); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

func graphBundleManifestBytes(manifest GraphBundleManifest) ([]byte, error) {
	manifest.Signature = ""
	return json.Marshal(manifest)
}

func validateGraphBundleInput(input GraphBundleInput) error {
	if err := input.Scope.Validate(); err != nil {
		return err
	}
	if !graphBundleIdentity.MatchString(input.Scope.WorkspaceID) || !graphBundleIdentity.MatchString(input.RevisionID) || len(input.Projection) == 0 || len(input.Correlation) == 0 {
		return fmt.Errorf("graph bundle identity or content is invalid")
	}
	if input.CreatedAt.IsZero() || !input.ExpiresAt.After(input.CreatedAt) || input.ExpiresAt.Sub(input.CreatedAt) > GraphProjectionRetention {
		return fmt.Errorf("graph bundle expiry exceeds retention policy")
	}
	return nil
}

func rejectGraphBundleSymlink(root *os.Root, name string) error {
	current := ""
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("graph bundle parent is not a real directory")
		}
	}
	return nil
}
