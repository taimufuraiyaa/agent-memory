package contracts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	GraphProjectionContractV1 = "graph-projection/v1"
	GraphAdapterContractV1    = "graph-adapter/v1"
	GraphArtifactSchemaV1     = "graph-artifact/v1"

	MaxGraphProjectionFiles     = 16
	MaxGraphProjectionDocuments = 1_000_000
	MaxGraphProjectionTextUnits = 10_000_000
	MaxGraphProjectionBytes     = int64(20 << 30)
	MaxGraphArtifactFiles       = 64
	MaxGraphArtifactFileBytes   = int64(20 << 30)
	MaxGraphArtifactRows        = int64(100_000_000)
	MaxGraphManifestStringBytes = 4096
)

type GraphIndexMode string

const (
	GraphIndexModeFull        GraphIndexMode = "full"
	GraphIndexModeIncremental GraphIndexMode = "incremental"
)

type GraphProjectionFile struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Records     int64  `json:"records"`
	Bytes       int64  `json:"bytes"`
	ContentHash string `json:"content_hash"`
}

type GraphProjectionManifest struct {
	ContractVersion         string                `json:"contract_version"`
	ProjectionPolicyVersion string                `json:"projection_policy_version"`
	Scope                   core.GraphScope       `json:"scope"`
	ConfigurationID         string                `json:"configuration_id"`
	JobID                   string                `json:"job_id"`
	RevisionID              string                `json:"revision_id"`
	Mode                    GraphIndexMode        `json:"mode"`
	BaseRevisionID          string                `json:"base_revision_id,omitempty"`
	Cutoff                  core.GraphWatermark   `json:"cutoff"`
	EventTimeStart          time.Time             `json:"event_time_start"`
	EventTimeEnd            time.Time             `json:"event_time_end"`
	Files                   []GraphProjectionFile `json:"files"`
	DocumentCount           int64                 `json:"document_count"`
	TextUnitCount           int64                 `json:"text_unit_count"`
	TotalBytes              int64                 `json:"total_bytes"`
	CorrelationMapHash      string                `json:"correlation_map_hash"`
	CorrelationMapLocation  string                `json:"correlation_map_location"`
	ModelRoutes             []string              `json:"model_routes"`
	PromptFingerprint       string                `json:"prompt_fingerprint"`
	RetentionClass          string                `json:"retention_class"`
	Sensitivity             string                `json:"sensitivity"`
	CreatedAt               time.Time             `json:"created_at"`
	ExpiresAt               time.Time             `json:"expires_at"`
	ProducerIdentity        string                `json:"producer_identity"`
}

