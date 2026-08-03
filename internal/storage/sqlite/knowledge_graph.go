package sqlite

import (
	"context"
	"encoding/json"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) PutKnowledgeEdge(ctx context.Context, e core.KnowledgeEdge) error {
	if err := e.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_edges(id,from_id,to_id,edge_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET edge_json=excluded.edge_json`, e.ID, e.FromID, e.ToID, string(b), e.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
func (s *Store) ListKnowledgeEdgesFrom(ctx context.Context, id string) ([]core.KnowledgeEdge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT edge_json FROM knowledge_edges WHERE from_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.KnowledgeEdge{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var e core.KnowledgeEdge
		if err := json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
