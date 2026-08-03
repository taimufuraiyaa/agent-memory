package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type NoteChunk struct {
	Ordinal   int
	Heading   string
	StartLine int
	EndLine   int
	Content   string
	Hash      string
}

func ChunkNote(body string, targetChars int) []NoteChunk {
	if targetChars < 80 {
		targetChars = 80
	}
	lines := strings.Split(body, "\n")
	chunks := make([]NoteChunk, 0)
	start := 0
	heading := ""
	flush := func(end int) {
		if end <= start {
			return
		}
		content := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if content == "" {
			start = end
			return
		}
		sum := sha256.Sum256([]byte(content))
		chunks = append(chunks, NoteChunk{
			Ordinal:   len(chunks),
			Heading:   heading,
			StartLine: start + 1,
			EndLine:   end,
			Content:   content,
			Hash:      hex.EncodeToString(sum[:]),
		})
		start = end
	}

	chars := 0
	for index, line := range lines {
		nextHeading := markdownHeading(line)
		if nextHeading != "" && index > start && chars >= targetChars/2 {
			flush(index)
			chars = 0
		}
		if nextHeading != "" {
			heading = nextHeading
		}
		chars += len(line) + 1
		if chars >= targetChars && strings.TrimSpace(line) == "" {
			flush(index + 1)
			chars = 0
		}
	}
	flush(len(lines))
	return chunks
}

func markdownHeading(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return ""
	}
	trimmed = strings.TrimLeft(trimmed, "#")
	return strings.TrimSpace(trimmed)
}

func (s *NoteService) IndexLatest(ctx context.Context, workspace, noteID string) (indexErr error) {
	if s.writer == nil {
		return errors.New("note indexer is not configured")
	}
	note, err := s.Get(ctx, workspace, noteID)
	if err != nil {
		return err
	}
	if note.DeletedAt != nil {
		return sqlite.ErrNoteNotFound
	}
	if err := s.store.SetNoteIndexState(ctx, workspace, noteID, note.Revision, core.NoteIndexIndexing, note.IndexedRevision, ""); err != nil {
		return err
	}
	defer func() {
		if indexErr != nil {
			_ = s.store.SetNoteIndexState(context.Background(), workspace, noteID, note.Revision, core.NoteIndexFailed, note.IndexedRevision, indexErr.Error())
		}
	}()

	chunks := ChunkNote(note.Body, 1400)
	mappings := make([]core.NoteMemoryChunk, 0, len(chunks))
	newMemoryIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		memoryID := uuid.NewString()
		retrievalContent := chunk.Content
		if !strings.Contains(strings.ToLower(chunk.Content), strings.ToLower(note.Title)) {
			retrievalContent = note.Title + "\n\n" + chunk.Content
		}
		result, err := s.writer.Write(ctx, engine.WriteInput{
			ID:        memoryID,
			Workspace: workspace,
			Type:      core.SemanticMemory,
			Content:   retrievalContent,
			Source: core.MemorySource{
				Type:         core.SourceUserInput,
				FilePath:     note.Path,
				LineRange:    []int{chunk.StartLine, chunk.EndLine},
				NoteID:       note.ID,
				NoteRevision: note.Revision,
				NotePath:     note.Path,
				Heading:      chunk.Heading,
			},
			Tags:            []string{"human-note", "note:" + note.ID},
			Mode:            engine.ExtractFast,
			ContentHashSalt: fmt.Sprintf("note:%s:revision:%d:chunk:%d", note.ID, note.Revision, chunk.Ordinal),
		})
		if err != nil {
			_ = s.store.DeleteByIDs(ctx, newMemoryIDs)
			return err
		}
		if result.Rejected {
			_ = s.store.DeleteByIDs(ctx, newMemoryIDs)
			return fmt.Errorf("note chunk %d rejected: %s", chunk.Ordinal, result.RejectReason)
		}
		memoryID = result.ID
		newMemoryIDs = append(newMemoryIDs, memoryID)
		mappings = append(mappings, core.NoteMemoryChunk{
			Workspace:   workspace,
			NoteID:      note.ID,
			Revision:    note.Revision,
			Ordinal:     chunk.Ordinal,
			Heading:     chunk.Heading,
			StartLine:   chunk.StartLine,
			EndLine:     chunk.EndLine,
			ContentHash: chunk.Hash,
			MemoryID:    memoryID,
			Active:      true,
		})
	}
	retiredIDs, err := s.store.ActivateNoteChunks(ctx, workspace, noteID, note.Revision, mappings)
	if err != nil {
		_ = s.store.DeleteByIDs(ctx, newMemoryIDs)
		return err
	}
	if len(retiredIDs) > 0 {
		if err := s.store.DeleteByIDs(ctx, retiredIDs); err != nil {
			return fmt.Errorf("retire prior note chunks: %w", err)
		}
	}
	return nil
}

func (s *NoteService) RetireIndex(ctx context.Context, workspace, noteID string) error {
	ids, err := s.store.RetireNoteChunks(ctx, workspace, noteID)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		return s.store.DeleteByIDs(ctx, ids)
	}
	return nil
}
