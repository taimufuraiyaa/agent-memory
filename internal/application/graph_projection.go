package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

type GraphProjectionKind string

const (
	GraphProjectionSourceText      GraphProjectionKind = "source_text"
	GraphProjectionMemory          GraphProjectionKind = "memory"
	GraphProjectionAgentMemory     GraphProjectionKind = "agent_memory"
	GraphProjectionSolutionSummary GraphProjectionKind = "solution_summary"
	GraphProjectionApprovedDerived GraphProjectionKind = "approved_derived"
)

type GraphProjectionRecord struct {
	ID          string
	Kind        GraphProjectionKind
	Content     string
	Fingerprint string
	EventTime   time.Time

	SourceID   string
	EditionID  string
	AssetID    string
	PassageID  string
	CitationID string
	EpisodeID  string

	Secret           bool
	RawReasoning     bool
	Quarantined      bool
	Deleted          bool
	Expired          bool
	Authorized       bool
	SafetySuppressed bool
	Exportable       bool
}

type GraphProjectionRequest struct {
	Scope                   core.GraphScope
	ConfigurationID         string
	JobID                   string
	RevisionID              string
	Mode                    contracts.GraphIndexMode
	BaseRevisionID          string
	ProjectionPolicyVersion string
	Cutoff                  core.GraphWatermark
	PromptFingerprint       string
	ModelRoutes             []string
	CreatedAt               time.Time
	ExpiresAt               time.Time
	ProducerIdentity        string
	Records                 []GraphProjectionRecord
}

type GraphCorrelationReference struct {
	CanonicalKind        GraphProjectionKind `json:"canonical_kind"`
	CanonicalID          string              `json:"canonical_id"`
	CanonicalFingerprint string              `json:"canonical_fingerprint"`
	SourceID             string              `json:"source_id,omitempty"`
	EditionID            string              `json:"edition_id,omitempty"`
	AssetID              string              `json:"asset_id,omitempty"`
	PassageID            string              `json:"passage_id,omitempty"`
	CitationID           string              `json:"citation_id,omitempty"`
	EpisodeID            string              `json:"episode_id,omitempty"`
}

type GraphProjection struct {
	Manifest       contracts.GraphProjectionManifest
	DocumentsJSONL []byte
	TextUnitsJSONL []byte
	Correlations   map[string]GraphCorrelationReference
}

type GraphProjectionBuilder struct{}

func NewGraphProjectionBuilder() *GraphProjectionBuilder { return &GraphProjectionBuilder{} }

