package sqlite

import (
	"context"
	"encoding/json"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) PutBookReconsolidation(ctx context.Context, r core.BookReconsolidation) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO book_reconsolidations(id,previous_memory_id,new_memory_id,record_json,created_at) VALUES(?,?,?,?,?)`, r.ID, r.PreviousMemoryID, r.NewMemoryID, string(b), r.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
