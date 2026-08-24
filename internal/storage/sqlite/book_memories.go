package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) ListAcceptedBookMemoryProposals(ctx context.Context, workspace, principalID string) ([]core.BookMemoryProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT proposal_json FROM book_memory_proposals WHERE workspace = ? AND status = ? ORDER BY created_at DESC, id`, workspace, core.ProposalAccepted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.BookMemoryProposal{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var proposal core.BookMemoryProposal
		if err := json.Unmarshal([]byte(payload), &proposal); err != nil {
			return nil, err
		}
		if proposal.RequestedBy.ID == principalID {
			out = append(out, proposal)
		}
	}
	return out, rows.Err()
}

func (s *Store) PutBookMemoryProposal(ctx context.Context, proposal core.BookMemoryProposal) error {
	if err := proposal.Validate(); err != nil {
		return err
	}
	return s.writeBookMemoryProposal(ctx, proposal)
}

func (s *Store) writeBookMemoryProposal(ctx context.Context, proposal core.BookMemoryProposal) error {
	payload, err := json.Marshal(proposal)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO book_memory_proposals (id, workspace, status, proposal_json, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET status = excluded.status, proposal_json = excluded.proposal_json
`, proposal.ID, proposal.Workspace, proposal.Status, string(payload), proposal.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put book memory proposal: %w", err)
	}
	return nil
}

func (s *Store) GetBookMemoryProposal(ctx context.Context, id string) (core.BookMemoryProposal, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT proposal_json FROM book_memory_proposals WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return core.BookMemoryProposal{}, errors.New("book memory proposal not found")
	}
	if err != nil {
		return core.BookMemoryProposal{}, err
	}
	var proposal core.BookMemoryProposal
	if err := json.Unmarshal([]byte(payload), &proposal); err != nil {
		return core.BookMemoryProposal{}, err
	}
	return proposal, nil
}

func (s *Store) UpdateBookMemoryProposal(ctx context.Context, proposal core.BookMemoryProposal) error {
	if proposal.Status == core.ProposalSuggested {
		return proposal.Validate()
	}
	if proposal.Status != core.ProposalAccepted && proposal.Status != core.ProposalRejected {
		return errors.New("invalid reviewed proposal status")
	}
	return s.writeBookMemoryProposal(ctx, proposal)
}

func (s *Store) PutBookMemoryLineage(ctx context.Context, lineage core.BookMemoryLineage) error {
	payload, err := json.Marshal(lineage)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO book_memory_lineage (memory_id, proposal_id, lineage_json, created_at)
VALUES (?, ?, ?, ?)
`, lineage.MemoryID, lineage.ProposalID, string(payload), lineage.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put book memory lineage: %w", err)
	}
	return nil
}

func (s *Store) GetBookMemoryLineage(ctx context.Context, memoryID string) (core.BookMemoryLineage, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT lineage_json FROM book_memory_lineage WHERE memory_id = ?`, memoryID).Scan(&payload)
	if err != nil {
		return core.BookMemoryLineage{}, err
	}
	var lineage core.BookMemoryLineage
	if err := json.Unmarshal([]byte(payload), &lineage); err != nil {
		return core.BookMemoryLineage{}, err
	}
	return lineage, nil
}
