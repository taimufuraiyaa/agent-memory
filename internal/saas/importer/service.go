// Package importer validates and resumes encrypted portable bundle publication.
package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
)

const MaxBundleBytes = 250 << 20

type ItemResult struct {
	Type       string `json:"type"`
	ExternalID string `json:"external_id"`
	ResultID   string `json:"result_id,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}
type Report struct {
	Imported []ItemResult `json:"imported"`
	Merged   []ItemResult `json:"merged"`
	Skipped  []ItemResult `json:"skipped"`
	Failed   []ItemResult `json:"failed"`
}
type Result struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Duplicate bool   `json:"duplicate"`
	Report    Report `json:"report"`
}
type Service struct {
	pool         *pgxpool.Pool
	memories     *memory.Service
	notes        *memory.WorkflowService
	sources      *sourceservice.Service
	attestations *attestation.Service
	entitlements *billing.Repository
	now          func() time.Time
}

func NewService(pool *pgxpool.Pool, memories *memory.Service, notes *memory.WorkflowService, sources *sourceservice.Service, attestations *attestation.Service, entitlements *billing.Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, memories: memories, notes: notes, sources: sources, attestations: attestations, entitlements: entitlements, now: now}
}

func (s *Service) Import(ctx context.Context, workspaceID, idempotencyKey, passphrase string, encrypted []byte) (Result, error) {
	request, ok := auth.FromContext(ctx)
	if !s.configured() || !ok || !request.Can("memory:write") || !request.Can("source:write") || len(encrypted) == 0 || len(encrypted) > MaxBundleBytes || len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return Result{}, errors.New("portable import is invalid")
	}
	plain, err := exportservice.DecryptPortable(passphrase, encrypted)
	if err != nil {
		return Result{}, err
	}
	var bundle exportservice.Bundle
	if err = json.Unmarshal(plain, &bundle); err != nil {
		return Result{}, errors.New("portable bundle schema is invalid")
	}
	if bundle.Format != "agent-memory-portable" || bundle.Version != "2.0" || bundle.MinReaderVersion > "2.0" || bundle.SourceBytesIncluded != (len(bundle.SourceObjects) > 0) {
		return Result{}, errors.New("unsupported portable bundle schema")
	}
	if err = bundle.VerifyManifest(); err != nil {
		return Result{}, err
	}
	if err = validateBundle(bundle); err != nil {
		return Result{}, err
	}
	if len(bundle.SourceObjects) > 0 {
		if _, err = s.attestations.RequireActive(ctx, request.AccountID); err != nil {
			return Result{}, err
		}
	}
	if err = s.validateTarget(ctx, request, workspaceID, bundle); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(encrypted)
	bundleHash := hex.EncodeToString(sum[:])
	unlock, err := s.lockBundle(ctx, request.TenantID, bundleHash)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	operation, duplicate, err := s.start(ctx, request, workspaceID, idempotencyKey, bundleHash, bundle)
	if err != nil {
		return Result{}, err
	}
	operation.Duplicate = duplicate
	if operation.State == "completed" {
		return operation, nil
	}
	for _, value := range bundle.Memories {
		if err := ctx.Err(); err != nil {
			return operation, err
		}
		external := stringValue(value, "id")
		if done, itemErr := s.itemTerminal(ctx, request.TenantID, operation.ID, "memory", external); itemErr != nil {
			return operation, itemErr
		} else if done {
			continue
		}
		content := stringValue(value, "content")
		kind := core.MemoryType(stringValue(value, "type"))
		entry, merged, itemErr := s.memories.Write(ctx, memory.Command{WorkspaceID: workspaceID, Type: kind, Content: content, Source: core.MemorySource{Type: core.SourceImport}, IdempotencyKey: itemKey(bundleHash, "memory", external)})
		state, reason, resultID := "imported", "", entry.ID
		if merged {
			state = "merged"
		}
		if itemErr != nil {
			state = "failed"
			reason = "memory_invalid"
		}
		if err := s.recordItem(ctx, request.TenantID, operation.ID, "memory", external, state, resultID, reason); err != nil {
			return operation, err
		}
	}
	for _, value := range bundle.Notes {
		if err := ctx.Err(); err != nil {
			return operation, err
		}
		external := stringValue(value, "id")
		if done, itemErr := s.itemTerminal(ctx, request.TenantID, operation.ID, "note", external); itemErr != nil {
			return operation, itemErr
		} else if done {
			continue
		}
		properties := mapValue(value, "properties")
		note, merged, itemErr := s.notes.CreateNote(ctx, memory.NoteCreate{Input: core.CreateNoteInput{Workspace: workspaceID, Path: stringValue(value, "path"), Title: stringValue(value, "title"), Body: stringValue(value, "body"), Properties: properties, AuthorKind: "import"}, IdempotencyKey: itemKey(bundleHash, "note", external)})
		state, reason, resultID := "imported", "", ""
		if note != nil {
			resultID = note.ID
		}
		if merged {
			state = "merged"
		}
		if itemErr != nil {
			state = "failed"
			reason = "note_invalid"
		}
		if err := s.recordItem(ctx, request.TenantID, operation.ID, "note", external, state, resultID, reason); err != nil {
			return operation, err
		}
	}
	for _, object := range bundle.SourceObjects {
		if err := ctx.Err(); err != nil {
			return operation, err
		}
		if done, itemErr := s.itemTerminal(ctx, request.TenantID, operation.ID, "source", object.SourceID); itemErr != nil {
			return operation, itemErr
		} else if done {
			continue
		}
		state, resultID, reason := s.importSource(ctx, workspaceID, bundle, object)
		if err := s.recordItem(ctx, request.TenantID, operation.ID, "source", object.SourceID, state, resultID, reason); err != nil {
			return operation, err
		}
	}
	for _, source := range bundle.Sources {
		external := stringValue(source, "id")
		if !containsSource(bundle.SourceObjects, external) {
			if done, itemErr := s.itemTerminal(ctx, request.TenantID, operation.ID, "source", external); itemErr != nil {
				return operation, itemErr
			} else if done {
				continue
			}
			if err := s.recordItem(ctx, request.TenantID, operation.ID, "source", external, "skipped", "", "source_bytes_not_selected"); err != nil {
				return operation, err
			}
		}
	}
	return s.finish(ctx, request, operation.ID)
}

func (s *Service) Status(ctx context.Context, id string) (Result, error) {
	request, ok := auth.FromContext(ctx)
	if !s.configured() || !ok || !request.Can("memory:write") || strings.TrimSpace(id) == "" {
		return Result{}, auth.ErrTenantUnavailable
	}
	tx, err := s.begin(ctx, request.TenantID)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result Result
	var reportJSON []byte
	if err = tx.QueryRow(ctx, `SELECT id::text,state,report FROM saas_import_operations WHERE tenant_id=$1 AND id=$2`, request.TenantID, id).Scan(&result.ID, &result.State, &reportJSON); err != nil {
		return Result{}, auth.ErrTenantUnavailable
	}
	if err = json.Unmarshal(reportJSON, &result.Report); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) validateTarget(ctx context.Context, request auth.RequestContext, workspaceID string, bundle exportservice.Bundle) error {
	tx, err := s.begin(ctx, request.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workspace bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active')`, request.TenantID, workspaceID).Scan(&workspace); err != nil || !workspace {
		return errors.New("import workspace unavailable")
	}
	limits, err := s.entitlements.Entitlements(ctx, request.TenantID)
	if err != nil {
		return err
	}
	var sources int
	var storage int64
	if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE((SELECT sum(expected_size) FROM saas_upload_grants WHERE tenant_id=$1 AND state NOT IN('failed','expired')),0) FROM saas_sources WHERE tenant_id=$1 AND state NOT IN('deleted','deleting')`, request.TenantID).Scan(&sources, &storage); err != nil {
		return err
	}
	incoming := int64(0)
	for _, object := range bundle.SourceObjects {
		incoming += object.SizeBytes
	}
	if sources+len(bundle.SourceObjects) > limits.MaxSourceCount || incoming+storage > limits.MaxStorageBytes {
		return errors.New("portable import exceeds tenant quota")
	}
	return nil
}

func (s *Service) start(ctx context.Context, request auth.RequestContext, workspace, key, hash string, bundle exportservice.Bundle) (Result, bool, error) {
	tx, err := s.begin(ctx, request.TenantID)
	if err != nil {
		return Result{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result Result
	var reportJSON []byte
	err = tx.QueryRow(ctx, `SELECT id::text,state,report FROM saas_import_operations WHERE tenant_id=$1 AND (idempotency_key=$2 OR bundle_sha256=$3) LIMIT 1`, request.TenantID, key, hash).Scan(&result.ID, &result.State, &reportJSON)
	if err == nil {
		_ = json.Unmarshal(reportJSON, &result.Report)
		return result, true, nil
	}
	result = Result{ID: uuid.NewString(), State: "running", Report: emptyReport()}
	_, err = tx.Exec(ctx, `INSERT INTO saas_import_operations(tenant_id,id,account_id,workspace_id,bundle_sha256,idempotency_key,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'running',$7,$7)`, request.TenantID, result.ID, request.AccountID, workspace, hash, key, s.now().UTC())
	if err == nil {
		requestID, trace := audit.NewRequestIDs()
		err = audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: s.now().UTC(), ActorType: "member", ActorID: request.AccountID, Service: "memory", Operation: "import.start", Outcome: "success", RequestID: requestID, TraceID: trace, TargetType: "import", TargetID: result.ID, PolicyVersion: "portable-v2", ReasonCode: "validated", SafeMetadata: map[string]any{"memory_count": len(bundle.Memories), "note_count": len(bundle.Notes), "source_count": len(bundle.SourceObjects)}})
	}
	if err != nil {
		return Result{}, false, err
	}
	return result, false, tx.Commit(ctx)
}

func (s *Service) importSource(ctx context.Context, workspace string, bundle exportservice.Bundle, object exportservice.SourceObject) (string, string, string) {
	body, err := base64.StdEncoding.DecodeString(object.BytesBase64)
	if err != nil {
		return "failed", "", "source_bytes_invalid"
	}
	rights := "lawfully_acquired_private_use"
	for _, source := range bundle.Sources {
		if stringValue(source, "id") == object.SourceID && stringValue(source, "rights_basis") != "" {
			rights = stringValue(source, "rights_basis")
		}
	}
	grant, err := s.sources.Issue(ctx, sourceservice.GrantRequest{WorkspaceID: workspace, Filename: object.Filename, MediaType: object.MediaType, SizeBytes: object.SizeBytes, ChecksumSHA256: object.ChecksumSHA256, RightsBasis: rights})
	if err != nil {
		return "failed", "", "source_grant_failed"
	}
	parsed, _ := url.Parse(grant.UploadPath)
	token := parsed.Query().Get("token")
	if err = s.sources.Upload(ctx, grant.ID, token, object.MediaType, object.SizeBytes, bytes.NewReader(body)); err != nil {
		return "failed", grant.SourceID, "source_upload_failed"
	}
	return "imported", grant.SourceID, ""
}

func validateBundle(bundle exportservice.Bundle) error {
	seen := make(map[string]struct{})
	for _, value := range bundle.Memories {
		id := stringValue(value, "id")
		content := stringValue(value, "content")
		kind := core.MemoryType(stringValue(value, "type"))
		if id == "" || content == "" || len(content) > 2000 || !validMemoryType(kind) || duplicateItem(seen, "memory", id) {
			return errors.New("portable bundle memory schema is invalid")
		}
	}
	for _, value := range bundle.Notes {
		id := stringValue(value, "id")
		path := stringValue(value, "path")
		body, bodyOK := value["body"].(string)
		if id == "" || path == "" || !bodyOK || len(body) > 1<<20 || duplicateItem(seen, "note", id) {
			return errors.New("portable bundle note schema is invalid")
		}
		if raw, exists := value["properties"]; exists {
			if _, ok := raw.(map[string]any); !ok {
				return errors.New("portable bundle note properties are invalid")
			}
		}
	}
	sourceRecords := make(map[string]struct{}, len(bundle.Sources))
	for _, value := range bundle.Sources {
		id := stringValue(value, "id")
		rights := stringValue(value, "rights_basis")
		if id == "" || duplicateItem(seen, "source-record", id) || (rights != "" && !validRightsBasis(rights)) {
			return errors.New("portable bundle source schema is invalid")
		}
		sourceRecords[id] = struct{}{}
	}
	for _, object := range bundle.SourceObjects {
		if object.SourceID == "" || strings.TrimSpace(object.Filename) == "" || object.SizeBytes < 1 || object.SizeBytes > MaxBundleBytes || duplicateItem(seen, "source-object", object.SourceID) {
			return errors.New("portable bundle source object schema is invalid")
		}
		if _, ok := sourceRecords[object.SourceID]; !ok {
			return errors.New("portable bundle source object has no source record")
		}
		switch object.MediaType {
		case "application/pdf", "application/epub+zip", "text/markdown", "text/plain":
		default:
			return errors.New("portable bundle source media type is invalid")
		}
		body, err := base64.StdEncoding.DecodeString(object.BytesBase64)
		if err != nil || int64(len(body)) != object.SizeBytes {
			return errors.New("portable bundle source bytes are invalid")
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != strings.ToLower(strings.TrimSpace(object.ChecksumSHA256)) {
			return errors.New("portable bundle source checksum mismatch")
		}
	}
	return nil
}

func validMemoryType(kind core.MemoryType) bool {
	switch kind {
	case core.EpisodicMemory, core.SemanticMemory, core.ProceduralMemory, core.OutcomeMemory:
		return true
	default:
		return false
	}
}

func validRightsBasis(value string) bool {
	switch value {
	case "author_owned", "licensed", "public_domain_or_open", "lawfully_acquired_private_use":
		return true
	default:
		return false
	}
}

func duplicateItem(seen map[string]struct{}, kind, id string) bool {
	key := kind + "|" + id
	if _, exists := seen[key]; exists {
		return true
	}
	seen[key] = struct{}{}
	return false
}

func (s *Service) itemTerminal(ctx context.Context, tenant, operation, itemType, external string) (bool, error) {
	tx, err := s.begin(ctx, tenant)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM saas_import_items WHERE tenant_id=$1 AND operation_id=$2 AND item_type=$3 AND external_id=$4`, tenant, operation, itemType, external).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == "imported" || state == "merged" || state == "skipped" || state == "failed", nil
}

