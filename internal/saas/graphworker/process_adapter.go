package graphworker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

type ProcessAdapterConfig struct {
	Executable           string
	JobRoot              string
	CompletionProvider   string
	CompletionModel      string
	EmbeddingProvider    string
	EmbeddingModel       string
	CompletionAPIKey     string
	EmbeddingAPIKey      string
	ProducerIdentity     string
	BuildDigest          string
	AttestationSignature string
	Timeout              time.Duration
	MaxOutputBytes       int64
}

type ProcessAdapter struct{ configuration ProcessAdapterConfig }

func NewProcessAdapter(configuration ProcessAdapterConfig) (*ProcessAdapter, error) {
	for _, value := range []string{configuration.Executable, configuration.JobRoot, configuration.CompletionProvider, configuration.CompletionModel, configuration.EmbeddingProvider, configuration.EmbeddingModel, configuration.ProducerIdentity, configuration.BuildDigest, configuration.AttestationSignature} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("graph process adapter configuration is incomplete")
		}
	}
	if !filepath.IsAbs(configuration.Executable) || !filepath.IsAbs(configuration.JobRoot) || configuration.Timeout < time.Second || configuration.Timeout > 24*time.Hour || configuration.MaxOutputBytes < 1024 || configuration.MaxOutputBytes > 16<<20 {
		return nil, fmt.Errorf("graph process adapter path or bounds are invalid")
	}
	if err := os.MkdirAll(configuration.JobRoot, 0o700); err != nil {
		return nil, err
	}
	return &ProcessAdapter{configuration: configuration}, nil
}

func (a *ProcessAdapter) Index(ctx context.Context, request AdapterRequest) (AdapterResult, error) {
	if a == nil {
		return AdapterResult{}, fmt.Errorf("graph process adapter is required")
	}
	jobRoot, err := os.MkdirTemp(a.configuration.JobRoot, "graph-worker-")
	if err != nil {
		return AdapterResult{}, err
	}
	defer os.RemoveAll(jobRoot)
	if err := os.Chmod(jobRoot, 0o700); err != nil {
		return AdapterResult{}, err
	}
	payload, err := a.requestEnvelope(jobRoot, request)
	if err != nil {
		return AdapterResult{}, err
	}
	requestPath := filepath.Join(jobRoot, "request.json")
	if err := os.WriteFile(requestPath, append(payload, '\n'), 0o600); err != nil {
		return AdapterResult{}, err
	}
	commandName := "full-index"
	if request.Mode == contracts.GraphIndexModeIncremental {
		if err := stageProcessAdapterBase(jobRoot, request); err != nil {
			return AdapterResult{}, err
		}
		commandName = "incremental-update"
	}
	runCtx, cancel := context.WithTimeout(ctx, a.configuration.Timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, a.configuration.Executable, commandName, "--request", requestPath)
	command.Dir = jobRoot
	command.Env = []string{
		"HOME=" + jobRoot, "TMPDIR=" + jobRoot, "PYTHONUNBUFFERED=1", "NO_COLOR=1",
		"INDEX_COMPLETION_API_KEY=" + a.configuration.CompletionAPIKey,
		"INDEX_EMBEDDING_API_KEY=" + a.configuration.EmbeddingAPIKey,
		"AGENT_MEMORY_GRAPHRAG_BUILD_DIGEST=" + a.configuration.BuildDigest,
	}
	stdout, stderr := &boundedAdapterBuffer{limit: a.configuration.MaxOutputBytes}, &boundedAdapterBuffer{limit: a.configuration.MaxOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if runCtx.Err() != nil {
			return AdapterResult{}, fmt.Errorf("graph adapter deadline exceeded")
		}
		return AdapterResult{}, fmt.Errorf("graph adapter failed")
	}
	if stdout.exceeded || stderr.exceeded {
		return AdapterResult{}, fmt.Errorf("graph adapter output exceeded policy")
	}
	var response struct {
		State string `json:"state"`
	}
	if err := decodeAdapterJSON(stdout.Bytes(), &response); err != nil || response.State != "completed" {
		return AdapterResult{}, fmt.Errorf("graph adapter returned an invalid completion")
	}
	result, err := loadProcessAdapterResult(runCtx, jobRoot)
	if err != nil {
		return AdapterResult{}, err
	}
	result.StateFiles, err = loadProcessAdapterState(jobRoot)
	if err != nil {
		return AdapterResult{}, err
	}
	result.StateManifest, err = contracts.BuildGraphAdapterStateManifest(request.Scope, request.RevisionID, result.StateFiles, time.Now().UTC())
	return result, err
}