func (b *GraphProjectionBuilder) Build(request GraphProjectionRequest) (GraphProjection, error) {
	if err := validateGraphProjectionRequest(request); err != nil {
		return GraphProjection{}, err
	}
	records := append([]GraphProjectionRecord(nil), request.Records...)
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		if records[i].ID != records[j].ID {
			return records[i].ID < records[j].ID
		}
		return records[i].Fingerprint < records[j].Fingerprint
	})

	var documents, textUnits bytes.Buffer
	correlations := make(map[string]GraphCorrelationReference)
	eligible := make([]GraphProjectionRecord, 0, len(records))
	for _, record := range records {
		if !recordEligibleForGraphProjection(record) {
			continue
		}
		if err := validation.ValidateGraphProjectionRecord(request.Scope, validation.GraphProjectionRecordValidation{
			Scope: request.Scope, ID: record.ID, Kind: string(record.Kind),
			Fingerprint: record.Fingerprint, ContentBytes: len([]byte(record.Content)),
		}); err != nil {
			return GraphProjection{}, err
		}
		token := graphCorrelationToken(request, record)
		reference := GraphCorrelationReference{
			CanonicalKind: record.Kind, CanonicalID: record.ID, CanonicalFingerprint: record.Fingerprint,
			SourceID: record.SourceID, EditionID: record.EditionID, AssetID: record.AssetID,
			PassageID: record.PassageID, CitationID: record.CitationID, EpisodeID: record.EpisodeID,
		}
		correlations[token] = reference
		payload := struct {
			ID          string              `json:"id"`
			Text        string              `json:"text"`
			Kind        GraphProjectionKind `json:"kind"`
			Fingerprint string              `json:"fingerprint"`
			Token       string              `json:"correlation_token"`
			SourceID    string              `json:"source_id,omitempty"`
			EditionID   string              `json:"edition_id,omitempty"`
			AssetID     string              `json:"asset_id,omitempty"`
			PassageID   string              `json:"passage_id,omitempty"`
			EventTime   time.Time           `json:"event_time"`
		}{token, strings.TrimSpace(record.Content), record.Kind, record.Fingerprint, token,
			record.SourceID, record.EditionID, record.AssetID, record.PassageID, record.EventTime.UTC()}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return GraphProjection{}, err
		}
		documents.Write(encoded)
		documents.WriteByte('\n')
		textUnits.Write(encoded)
		textUnits.WriteByte('\n')
		eligible = append(eligible, record)
	}
	if len(eligible) == 0 {
		return GraphProjection{}, fmt.Errorf("graph projection contains no eligible records")
	}

	documentBytes, textUnitBytes := documents.Bytes(), textUnits.Bytes()
	correlationHash, err := hashGraphCorrelations(correlations)
	if err != nil {
		return GraphProjection{}, err
	}
	eventStart, eventEnd := eligible[0].EventTime, eligible[0].EventTime
	for _, record := range eligible[1:] {
		if record.EventTime.Before(eventStart) {
			eventStart = record.EventTime
		}
		if record.EventTime.After(eventEnd) {
			eventEnd = record.EventTime
		}
	}
	manifest := contracts.GraphProjectionManifest{
		ContractVersion: contracts.GraphProjectionContractV1, ProjectionPolicyVersion: request.ProjectionPolicyVersion,
		Scope: request.Scope, ConfigurationID: request.ConfigurationID, JobID: request.JobID,
		RevisionID: request.RevisionID, Mode: request.Mode, BaseRevisionID: request.BaseRevisionID,
		Cutoff: request.Cutoff, EventTimeStart: eventStart.UTC(), EventTimeEnd: eventEnd.UTC(),
		Files: []contracts.GraphProjectionFile{
			{Name: "documents.jsonl", Kind: "documents", Records: int64(len(eligible)), Bytes: int64(len(documentBytes)), ContentHash: graphContentHash(documentBytes)},
			{Name: "text_units.jsonl", Kind: "text_units", Records: int64(len(eligible)), Bytes: int64(len(textUnitBytes)), ContentHash: graphContentHash(textUnitBytes)},
		},
		DocumentCount: int64(len(eligible)), TextUnitCount: int64(len(eligible)),
		TotalBytes: int64(len(documentBytes) + len(textUnitBytes)), CorrelationMapHash: correlationHash,
		CorrelationMapLocation: "correlations.enc", ModelRoutes: append([]string(nil), request.ModelRoutes...),
		PromptFingerprint: request.PromptFingerprint, RetentionClass: "graph-projection-24h",
		Sensitivity: "workspace-private", CreatedAt: request.CreatedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC(),
		ProducerIdentity: request.ProducerIdentity,
	}
	if err := manifest.Validate(); err != nil {
		return GraphProjection{}, err
	}
	return GraphProjection{Manifest: manifest, DocumentsJSONL: documentBytes, TextUnitsJSONL: textUnitBytes, Correlations: correlations}, nil
}

func recordEligibleForGraphProjection(record GraphProjectionRecord) bool {
	return record.Authorized && record.Exportable && !record.Secret && !record.RawReasoning && !record.Quarantined &&
		!record.Deleted && !record.Expired && !record.SafetySuppressed
}

func validateGraphProjectionRequest(request GraphProjectionRequest) error {
	if err := request.Scope.Validate(); err != nil {
		return err
	}
	for _, value := range []string{request.ConfigurationID, request.JobID, request.RevisionID,
		request.ProjectionPolicyVersion, request.Cutoff.Digest, request.PromptFingerprint, request.ProducerIdentity} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("graph projection request identity is required")
		}
	}
	if request.Mode != contracts.GraphIndexModeFull && request.Mode != contracts.GraphIndexModeIncremental {
		return fmt.Errorf("unsupported graph projection mode")
	}
	if request.Mode == contracts.GraphIndexModeIncremental && strings.TrimSpace(request.BaseRevisionID) == "" {
		return fmt.Errorf("incremental graph projection requires base revision")
	}
	if len(request.ModelRoutes) == 0 || !request.ExpiresAt.After(request.CreatedAt) {
		return fmt.Errorf("graph projection model route and expiry are required")
	}
	return nil
}

func graphCorrelationToken(request GraphProjectionRequest, record GraphProjectionRecord) string {
	hash := sha256.New()
	for _, value := range []string{request.Scope.TenantID, request.Scope.WorkspaceID, request.ProjectionPolicyVersion,
		string(record.Kind), record.ID, record.Fingerprint} {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "corr_" + hex.EncodeToString(hash.Sum(nil))
}

func graphContentHash(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func hashGraphCorrelations(correlations map[string]GraphCorrelationReference) (string, error) {
	keys := make([]string, 0, len(correlations))
	for key := range correlations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		encoded, err := json.Marshal(struct {
			Token     string                    `json:"token"`
			Reference GraphCorrelationReference `json:"reference"`
		}{key, correlations[key]})
		if err != nil {
			return "", err
		}
		hash.Write(encoded)
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
