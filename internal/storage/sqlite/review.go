package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) PutKnowledgeReview(ctx context.Context, r library.KnowledgeReview) error {
	if err := r.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_reviews(id,review_json,state,version) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET review_json=excluded.review_json,state=excluded.state,version=excluded.version`, r.ID, string(b), r.State, r.Version)
	return err
}
func (s *Store) GetKnowledgeReview(ctx context.Context, id string) (library.KnowledgeReview, error) {
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT review_json FROM knowledge_reviews WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return library.KnowledgeReview{}, errors.New("knowledge review not found")
	}
	if err != nil {
		return library.KnowledgeReview{}, err
	}
	var r library.KnowledgeReview
	err = json.Unmarshal([]byte(b), &r)
	return r, err
}
func (s *Store) AppendReviewTransition(ctx context.Context, v library.ReviewTransition) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_review_transitions(review_id,version,transition_json,occurred_at) VALUES(?,?,?,?)`, v.ReviewID, v.Version, string(b), v.At.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
func (s *Store) ListReviewTransitions(ctx context.Context, id string) ([]library.ReviewTransition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT transition_json FROM knowledge_review_transitions WHERE review_id=? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []library.ReviewTransition{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var v library.ReviewTransition
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