func (a *ProcessAdapter) requestEnvelope(jobRoot string, request AdapterRequest) ([]byte, error) {
	if err := validateAdapterRequest(request); err != nil {
		return nil, err
	}
	documents, err := decodeProjectionDocuments(request.Projection)
	if err != nil {
		return nil, err
	}
	correlations, err := decodeProjectionCorrelations(request.Correlations)
	if err != nil {
		return nil, err
	}
	manifestHash := sha256.Sum256(request.ProjectionManifest)
	commandName := "full-index"
	payload := map[string]any{
		"scope": request.Scope, "job_id": request.JobID, "configuration_id": request.ConfigurationID,
		"revision_id": request.RevisionID, "method": "standard", "documents": documents,
		"correlations": correlations, "completion_provider": a.configuration.CompletionProvider,
		"completion_model": a.configuration.CompletionModel, "embedding_provider": a.configuration.EmbeddingProvider,
		"embedding_model": a.configuration.EmbeddingModel, "input_manifest_hash": "sha256:" + hex.EncodeToString(manifestHash[:]),
		"producer_identity": a.configuration.ProducerIdentity, "build_digest": a.configuration.BuildDigest,
		"attestation_signature": a.configuration.AttestationSignature,
	}
	if request.Mode == contracts.GraphIndexModeIncremental {
		if request.BaseStateManifest.RevisionID != request.BaseRevisionID || request.BaseStateManifest.Validate(request.BaseState) != nil {
			return nil, fmt.Errorf("incremental hosted graph base state is invalid")
		}
		baseManifest := map[string]any{"revision_id": request.BaseRevisionID, "status": "completed", "state_schema": request.BaseStateManifest.Schema}
		encoded, marshalErr := json.Marshal(baseManifest)
		if marshalErr != nil {
			return nil, marshalErr
		}
		digest := sha256.Sum256(encoded)
		payload["base_revision_id"] = request.BaseRevisionID
		payload["base_manifest_path"] = filepath.Join(jobRoot, "base-state-manifest.json")
		payload["base_manifest_sha256"] = hex.EncodeToString(digest[:])
		commandName = "incremental-update"
	}
	envelope := map[string]any{"contract_version": contracts.GraphAdapterContractV1, "command": commandName, "job_root": jobRoot, "request": payload}
	return json.Marshal(envelope)
}

func validateAdapterRequest(request AdapterRequest) error {
	job := JobEnvelope{Scope: request.Scope, JobID: request.JobID, ConfigurationID: request.ConfigurationID, RevisionID: request.RevisionID, ProjectionRevisionID: request.RevisionID, BaseRevisionID: request.BaseRevisionID, Mode: request.Mode, CreatedAt: time.Now().UTC(), Limits: DefaultWorkspaceLimits()}
	return validateJob(job)
}

func stageProcessAdapterBase(jobRoot string, request AdapterRequest) error {
	if request.BaseStateManifest.RevisionID != request.BaseRevisionID || request.BaseStateManifest.Validate(request.BaseState) != nil {
		return fmt.Errorf("incremental hosted graph base state is invalid")
	}
	outputRoot := filepath.Join(jobRoot, "output")
	for _, file := range request.BaseStateManifest.Files {
		target := filepath.Join(outputRoot, filepath.FromSlash(file.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, request.BaseState[file.Name], 0o600); err != nil {
			return err
		}
	}
	baseManifest := map[string]any{"revision_id": request.BaseRevisionID, "status": "completed", "state_schema": request.BaseStateManifest.Schema}
	encoded, err := json.Marshal(baseManifest)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(jobRoot, "base-state-manifest.json"), encoded, 0o600)
}