func (m GraphProjectionManifest) Validate() error {
	if m.ContractVersion != GraphProjectionContractV1 {
		return fmt.Errorf("%w: unsupported projection contract %q", ErrInvalidGraphIndexRequest, m.ContractVersion)
	}
	if err := m.Scope.Validate(); err != nil {
		return err
	}
	if err := requireManifestStrings(
		m.ProjectionPolicyVersion, m.ConfigurationID, m.JobID, m.RevisionID,
		m.Cutoff.Digest, m.CorrelationMapHash, m.CorrelationMapLocation,
		m.PromptFingerprint, m.RetentionClass, m.Sensitivity, m.ProducerIdentity,
	); err != nil {
		return err
	}
	if m.Mode != GraphIndexModeFull && m.Mode != GraphIndexModeIncremental {
		return fmt.Errorf("%w: unsupported projection mode %q", ErrInvalidGraphIndexRequest, m.Mode)
	}
	if m.Mode == GraphIndexModeIncremental && strings.TrimSpace(m.BaseRevisionID) == "" {
		return fmt.Errorf("%w: incremental projection requires base_revision_id", ErrInvalidGraphIndexRequest)
	}
	if m.BaseRevisionID == m.RevisionID {
		return fmt.Errorf("%w: base and candidate revisions must differ", ErrInvalidGraphIndexRequest)
	}
	if m.Cutoff.Sequence < 0 || m.EventTimeEnd.Before(m.EventTimeStart) || !m.ExpiresAt.After(m.CreatedAt) {
		return fmt.Errorf("%w: invalid watermark or time range", ErrInvalidGraphIndexRequest)
	}
	if m.DocumentCount < 0 || m.DocumentCount > MaxGraphProjectionDocuments ||
		m.TextUnitCount < 0 || m.TextUnitCount > MaxGraphProjectionTextUnits ||
		m.TotalBytes < 0 || m.TotalBytes > MaxGraphProjectionBytes {
		return fmt.Errorf("%w: projection totals exceed bounds", ErrInvalidGraphIndexRequest)
	}
	if len(m.Files) == 0 || len(m.Files) > MaxGraphProjectionFiles {
		return fmt.Errorf("%w: projection file count is out of bounds", ErrInvalidGraphIndexRequest)
	}
	var totalBytes int64
	seen := make(map[string]struct{}, len(m.Files))
	for _, file := range m.Files {
		if err := requireManifestStrings(file.Name, file.Kind, file.ContentHash); err != nil {
			return err
		}
		if _, exists := seen[file.Name]; exists {
			return fmt.Errorf("%w: duplicate projection file %q", ErrInvalidGraphIndexRequest, file.Name)
		}
		seen[file.Name] = struct{}{}
		if file.Records < 0 || file.Bytes < 0 || file.Bytes > MaxGraphProjectionBytes {
			return fmt.Errorf("%w: invalid projection file bounds", ErrInvalidGraphIndexRequest)
		}
		totalBytes += file.Bytes
	}
	if totalBytes != m.TotalBytes {
		return fmt.Errorf("%w: projection byte total does not match files", ErrInvalidGraphIndexRequest)
	}
	if len(m.ModelRoutes) == 0 {
		return fmt.Errorf("%w: at least one model route is required", ErrInvalidGraphIndexRequest)
	}
	for _, route := range m.ModelRoutes {
		if err := requireManifestStrings(route); err != nil {
			return err
		}
	}
	return nil
}

func (m GraphProjectionManifest) CanonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