func (s *Service) recordItem(ctx context.Context, tenant, operation, itemType, external, state, result, reason string) error {
	if external == "" {
		external = uuid.NewString()
	}
	tx, err := s.begin(ctx, tenant)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO saas_import_items(tenant_id,operation_id,item_type,external_id,state,result_id,reason_code,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,operation_id,item_type,external_id) DO UPDATE SET state=EXCLUDED.state,result_id=EXCLUDED.result_id,reason_code=EXCLUDED.reason_code,updated_at=EXCLUDED.updated_at`, tenant, operation, itemType, external, state, result, reason, s.now().UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) finish(ctx context.Context, request auth.RequestContext, id string) (Result, error) {
	tx, err := s.begin(ctx, request.TenantID)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	report := emptyReport()
	rows, err := tx.Query(ctx, `SELECT item_type,external_id,result_id,state,reason_code FROM saas_import_items WHERE tenant_id=$1 AND operation_id=$2 ORDER BY item_type,external_id`, request.TenantID, id)
	if err != nil {
		return Result{}, err
	}
	for rows.Next() {
		var item ItemResult
		var state string
		if err := rows.Scan(&item.Type, &item.ExternalID, &item.ResultID, &state, &item.ReasonCode); err != nil {
			return Result{}, err
		}
		switch state {
		case "imported":
			report.Imported = append(report.Imported, item)
		case "merged":
			report.Merged = append(report.Merged, item)
		case "skipped":
			report.Skipped = append(report.Skipped, item)
		default:
			report.Failed = append(report.Failed, item)
		}
	}
	rows.Close()
	encoded, _ := json.Marshal(report)
	state := "completed"
	safeCode := ""
	if len(report.Failed) > 0 {
		safeCode = "items_failed"
	}
	_, err = tx.Exec(ctx, `UPDATE saas_import_operations SET state=$3,report=$4,safe_error_code=$5,updated_at=$6,completed_at=$6 WHERE tenant_id=$1 AND id=$2`, request.TenantID, id, state, encoded, safeCode, s.now().UTC())
	if err != nil {
		return Result{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{ID: id, State: state, Report: report}, nil
}
func emptyReport() Report {
	return Report{Imported: []ItemResult{}, Merged: []ItemResult{}, Skipped: []ItemResult{}, Failed: []ItemResult{}}
}
func itemKey(hash, kind, id string) string {
	sum := sha256.Sum256([]byte(hash + "|" + kind + "|" + id))
	return "import-" + hex.EncodeToString(sum[:])[:32]
}
func stringValue(value map[string]any, key string) string {
	raw := value[key]
	if text, ok := raw.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
func mapValue(value map[string]any, key string) map[string]any {
	if result, ok := value[key].(map[string]any); ok {
		return result
	}
	return map[string]any{}
}
func containsSource(values []exportservice.SourceObject, id string) bool {
	for _, value := range values {
		if value.SourceID == id {
			return true
		}
	}
	return false
}
func (s *Service) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if !s.configured() {
		return nil, fmt.Errorf("importer is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func (s *Service) lockBundle(ctx context.Context, tenant, hash string) (func(), error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := tenant + "|" + hash
	if _, err = connection.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		connection.Release()
		return nil, err
	}
	return func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := connection.Exec(unlockContext, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); unlockErr != nil {
			_ = connection.Hijack().Close(unlockContext)
			return
		}
		connection.Release()
	}, nil
}

func (s *Service) configured() bool {
	return s != nil && s.pool != nil && s.memories != nil && s.notes != nil && s.sources != nil && s.attestations != nil && s.entitlements != nil
}