func loadProcessAdapterState(jobRoot string) (map[string][]byte, error) {
	root := filepath.Join(jobRoot, "output")
	files := map[string][]byte{}
	var total int64
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == "normalized" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > 2<<30 {
			return fmt.Errorf("graph adapter state file is unsafe")
		}
		name := filepath.ToSlash(relative)
		if filepath.Ext(name) != ".parquet" && name != "context.json" && name != "stats.json" {
			return nil
		}
		value, err := os.ReadFile(current)
		if err != nil || int64(len(value)) != info.Size() {
			return fmt.Errorf("graph adapter state file changed while reading")
		}
		total += int64(len(value))
		if total > 20<<30 || len(files) >= 128 {
			return fmt.Errorf("graph adapter state exceeds policy")
		}
		files[name] = value
		return nil
	})
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("graph adapter state is unavailable: %w", err)
	}
	return files, nil
}

func decodeProjectionDocuments(contents []byte) ([]map[string]string, error) {
	if len(contents) == 0 || len(contents) > int(contracts.MaxGraphProjectionBytes) {
		return nil, fmt.Errorf("graph projection bytes are outside policy")
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	documents := make([]map[string]string, 0)
	for scanner.Scan() {
		var row struct {
			ID          string    `json:"id"`
			Text        string    `json:"text"`
			Kind        string    `json:"kind"`
			Fingerprint string    `json:"fingerprint"`
			Token       string    `json:"correlation_token"`
			SourceID    string    `json:"source_id,omitempty"`
			EditionID   string    `json:"edition_id,omitempty"`
			AssetID     string    `json:"asset_id,omitempty"`
			PassageID   string    `json:"passage_id,omitempty"`
			EventTime   time.Time `json:"event_time"`
		}
		if err := decodeAdapterJSON(scanner.Bytes(), &row); err != nil || strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.Text) == "" || strings.TrimSpace(row.Token) == "" {
			return nil, fmt.Errorf("graph projection record is invalid")
		}
		documents = append(documents, map[string]string{"id": row.Token, "title": row.Kind, "text": row.Text})
		if len(documents) > 100_000 {
			return nil, fmt.Errorf("graph projection document count exceeds policy")
		}
	}
	if err := scanner.Err(); err != nil || len(documents) == 0 {
		return nil, fmt.Errorf("graph projection is empty or unreadable")
	}
	return documents, nil
}

func decodeProjectionCorrelations(contents []byte) (map[string]map[string]string, error) {
	var correlations map[string]map[string]string
	if err := decodeAdapterJSON(contents, &correlations); err != nil || len(correlations) == 0 {
		return nil, fmt.Errorf("graph projection correlations are invalid")
	}
	for token, reference := range correlations {
		if strings.TrimSpace(token) == "" || reference["canonical_kind"] == "" || reference["canonical_id"] == "" || reference["canonical_fingerprint"] == "" {
			return nil, fmt.Errorf("graph projection correlation is incomplete")
		}
	}
	return correlations, nil
}

func loadProcessAdapterResult(ctx context.Context, jobRoot string) (AdapterResult, error) {
	root := filepath.Join(jobRoot, "output", "normalized")
	manifestBytes, err := os.ReadFile(filepath.Join(root, "artifact-manifest.json"))
	if err != nil {
		return AdapterResult{}, err
	}
	var manifest contracts.GraphArtifactManifest
	if err := decodeAdapterJSON(manifestBytes, &manifest); err != nil {
		return AdapterResult{}, err
	}
	if _, err := validation.ValidateGraphArtifact(ctx, root, manifest, validation.GraphArtifactPolicy{CommunitiesEnabled: hasAdapterOutput(manifest, "communities.jsonl"), ReportsEnabled: hasAdapterOutput(manifest, "community_reports.jsonl")}); err != nil {
		return AdapterResult{}, err
	}
	files := make(map[string][]byte, len(manifest.Outputs))
	for _, output := range manifest.Outputs {
		contents, readErr := os.ReadFile(filepath.Join(root, output.Name))
		if readErr != nil {
			return AdapterResult{}, readErr
		}
		files[output.Name] = contents
	}
	return AdapterResult{Files: files, Manifest: manifest}, nil
}

func hasAdapterOutput(manifest contracts.GraphArtifactManifest, name string) bool {
	for _, output := range manifest.Outputs {
		if output.Name == name {
			return true
		}
	}
	return false
}

func decodeAdapterJSON(contents []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return err
	}
	return nil
}

type boundedAdapterBuffer struct {
	bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *boundedAdapterBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - int64(b.Len())
	if remaining <= 0 {
		b.exceeded = true
		return original, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
		b.exceeded = true
	}
	_, _ = b.Buffer.Write(value)
	return original, nil
}
