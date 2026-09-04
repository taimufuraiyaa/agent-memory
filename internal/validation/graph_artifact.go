package validation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
)

type GraphArtifactPolicy struct {
	CommunitiesEnabled bool
	ReportsEnabled     bool
}

type GraphArtifactEvidence struct {
	CanonicalKind        string `json:"canonical_kind"`
	CanonicalID          string `json:"canonical_id"`
	CanonicalFingerprint string `json:"canonical_fingerprint"`
}

type GraphArtifactEntity struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	Type     string                  `json:"type"`
	Evidence []GraphArtifactEvidence `json:"evidence"`
}

type GraphArtifactRelationship struct {
	ID       string                  `json:"id"`
	SourceID string                  `json:"source_id"`
	TargetID string                  `json:"target_id"`
	Kind     string                  `json:"kind"`
	Evidence []GraphArtifactEvidence `json:"evidence"`
}

type GraphArtifactCommunity struct {
	ID        string   `json:"id"`
	ParentID  string   `json:"parent_id"`
	EntityIDs []string `json:"entity_ids"`
}

type GraphArtifactReport struct {
	ID          string                  `json:"id"`
	CommunityID string                  `json:"community_id"`
	Title       string                  `json:"title"`
	Summary     string                  `json:"summary"`
	Evidence    []GraphArtifactEvidence `json:"evidence"`
}

type ValidatedGraphArtifact struct {
	Manifest      contracts.GraphArtifactManifest
	Entities      []GraphArtifactEntity
	Relationships []GraphArtifactRelationship
	Communities   []GraphArtifactCommunity
	Reports       []GraphArtifactReport
}

var graphArtifactAllowlist = map[string]struct{}{
	"entities.jsonl": {}, "relationships.jsonl": {}, "communities.jsonl": {}, "community_reports.jsonl": {},
}

func ValidateGraphArtifact(ctx context.Context, rootPath string, manifest contracts.GraphArtifactManifest, policy GraphArtifactPolicy) (ValidatedGraphArtifact, error) {
	if err := manifest.Validate(); err != nil {
		return ValidatedGraphArtifact{}, err
	}
	if !filepath.IsAbs(rootPath) {
		return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact root must be absolute")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return ValidatedGraphArtifact{}, err
	}
	defer root.Close()
	files := make(map[string][]byte, len(manifest.Outputs))
	for _, output := range manifest.Outputs {
		if err := ctx.Err(); err != nil {
			return ValidatedGraphArtifact{}, err
		}
		if _, allowed := graphArtifactAllowlist[output.Name]; !allowed || filepath.Base(output.Name) != output.Name {
			return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact output %q is not allowed", output.Name)
		}
		info, err := root.Lstat(output.Name)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != output.Bytes {
			return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact output %q is unsafe or changed", output.Name)
		}
		contents, _, err := readGraphArtifactFile(root, output)
		if err != nil {
			return ValidatedGraphArtifact{}, err
		}
		files[output.Name] = contents
	}
	return ValidateGraphArtifactFiles(ctx, manifest, files, policy)
}

// ValidateGraphArtifactFiles applies the same untrusted supplier checks to
// bytes loaded from immutable object custody rather than a local directory.
func ValidateGraphArtifactFiles(ctx context.Context, manifest contracts.GraphArtifactManifest, files map[string][]byte, policy GraphArtifactPolicy) (ValidatedGraphArtifact, error) {
	if err := manifest.Validate(); err != nil {
		return ValidatedGraphArtifact{}, err
	}
	required := map[string]bool{"entities.jsonl": false, "relationships.jsonl": false}
	if policy.CommunitiesEnabled {
		required["communities.jsonl"] = false
	}
	if policy.ReportsEnabled {
		required["community_reports.jsonl"] = false
	}
	if len(files) != len(manifest.Outputs) {
		return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact file count mismatch")
	}
	validated := ValidatedGraphArtifact{Manifest: manifest}
	for _, output := range manifest.Outputs {
		if err := ctx.Err(); err != nil {
			return ValidatedGraphArtifact{}, err
		}
		if _, allowed := graphArtifactAllowlist[output.Name]; !allowed || filepath.Base(output.Name) != output.Name {
			return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact output %q is not allowed", output.Name)
		}
		contents, ok := files[output.Name]
		if !ok || int64(len(contents)) != output.Bytes {
			return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact output %q is absent or changed", output.Name)
		}
		digest := sha256.Sum256(contents)
		if output.ContentHash != "sha256:"+hex.EncodeToString(digest[:]) || int64(strings.Count(string(contents), "\n")) != output.Rows {
			return ValidatedGraphArtifact{}, fmt.Errorf("graph artifact digest or row count mismatch")
		}
		required[output.Name] = true
		switch output.Name {
		case "entities.jsonl":
			if err := decodeGraphJSONLines(contents, &validated.Entities); err != nil {
				return ValidatedGraphArtifact{}, err
			}
		case "relationships.jsonl":
			if err := decodeGraphJSONLines(contents, &validated.Relationships); err != nil {
				return ValidatedGraphArtifact{}, err
			}
		case "communities.jsonl":
			if err := decodeGraphJSONLines(contents, &validated.Communities); err != nil {
				return ValidatedGraphArtifact{}, err
			}
		case "community_reports.jsonl":
			if err := decodeGraphJSONLines(contents, &validated.Reports); err != nil {
				return ValidatedGraphArtifact{}, err
			}
		}
	}
	for name, present := range required {
		if !present {
			return ValidatedGraphArtifact{}, fmt.Errorf("required graph artifact %q is absent", name)
		}
	}
	if err := validateGraphArtifactReferences(validated); err != nil {
		return ValidatedGraphArtifact{}, err
	}
	return validated, nil
}

