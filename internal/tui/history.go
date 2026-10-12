package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

type HistoryItem struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Query     string          `json:"query"`
	Result    json.RawMessage `json:"result"`
	CreatedAt time.Time       `json:"created_at"`
}
type historyBackend struct {
	Backend
	console              ConsoleBackend
	workspace, directory string
	mu                   *sync.Mutex
	warning              string
}

func WithHistory(backend Backend, workspace, directory string) (Backend, error) {
	if err := validation.ValidateWorkspaceName(workspace); err != nil {
		return nil, err
	}
	console, ok := backend.(ConsoleBackend)
	if !ok {
		return nil, errors.New("terminal history requires the workspace console")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("cannot create private terminal history directory")
	}
	return &historyBackend{Backend: backend, console: console, workspace: workspace, directory: directory, mu: &sync.Mutex{}}, nil
}

func (b *historyBackend) Search(ctx context.Context, query string, limit int) ([]MemoryItem, error) {
	items, err := b.Backend.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(items)
	b.record("search", query, raw)
	return items, nil
}

func (b *historyBackend) Execute(ctx context.Context, request Request) (json.RawMessage, error) {
	if strings.HasPrefix(request.Path, "/api/v1/tui/history") {
		return b.historyRequest(ctx, request)
	}
	raw, err := b.console.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if request.Path == "/api/v1/memories/recall/preview" {
		query, _ := request.Values["task_description"].(string)
		b.record("chat", query, raw)
	}
	if request.Path == "/api/v1/memories/search" {
		query, _ := request.Values["query"].(string)
		b.record("search", query, raw)
	}
	return raw, nil
}

func (b *historyBackend) SelectWorkspace(ctx context.Context, name string) (Backend, error) {
	selector, ok := b.Backend.(WorkspaceSelector)
	if !ok {
		return nil, errors.New("workspace switching unavailable")
	}
	backend, err := selector.SelectWorkspace(ctx, name)
	if err != nil {
		return nil, err
	}
	next, err := WithHistory(backend, name, b.directory)
	if err != nil {
		return nil, err
	}
	next.(*historyBackend).mu = b.mu
	return next, nil
}

func (b *historyBackend) load() ([]HistoryItem, error) {
	file, err := os.Open(filepath.Join(b.directory, b.workspace+".json"))
	if os.IsNotExist(err) {
		return []HistoryItem{}, nil
	}
	if err != nil {
		return nil, errors.New("cannot read local history")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errors.New("local history is not a bounded regular file")
	}
	var items []HistoryItem
	if err := json.NewDecoder(io.LimitReader(file, 16<<20+1)).Decode(&items); err != nil {
		return nil, errors.New("local history is unreadable; preserve the file before repairing it")
	}
	return items, nil
}

func (b *historyBackend) save(items []HistoryItem) error {
	if len(items) > 50 {
		items = items[len(items)-50:]
	}
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(b.directory, ".history-*")
	if err != nil {
		return errors.New("cannot create local history update")
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(b.directory, b.workspace+".json")); err != nil {
		return errors.New("cannot save local history")
	}
	return nil
}

func (b *historyBackend) record(kind, query string, result json.RawMessage) {
	if strings.TrimSpace(query) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	items, err := b.load()
	if err != nil {
		b.warning = err.Error()
		return
	}
	// Keep metadata for large results without duplicating an entire workspace.
	if len(result) > 128<<10 {
		result = json.RawMessage(`{"notice":"Large result omitted from local snapshot; run the query again."}`)
	}
	title := []rune(query)
	if len(title) > 80 {
		title = title[:80]
	}
	items = append(items, HistoryItem{ID: uuid.NewString(), Kind: kind, Title: string(title), Query: query, Result: result, CreatedAt: time.Now().UTC()})
	if err := b.save(items); err != nil {
		b.warning = "Query succeeded, but local history could not be saved."
	} else {
		b.warning = ""
	}
}

func (b *historyBackend) historyRequest(ctx context.Context, request Request) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	items, err := b.load()
	if err != nil {
		return nil, err
	}
	if request.Path == "/api/v1/tui/history" && request.Method == "GET" {
		recent := append([]HistoryItem(nil), items...)
		for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
			recent[i], recent[j] = recent[j], recent[i]
		}
		return json.Marshal(map[string]any{"items": recent, "notice": b.warning})
	}
	if request.Method != "POST" {
		return nil, errors.New("unsupported local history operation")
	}
	id, _ := request.Values["id"].(string)
	for index, item := range items {
		if item.ID == id {
			switch request.Path {
			case "/api/v1/tui/history/rename":
				title, _ := request.Values["title"].(string)
				title = strings.TrimSpace(title)
				if title == "" || len(title) > 256 {
					return nil, errors.New("history title must contain 1 to 256 bytes")
				}
				items[index].Title = title
			case "/api/v1/tui/history/delete":
				items = append(items[:index], items[index+1:]...)
			default:
				return nil, errors.New("unsupported local history operation")
			}
			if err := b.save(items); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"updated":true}`), nil
		}
	}
	return nil, errors.New("local history record no longer exists; refresh")
}