type GraphArtifactFile struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Required          bool   `json:"required"`
	Bytes             int64  `json:"bytes"`
	Rows              int64  `json:"rows"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	ContentHash       string `json:"content_hash"`
}

type GraphModelUsage struct {
	Requests            int64 `json:"requests"`
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	EstimatedCostMicros int64 `json:"estimated_cost_micros"`
	CacheHits           int64 `json:"cache_hits"`
	Retries             int64 `json:"retries"`
}

type GraphArtifactAttestation struct {
	ProducerIdentity string `json:"producer_identity"`
	BuildDigest      string `json:"build_digest"`
	Signature        string `json:"signature"`
}

type GraphArtifactStatus string

const (
	GraphArtifactCompleted GraphArtifactStatus = "completed"
	GraphArtifactCancelled GraphArtifactStatus = "cancelled"
	GraphArtifactFailed    GraphArtifactStatus = "failed"
)

type GraphArtifactManifest struct {
	ContractVersion          string                   `json:"contract_version"`
	ArtifactSchemaVersion    string                   `json:"artifact_schema_version"`
	Scope                    core.GraphScope          `json:"scope"`
	ConfigurationID          string                   `json:"configuration_id"`
	JobID                    string                   `json:"job_id"`
	RevisionID               string                   `json:"revision_id"`
	AdapterName              string                   `json:"adapter_name"`
	AdapterVersion           string                   `json:"adapter_version"`
	GraphRAGVersion          string                   `json:"graphrag_version"`
	PythonVersion            string                   `json:"python_version"`
	EnvironmentFingerprint   string                   `json:"environment_fingerprint"`
	InputManifestHash        string                   `json:"input_manifest_hash"`
	ConfigurationFingerprint string                   `json:"configuration_fingerprint"`
	PromptFingerprint        string                   `json:"prompt_fingerprint"`
	IndexMethod              core.GraphIndexMethod    `json:"index_method"`
	Mode                     GraphIndexMode           `json:"mode"`
	Outputs                  []GraphArtifactFile      `json:"outputs"`
	Models                   []string                 `json:"models"`
	Usage                    GraphModelUsage          `json:"usage"`
	DurationMillis           int64                    `json:"duration_millis"`
	Status                   GraphArtifactStatus      `json:"status"`
	FailureCode              string                   `json:"failure_code,omitempty"`
	CompletedAt              time.Time                `json:"completed_at"`
	Attestation              GraphArtifactAttestation `json:"attestation"`
}

func (m GraphArtifactManifest) Validate() error {
	if m.ContractVersion != GraphAdapterContractV1 || m.ArtifactSchemaVersion != GraphArtifactSchemaV1 {
		return fmt.Errorf("%w: unsupported adapter or artifact contract", ErrInvalidGraphIndexRequest)
	}
	if err := m.Scope.Validate(); err != nil {
		return err
	}
	if err := requireManifestStrings(
		m.ConfigurationID, m.JobID, m.RevisionID, m.AdapterName, m.AdapterVersion,
		m.GraphRAGVersion, m.PythonVersion, m.EnvironmentFingerprint,
		m.InputManifestHash, m.ConfigurationFingerprint, m.PromptFingerprint,
		m.Attestation.ProducerIdentity, m.Attestation.BuildDigest, m.Attestation.Signature,
	); err != nil {
		return err
	}
	if m.IndexMethod != core.GraphIndexStandard && m.IndexMethod != core.GraphIndexFast {
		return fmt.Errorf("%w: unsupported graph index method", ErrInvalidGraphIndexRequest)
	}
	if m.Mode != GraphIndexModeFull && m.Mode != GraphIndexModeIncremental {
		return fmt.Errorf("%w: unsupported graph index mode", ErrInvalidGraphIndexRequest)
	}
	if m.Status != GraphArtifactCompleted && m.Status != GraphArtifactCancelled && m.Status != GraphArtifactFailed {
		return fmt.Errorf("%w: unsupported artifact status", ErrInvalidGraphIndexRequest)
	}
	if m.DurationMillis < 0 || !validUsage(m.Usage) {
		return fmt.Errorf("%w: invalid duration or model usage", ErrInvalidGraphIndexRequest)
	}
	if len(m.Outputs) > MaxGraphArtifactFiles {
		return fmt.Errorf("%w: artifact file count exceeds bounds", ErrInvalidGraphIndexRequest)
	}
	if m.Status == GraphArtifactCompleted && (len(m.Outputs) == 0 || m.CompletedAt.IsZero()) {
		return fmt.Errorf("%w: completed artifact requires outputs and completion time", ErrInvalidGraphIndexRequest)
	}
	if m.Status != GraphArtifactCompleted && strings.TrimSpace(m.FailureCode) == "" {
		return fmt.Errorf("%w: incomplete artifact requires a bounded failure code", ErrInvalidGraphIndexRequest)
	}
	seen := make(map[string]struct{}, len(m.Outputs))
	for _, output := range m.Outputs {
		if err := requireManifestStrings(output.Name, output.Kind, output.SchemaFingerprint, output.ContentHash); err != nil {
			return err
		}
		if _, exists := seen[output.Name]; exists {
			return fmt.Errorf("%w: duplicate artifact file %q", ErrInvalidGraphIndexRequest, output.Name)
		}
		seen[output.Name] = struct{}{}
		if output.Bytes < 0 || output.Bytes > MaxGraphArtifactFileBytes || output.Rows < 0 || output.Rows > MaxGraphArtifactRows {
			return fmt.Errorf("%w: artifact file exceeds bounds", ErrInvalidGraphIndexRequest)
		}
	}
	if len(m.Models) == 0 {
		return fmt.Errorf("%w: artifact model identity is required", ErrInvalidGraphIndexRequest)
	}
	for _, model := range m.Models {
		if err := requireManifestStrings(model); err != nil {
			return err
		}
	}
	return nil
}

func (m GraphArtifactManifest) CanonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func requireManifestStrings(values ...string) error {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return fmt.Errorf("%w: required manifest string is empty", ErrInvalidGraphIndexRequest)
		}
		if len(trimmed) > MaxGraphManifestStringBytes {
			return fmt.Errorf("%w: manifest string exceeds bounds", ErrInvalidGraphIndexRequest)
		}
	}
	return nil
}

func validUsage(usage GraphModelUsage) bool {
	return usage.Requests >= 0 && usage.InputTokens >= 0 && usage.OutputTokens >= 0 &&
		usage.EstimatedCostMicros >= 0 && usage.CacheHits >= 0 && usage.Retries >= 0
}
