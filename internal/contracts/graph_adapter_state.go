package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const GraphAdapterStateSchemaV1 = "graph-adapter-state/v1"

type GraphAdapterStateFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type GraphAdapterStateManifest struct {
	Schema     string                  `json:"schema"`
	Scope      core.GraphScope         `json:"scope"`
	RevisionID string                  `json:"revision_id"`
	Files      []GraphAdapterStateFile `json:"files"`
	CreatedAt  time.Time               `json:"created_at"`
}

func BuildGraphAdapterStateManifest(scope core.GraphScope, revisionID string, files map[string][]byte, createdAt time.Time) (GraphAdapterStateManifest, error) {
	manifest := GraphAdapterStateManifest{Schema: GraphAdapterStateSchemaV1, Scope: scope, RevisionID: revisionID, CreatedAt: createdAt.UTC()}
	for name, value := range files {
		digest := sha256.Sum256(value)
		manifest.Files = append(manifest.Files, GraphAdapterStateFile{Name: name, Bytes: int64(len(value)), SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Name < manifest.Files[j].Name })
	if err := manifest.Validate(files); err != nil {
		return GraphAdapterStateManifest{}, err
	}
	return manifest, nil
}

func (m GraphAdapterStateManifest) Validate(files map[string][]byte) error {
	if m.Schema != GraphAdapterStateSchemaV1 || m.Scope.Validate() != nil || strings.TrimSpace(m.Scope.TenantID) == "" || strings.TrimSpace(m.RevisionID) == "" || m.CreatedAt.IsZero() || len(m.Files) == 0 || len(m.Files) > 128 || len(files) != len(m.Files) {
		return fmt.Errorf("invalid graph adapter state manifest")
	}
	var total int64
	seen := map[string]struct{}{}
	for _, file := range m.Files {
		value, ok := files[file.Name]
		digest := sha256.Sum256(value)
		if !ok || path.Clean(file.Name) != file.Name || strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "..") || strings.Contains(file.Name, "\\") || file.Bytes != int64(len(value)) || file.Bytes < 1 || file.SHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("invalid graph adapter state file")
		}
		if _, duplicate := seen[file.Name]; duplicate {
			return fmt.Errorf("duplicate graph adapter state file")
		}
		seen[file.Name] = struct{}{}
		total += file.Bytes
	}
	if total > 20<<30 {
		return fmt.Errorf("graph adapter state exceeds policy")
	}
	return nil
}
