package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// tombstoneCooldownDuration is how long a removed memory stays in cooldown
// before the gap detector may consider it again.
const tombstoneCooldownDuration = 7 * 24 * time.Hour

// AddTombstone stores a compact breadcrumb for a removed memory.
func (s *Store) AddTombstone(ctx context.Context, m core.MemoryEntry, reason, lineageID string) error {
	entityHash := tombstoneEntityHash(m)
	summary := summarizeFragment(m.Content, 24)
	evictedAt := time.Now().UTC()
	cooldown := evictedAt.Add(tombstoneCooldownDuration)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO memory_tombstones (id, memory_id, workspace, type, entity_hash, fragment_summary, eviction_reason, lineage_memory_id, evicted_at, cooldown_until)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(),
		m.ID,
		m.Workspace,
		string(m.Type),
		entityHash,
		summary,
		reason,
		lineageID,
		evictedAt.Format(time.RFC3339Nano),
		cooldown.Format(time.RFC3339Nano),
	)
	return err
}

// ListTombstones returns tombstones for one workspace (optional entity hash filter).
func (s *Store) ListTombstones(ctx context.Context, workspace, entity string) ([]core.MemoryTombstone, error) {
	entity = strings.TrimSpace(strings.ToLower(entity))
	query := `SELECT id, memory_id, workspace, type, entity_hash, fragment_summary, eviction_reason, lineage_memory_id, evicted_at, cooldown_until
FROM memory_tombstones WHERE workspace = ?`
	args := []any{workspace}
	if entity != "" {
		query += ` AND entity_hash = ?`
		args = append(args, tombstoneHash(entity))
	}
	query += ` ORDER BY evicted_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]core.MemoryTombstone, 0)
	for rows.Next() {
		var t core.MemoryTombstone
		var mt string
		var evictedAt, cooldown string
		if err := rows.Scan(
			&t.ID, &t.MemoryID, &t.Workspace, &mt, &t.EntityHash, &t.FragmentSummary, &t.EvictionReason, &t.LineageMemoryID, &evictedAt, &cooldown,
		); err != nil {
			return nil, err
		}
		t.Type = core.MemoryType(mt)
		t.EvictedAt, _ = time.Parse(time.RFC3339Nano, evictedAt)
		t.CooldownUntil, _ = time.Parse(time.RFC3339Nano, cooldown)
		// Legacy rows store cooldown_until equal to evicted_at (old bug) or an
		// empty value. Treat those as already out of cooldown (cooldown == evicted_at).
		if t.CooldownUntil.IsZero() || t.CooldownUntil.Equal(t.EvictedAt) {
			t.CooldownUntil = t.EvictedAt
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func tombstoneEntityHash(m core.MemoryEntry) string {
	if len(m.Entities) > 0 {
		return tombstoneHash(strings.ToLower(strings.Join(m.Entities, "|")))
	}
	words := strings.Fields(strings.ToLower(m.Content))
	if len(words) > 8 {
		words = words[:8]
	}
	return tombstoneHash(strings.Join(words, " "))
}

func tombstoneHash(v string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(v))))
	return hex.EncodeToString(sum[:])
}

func summarizeFragment(content string, maxWords int) string {
	parts := strings.Fields(strings.TrimSpace(content))
	if len(parts) <= maxWords {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts[:maxWords], " ")
}

// SetTombstoneCooldownForWorkspace updates cooldown timestamp for testing/maintenance.
func (s *Store) SetTombstoneCooldownForWorkspace(ctx context.Context, workspace string, cooldownUntil time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_tombstones SET cooldown_until = ? WHERE workspace = ?`,
		cooldownUntil.Format(time.RFC3339Nano), workspace,
	)
	return err
}
