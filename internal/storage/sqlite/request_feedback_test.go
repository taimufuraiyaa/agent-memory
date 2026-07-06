package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestFeedback(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	store, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	ws := "test-ws"

	// 1. Verify LogRetrievalRequest logs a pending request
	reqID := "req-123"
	err = store.LogRetrievalRequest(ctx, reqID, ws, "search", "how to build a tree")
	require.NoError(t, err)

	// Verify it exists in db with score = -1
	var score int
	var reason string
	var usefulCountVal, totalCountVal int
	err = store.db.QueryRowContext(ctx, "SELECT score FROM retrieval_requests WHERE id = ?", reqID).Scan(&score)
	require.NoError(t, err)
	assert.Equal(t, -1, score)

	// 2. Verify RecordRequestFeedback updates score correctly
	err = store.RecordRequestFeedback(ctx, reqID, 4, "helpful results", 3, 8)
	require.NoError(t, err)

	err = store.db.QueryRowContext(ctx, "SELECT score, reason, useful_count, total_count FROM retrieval_requests WHERE id = ?", reqID).Scan(&score, &reason, &usefulCountVal, &totalCountVal)
	require.NoError(t, err)
	assert.Equal(t, 4, score)
	assert.Equal(t, "helpful results", reason)
	assert.Equal(t, 3, usefulCountVal)
	assert.Equal(t, 8, totalCountVal)

	// Verify error on invalid score
	err = store.RecordRequestFeedback(ctx, reqID, 10, "", -1, -1)
	assert.Error(t, err)

	// Verify error on non-existent request ID
	err = store.RecordRequestFeedback(ctx, "non-existent", 3, "some reason", -1, -1)
	assert.Error(t, err)

	// 3. Verify GetFeedbackStats aggregates scores correctly
	// Add more scored requests
	err = store.LogRetrievalRequest(ctx, "req-2", ws, "recall", "deploy stack")
	require.NoError(t, err)
	err = store.RecordRequestFeedback(ctx, "req-2", 2, "insufficient details", 1, 5)
	require.NoError(t, err)

	// Add an unscored request (score = -1) - should not affect the average
	err = store.LogRetrievalRequest(ctx, "req-3", ws, "search", "something else")
	require.NoError(t, err)

	stats, err := store.GetFeedbackStats(ctx, ws)
	require.NoError(t, err)
	assert.Equal(t, ws, stats.Workspace)
	assert.Equal(t, 2, stats.TotalFeedbackCount) // req-123 (4) and req-2 (2)
	assert.InDelta(t, 3.0, stats.AverageWeek, 0.001)
	assert.InDelta(t, 3.0, stats.AverageMonth, 0.001)
	assert.InDelta(t, 3.0, stats.AverageYear, 0.001)
	assert.InDelta(t, 2.0, stats.AverageUsefulCount, 0.001)
	assert.InDelta(t, 6.5, stats.AverageTotalCount, 0.001)
	assert.InDelta(t, 0.2875, stats.AverageUsefulRatio, 0.001)

	// Add a historical feedback (simulate old record)
	// SQLite created_at format is RFC3339
	oldTime := time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO retrieval_requests (id, workspace, request_type, query, score, created_at)
		VALUES ('req-old', ?, 'search', 'ancient history', 5, ?)`,
		ws, oldTime,
	)
	require.NoError(t, err)

	// Refresh stats
	stats, err = store.GetFeedbackStats(ctx, ws)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.TotalFeedbackCount) // 4, 2, and 5
	// Weekly average should only cover req-123 and req-2 => (4+2)/2 = 3.0
	assert.InDelta(t, 3.0, stats.AverageWeek, 0.001)
	// Monthly average covers all => (4+2+5)/3 = 3.666
	assert.InDelta(t, 3.666, stats.AverageMonth, 0.01)
}

func TestRequestFeedbackDeduplication(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_dedup.db")

	store, err := Open(context.Background(), dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	ws := "test-ws"
	q := "duplicate search query"

	// 1. Log first request
	err = store.LogRetrievalRequest(ctx, "req-1", ws, "search", q)
	require.NoError(t, err)

	// 2. Log duplicate request (must purge req-1 since it is pending and within 60s)
	err = store.LogRetrievalRequest(ctx, "req-2", ws, "search", q)
	require.NoError(t, err)

	// Verify req-1 was deleted, and req-2 exists
	var count int
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM retrieval_requests WHERE id = 'req-1'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM retrieval_requests WHERE id = 'req-2'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// 3. Score req-2 to make it not pending
	err = store.RecordRequestFeedback(ctx, "req-2", 5, "good", 1, 1)
	require.NoError(t, err)

	// 4. Log duplicate request again (should NOT purge req-2 because it is scored/not pending)
	err = store.LogRetrievalRequest(ctx, "req-3", ws, "search", q)
	require.NoError(t, err)

	// Verify req-2 still exists, and req-3 exists
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM retrieval_requests WHERE id = 'req-2'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM retrieval_requests WHERE id = 'req-3'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
