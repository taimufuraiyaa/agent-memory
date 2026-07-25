package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type NoteService struct {
	store  *sqlite.Store
	writer *engine.WritePipeline

	indexMu     sync.Mutex
	indexTimers map[string]*time.Timer
}

func NewNoteService(store *sqlite.Store, writers ...*engine.WritePipeline) *NoteService {
	var writer *engine.WritePipeline
	if len(writers) > 0 {
		writer = writers[0]
	}
	return &NoteService{store: store, writer: writer, indexTimers: make(map[string]*time.Timer)}
}

func (s *NoteService) Create(ctx context.Context, input core.CreateNoteInput) (*core.Note, error) {
	input.Workspace = strings.TrimSpace(input.Workspace)
	return s.store.CreateNote(ctx, input)
}

func (s *NoteService) Update(ctx context.Context, input core.UpdateNoteInput) (*core.Note, error) {
	input.Workspace = strings.TrimSpace(input.Workspace)
	input.NoteID = strings.TrimSpace(input.NoteID)
	return s.store.UpdateNote(ctx, input)
}

func (s *NoteService) Get(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	return s.store.GetNote(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *NoteService) List(ctx context.Context, workspace string, includeDeleted bool) ([]core.Note, error) {
	return s.store.ListNotes(ctx, strings.TrimSpace(workspace), includeDeleted)
}

func (s *NoteService) Trash(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	note, err := s.store.TrashNote(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	if err := s.RetireIndex(ctx, workspace, noteID); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) Restore(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	return s.store.RestoreNote(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *NoteService) DeletePermanently(ctx context.Context, workspace, noteID string) error {
	return s.store.DeleteNotePermanently(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *NoteService) Revisions(ctx context.Context, workspace, noteID string) ([]core.NoteRevision, error) {
	return s.store.ListNoteRevisions(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *NoteService) RestoreRevision(ctx context.Context, workspace, noteID string, revision, expectedRevision int) (*core.Note, error) {
	if revision < 1 {
		return nil, errors.New("revision must be positive")
	}
	historical, err := s.store.GetNoteRevision(ctx, workspace, noteID, revision)
	if err != nil {
		return nil, err
	}
	return s.Update(ctx, core.UpdateNoteInput{
		Workspace:        workspace,
		NoteID:           noteID,
		ExpectedRevision: expectedRevision,
		Path:             historical.Path,
		Title:            historical.Title,
		Body:             historical.Body,
		Properties:       historical.Properties,
		AuthorKind:       "revision_restore",
	})
}

func (s *NoteService) Backlinks(ctx context.Context, workspace, noteID string) ([]core.NoteLink, error) {
	return s.store.ListNoteBacklinks(ctx, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *NoteService) ScheduleIndex(workspace, noteID string, delay time.Duration) {
	workspace = strings.TrimSpace(workspace)
	noteID = strings.TrimSpace(noteID)
	if workspace == "" || noteID == "" || s.writer == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	key := workspace + "\x00" + noteID
	s.indexMu.Lock()
	if existing := s.indexTimers[key]; existing != nil {
		existing.Stop()
	}
	s.indexTimers[key] = time.AfterFunc(delay, func() {
		s.indexMu.Lock()
		delete(s.indexTimers, key)
		s.indexMu.Unlock()
		_ = s.IndexLatest(context.Background(), workspace, noteID)
	})
	s.indexMu.Unlock()
}
