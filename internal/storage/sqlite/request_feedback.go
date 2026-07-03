package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// LogRetrievalRequest inserts a new query/recall request log entry with a pending score of -1.
func (s *Store) LogRetrievalRequest(ctx context.Context, id, workspace, requestType, query string) error {
	if s == nil {
		return errors.New("store is nil")
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO retrieval_requests (id, workspace, request_type, query, score, created_at)
		VALUES (?, ?, ?, ?, -1, ?)`,
		id, workspace, requestType, query, createdAt,
	)
	return err
}

// RecordRequestFeedback records a user score (0 to 5), a reason, and optional useful/total memory counts for a specific request ID.
func (s *Store) RecordRequestFeedback(ctx context.Context, id string, score int, reason string, usefulCount, totalCount int) error {
	if s == nil {
		return errors.New("store is nil")
	}
	if score < 0 || score > 5 {
		return errors.New("invalid score: must be between 0 and 5")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE retrieval_requests
		SET score = ?, reason = ?, useful_count = ?, total_count = ?
		WHERE id = ?`,
		score, reason, usefulCount, totalCount, id,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("request %s not found", id)
	}
	return nil
}

// GetFeedbackStats aggregates feedback score averages for the past week, month, and year.
func (s *Store) GetFeedbackStats(ctx context.Context, workspace string) (*core.FeedbackStats, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT 
			COALESCE(AVG(CASE WHEN datetime(created_at) >= datetime('now', '-7 days') THEN score END), 0.0) as avg_week,
			COALESCE(AVG(CASE WHEN datetime(created_at) >= datetime('now', '-30 days') THEN score END), 0.0) as avg_month,
			COALESCE(AVG(CASE WHEN datetime(created_at) >= datetime('now', '-365 days') THEN score END), 0.0) as avg_year,
			COUNT(CASE WHEN score >= 0 THEN 1 END) as total_count,
			COALESCE(AVG(CASE WHEN useful_count >= 0 THEN useful_count END), 0.0) as avg_useful,
			COALESCE(AVG(CASE WHEN total_count >= 0 THEN total_count END), 0.0) as avg_total,
			COALESCE(AVG(CASE WHEN total_count > 0 AND useful_count >= 0 THEN CAST(useful_count AS REAL) / total_count END), 0.0) as avg_ratio
		FROM retrieval_requests
		WHERE workspace = ? AND score >= 0`,
		workspace,
	)
	var stats core.FeedbackStats
	stats.Workspace = workspace
	err := row.Scan(&stats.AverageWeek, &stats.AverageMonth, &stats.AverageYear, &stats.TotalFeedbackCount, &stats.AverageUsefulCount, &stats.AverageTotalCount, &stats.AverageUsefulRatio)
	if err != nil {
		return nil, err
	}

	dist := map[string]int{"0": 0, "1": 0, "2": 0, "3": 0, "4": 0, "5": 0}
	rows, errQuery := s.db.QueryContext(ctx, `
		SELECT score, COUNT(*)
		FROM retrieval_requests
		WHERE workspace = ? AND score >= 0 AND score <= 5
		GROUP BY score`,
		workspace,
	)
	if errQuery == nil {
		defer rows.Close()
		for rows.Next() {
			var scoreVal int
			var countVal int
			if errScan := rows.Scan(&scoreVal, &countVal); errScan == nil {
				dist[fmt.Sprintf("%d", scoreVal)] = countVal
			}
		}
	}
	stats.ScoreDistribution = dist
	return &stats, nil
}

// ListRetrievalRequests returns all retrieval requests in the database (ordered by created_at DESC).
func (s *Store) ListRetrievalRequests(ctx context.Context, workspace string) ([]core.RetrievalRequestLog, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace, request_type, query, score, reason, useful_count, total_count, created_at
		FROM retrieval_requests
		WHERE workspace = ?
		ORDER BY datetime(created_at) DESC`,
		workspace,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []core.RetrievalRequestLog
	for rows.Next() {
		var r core.RetrievalRequestLog
		if err := rows.Scan(&r.ID, &r.Workspace, &r.RequestType, &r.Query, &r.Score, &r.Reason, &r.UsefulCount, &r.TotalCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}