func readGraphArtifactFile(root *os.Root, output contracts.GraphArtifactFile) ([]byte, int64, error) {
	file, err := root.Open(output.Name)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, output.Bytes+1))
	if err != nil || int64(len(contents)) != output.Bytes {
		return nil, 0, fmt.Errorf("read graph artifact output %q", output.Name)
	}
	digest := sha256.Sum256(contents)
	if output.ContentHash != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, 0, fmt.Errorf("graph artifact digest mismatch")
	}
	return contents, int64(strings.Count(string(contents), "\n")), nil
}

func decodeGraphJSONLines[T any](contents []byte, destination *[]T) error {
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		var value T
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid graph artifact record: %w", err)
		}
		*destination = append(*destination, value)
	}
	return scanner.Err()
}

func validateGraphArtifactReferences(artifact ValidatedGraphArtifact) error {
	entities := make(map[string]struct{}, len(artifact.Entities))
	for _, entity := range artifact.Entities {
		if !boundedGraphGeneratedText(entity.ID, 256) || !boundedGraphGeneratedText(entity.Name, 16<<10) || !boundedGraphGeneratedText(entity.Type, 256) || len(entity.Evidence) == 0 {
			return fmt.Errorf("graph artifact entity is invalid or evidence-free")
		}
		if _, duplicate := entities[entity.ID]; duplicate {
			return fmt.Errorf("duplicate graph artifact entity")
		}
		entities[entity.ID] = struct{}{}
		if err := validateGraphEvidence(entity.Evidence); err != nil {
			return err
		}
	}
	relationships := map[string]struct{}{}
	for _, relationship := range artifact.Relationships {
		if !boundedGraphGeneratedText(relationship.ID, 256) || !boundedGraphGeneratedText(relationship.Kind, 256) || len(relationship.Evidence) == 0 {
			return fmt.Errorf("graph artifact relationship is invalid or evidence-free")
		}
		if _, duplicate := relationships[relationship.ID]; duplicate {
			return fmt.Errorf("duplicate graph artifact relationship")
		}
		relationships[relationship.ID] = struct{}{}
		if _, exists := entities[relationship.SourceID]; !exists {
			return fmt.Errorf("graph artifact relationship source is unresolved")
		}
		if _, exists := entities[relationship.TargetID]; !exists {
			return fmt.Errorf("graph artifact relationship target is unresolved")
		}
		if err := validateGraphEvidence(relationship.Evidence); err != nil {
			return err
		}
	}
	communities := make(map[string]GraphArtifactCommunity, len(artifact.Communities))
	for _, community := range artifact.Communities {
		if !boundedGraphGeneratedText(community.ID, 256) {
			return fmt.Errorf("graph artifact community is invalid")
		}
		if _, duplicate := communities[community.ID]; duplicate {
			return fmt.Errorf("duplicate graph artifact community")
		}
		for _, entityID := range community.EntityIDs {
			if _, exists := entities[entityID]; !exists {
				return fmt.Errorf("graph artifact community entity is unresolved")
			}
		}
		communities[community.ID] = community
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("graph artifact community hierarchy is cyclic")
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		parent := communities[id].ParentID
		if parent != "" {
			if _, exists := communities[parent]; !exists {
				return fmt.Errorf("graph artifact community parent is unresolved")
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		visiting[id], visited[id] = false, true
		return nil
	}
	for id := range communities {
		if err := visit(id); err != nil {
			return err
		}
	}
	reports := map[string]struct{}{}
	for _, report := range artifact.Reports {
		if !boundedGraphGeneratedText(report.ID, 256) || !boundedGraphGeneratedText(report.Title, 16<<10) || !boundedGraphGeneratedText(report.Summary, 64<<10) || len(report.Evidence) == 0 {
			return fmt.Errorf("graph artifact report is invalid or evidence-free")
		}
		if _, duplicate := reports[report.ID]; duplicate {
			return fmt.Errorf("duplicate graph artifact report")
		}
		reports[report.ID] = struct{}{}
		if _, exists := communities[report.CommunityID]; !exists {
			return fmt.Errorf("graph artifact report community is unresolved")
		}
		if err := validateGraphEvidence(report.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func validateGraphEvidence(evidence []GraphArtifactEvidence) error {
	for _, item := range evidence {
		if !boundedGraphGeneratedText(item.CanonicalKind, 128) || !boundedGraphGeneratedText(item.CanonicalID, 512) || !boundedGraphGeneratedText(item.CanonicalFingerprint, 512) {
			return fmt.Errorf("graph artifact evidence is invalid")
		}
	}
	return nil
}

func boundedGraphGeneratedText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit && !strings.ContainsRune(value, '\x00')
}
