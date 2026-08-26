package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphChangeExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// appendGraphChangesForMemoryTx records only durable scheduling intent. It
// never invokes the coordinator or adapter on the canonical write path.
func appendGraphChangesForMemoryTx(ctx context.Context, tx graphChangeExecer, memory *core.MemoryEntry, kind string, at time.Time) error {
	if memory == nil {
		return fmt.Errorf("memory is required for graph change")
	}
	fingerprint, err := graphMemoryFingerprint(memory, kind)
	if err != nil {
		return err
	}
	return appendGraphChangesTx(ctx, tx, memory.Workspace, "memory", memory.ID, fingerprint, kind, at)
}

func appendGraphChangesTx(ctx context.Context, tx graphChangeExecer, workspace, subjectKind, subjectID, fingerprint, kind string, at time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,version,projection_version FROM graph_configurations WHERE workspace=? AND enabled=1 ORDER BY version,id`, workspace)
	if err != nil {
		return err
	}
	defer rows.Close()
	type configuration struct{ id, version, projection string }
	var configurations []configuration
	for rows.Next() {
		var id, projection string
		var version int64
		if err := rows.Scan(&id, &version, &projection); err != nil {
			return err
		}
		configurations = append(configurations, configuration{id: id, version: fmt.Sprintf("%s@%d", id, version), projection: projection})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, configuration := range configurations {
		identity := strings.Join([]string{workspace, subjectKind, subjectID, fingerprint, configuration.projection, configuration.version}, "\x00")
		digest := sha256.Sum256([]byte(identity))
		_, err := tx.ExecContext(ctx, `INSERT INTO graph_change_journal (
			id,workspace,subject_kind,subject_id,subject_fingerprint,projection_version,
			configuration_version,change_kind,occurred_at,processed_revision_id
		) VALUES (?,?,?,?,?,?,?,?,?, '')
		ON CONFLICT(workspace,subject_kind,subject_id,subject_fingerprint,projection_version,configuration_version) DO NOTHING`,
			"change-"+hex.EncodeToString(digest[:]), workspace, subjectKind, subjectID, fingerprint,
			configuration.projection, configuration.version, kind, formatGraphTime(at))
		if err != nil {
			return err
		}
	}
	return nil
}

func graphMemoryFingerprint(memory *core.MemoryEntry, kind string) (string, error) {
	value := struct {
		Kind         string            `json:"kind"`
		ID           string            `json:"id"`
		Type         core.MemoryType   `json:"type"`
		Content      string            `json:"content"`
		Workspace    string            `json:"workspace"`
		Source       core.MemorySource `json:"source"`
		Entities     []string          `json:"entities"`
		Tags         []string          `json:"tags"`
		SupersededBy *string           `json:"superseded_by"`
	}{kind, memory.ID, memory.Type, memory.Content, memory.Workspace, memory.Source, memory.Entities, memory.Tags, memory.SupersededBy}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
