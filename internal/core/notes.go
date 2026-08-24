package core

import (
	"strings"
	"time"
)

type NoteIndexState string

const (
	NoteIndexPending  NoteIndexState = "pending"
	NoteIndexIndexing NoteIndexState = "indexing"
	NoteIndexReady    NoteIndexState = "ready"
	NoteIndexFailed   NoteIndexState = "failed"
	NoteIndexRetired  NoteIndexState = "retired"
	NoteIndexPaused   NoteIndexState = "paused"
)

type Note struct {
	ID              string         `json:"id"`
	Workspace       string         `json:"workspace"`
	Path            string         `json:"path"`
	Title           string         `json:"title"`
	Body            string         `json:"body,omitempty"`
	Properties      map[string]any `json:"properties"`
	Revision        int            `json:"revision"`
	ContentHash     string         `json:"content_hash"`
	IndexState      NoteIndexState `json:"index_state"`
	IndexedRevision int            `json:"indexed_revision"`
	IndexError      string         `json:"index_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty"`
}

type NoteRevision struct {
	NoteID      string         `json:"note_id"`
	Workspace   string         `json:"workspace"`
	Revision    int            `json:"revision"`
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Properties  map[string]any `json:"properties"`
	ContentHash string         `json:"content_hash"`
	AuthorKind  string         `json:"author_kind"`
	CreatedAt   time.Time      `json:"created_at"`
}

type NoteLink struct {
	SourceNoteID string `json:"source_note_id"`
	TargetNoteID string `json:"target_note_id,omitempty"`
	RawTarget    string `json:"raw_target"`
	Line         int    `json:"line"`
	Snippet      string `json:"snippet"`
}

type NoteMemoryChunk struct {
	Workspace   string `json:"workspace"`
	NoteID      string `json:"note_id"`
	Revision    int    `json:"revision"`
	Ordinal     int    `json:"ordinal"`
	Heading     string `json:"heading,omitempty"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	ContentHash string `json:"content_hash"`
	MemoryID    string `json:"memory_id"`
	Active      bool   `json:"active"`
}

type CreateNoteInput struct {
	Workspace  string         `json:"workspace"`
	Path       string         `json:"path"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Properties map[string]any `json:"properties"`
	AuthorKind string         `json:"author_kind,omitempty"`
}

type UpdateNoteInput struct {
	Workspace        string         `json:"workspace"`
	NoteID           string         `json:"note_id"`
	ExpectedRevision int            `json:"expected_revision"`
	Path             string         `json:"path"`
	Title            string         `json:"title"`
	Body             string         `json:"body"`
	Properties       map[string]any `json:"properties"`
	AuthorKind       string         `json:"author_kind,omitempty"`
}

func (n Note) IsDeleted() bool {
	return n.DeletedAt != nil
}

func NormalizeNoteTitle(title, path string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		return title
	}
	path = strings.TrimSuffix(strings.TrimSpace(path), ".md")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	if path == "" {
		return "Untitled"
	}
	return path
}

// NoteTitleFromBody treats the first physical Markdown line as the note title.
// ATX heading markers are presentation syntax and are not part of the title.
func NoteTitleFromBody(body, fallback string) string {
	firstLine, _, _ := strings.Cut(body, "\n")
	title := strings.TrimSpace(firstLine)

	headingMarkers := 0
	for headingMarkers < len(title) && headingMarkers < 6 && title[headingMarkers] == '#' {
		headingMarkers++
	}
	isHeading := headingMarkers > 0 && (headingMarkers == len(title) || title[headingMarkers] == ' ' || title[headingMarkers] == '\t')
	if isHeading {
		title = strings.TrimSpace(title[headingMarkers:])
	}

	lastNonHash := len(title)
	for lastNonHash > 0 && title[lastNonHash-1] == '#' {
		lastNonHash--
	}
	if isHeading && lastNonHash < len(title) && lastNonHash > 0 && (title[lastNonHash-1] == ' ' || title[lastNonHash-1] == '\t') {
		title = strings.TrimSpace(title[:lastNonHash])
	}
	if title == "" {
		return strings.TrimSpace(fallback)
	}
	return title
}
