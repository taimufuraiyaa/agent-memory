// Package replay imports sanitized external session timelines.
package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type Importer struct{ store *sqlite.Store }

type ImportOptions struct{ Workspace, Path string }

type ImportResult struct {
	Imported       int `json:"imported"`
	Deduplicated   int `json:"deduplicated"`
	Malformed      int `json:"malformed"`
	Files          int `json:"files"`
	ResumedFrom    int `json:"resumed_from"`
	CheckpointLine int `json:"checkpoint_line"`
}

func NewImporter(store *sqlite.Store) *Importer { return &Importer{store: store} }

var sensitivePath = regexp.MustCompile(`(?i)(^|[._-])(secrets?|credentials?|passwords?|tokens?|\.env)([._-]|$)`)

func (i *Importer) Import(ctx context.Context, options ImportOptions) (ImportResult, error) {
	if strings.TrimSpace(options.Workspace) == "" {
		return ImportResult{}, errors.New("workspace is required")
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return ImportResult{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ImportResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ImportResult{}, errors.New("symlink imports are not allowed")
	}
	if sensitivePath.MatchString(filepath.Base(path)) {
		return ImportResult{}, errors.New("sensitive import path is not allowed")
	}
	if info.IsDir() {
		return i.importDirectory(ctx, options.Workspace, path)
	}
	return i.importFile(ctx, options.Workspace, path)
}

func (i *Importer) importDirectory(ctx context.Context, workspace, root string) (ImportResult, error) {
	total := ImportResult{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			return nil
		}
		if sensitivePath.MatchString(entry.Name()) {
			return nil
		}
		result, err := i.importFile(ctx, workspace, path)
		if err != nil {
			return err
		}
		total.Imported += result.Imported
		total.Deduplicated += result.Deduplicated
		total.Malformed += result.Malformed
		total.Files += result.Files
		return nil
	})
	return total, err
}

func (i *Importer) importFile(ctx context.Context, workspace, path string) (ImportResult, error) {
	checkpoint, err := i.store.ReplayImportCheckpoint(ctx, workspace, path)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{Files: 1, ResumedFrom: checkpoint, CheckpointLine: checkpoint}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber <= checkpoint {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			result.Malformed++
			_ = i.store.SetReplayImportCheckpoint(ctx, workspace, path, lineNumber)
			result.CheckpointLine = lineNumber
			continue
		}
		event, ok := normalizeRecord(workspace, filepath.Base(path), raw)
		if !ok {
			result.Malformed++
		} else {
			_, deduplicated, err := i.store.InsertObservationDedupWindow(ctx, event, 24*time.Hour)
			if err != nil {
				return result, err
			}
			if deduplicated {
				result.Deduplicated++
			} else {
				result.Imported++
				_ = i.store.UpsertSessionFromObservation(ctx, sqlite.ObserveUpsertSessionInput{Workspace: workspace, SessionID: event.SessionID, OccurredAt: event.OccurredAt, Kind: event.Kind})
			}
		}
		if err := i.store.SetReplayImportCheckpoint(ctx, workspace, path, lineNumber); err != nil {
			return result, err
		}
		result.CheckpointLine = lineNumber
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	_, _ = i.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "import_jsonl", Outcome: "success", Actor: "cli", Source: "jsonl", TargetType: "observation", TargetCount: result.Imported, Reason: "session transcript import", Metadata: map[string]any{"malformed": result.Malformed, "deduplicated": result.Deduplicated, "source": filepath.Base(path)}})
	return result, nil
}

func normalizeRecord(workspace, fallbackSession string, raw map[string]any) (sqlite.ObservationInsert, bool) {
	sessionID := text(raw["session_id"])
	if sessionID == "" {
		sessionID = strings.TrimSuffix(fallbackSession, filepath.Ext(fallbackSession))
	}
	kind := text(raw["type"])
	if kind == "" {
		kind = text(raw["kind"])
	}
	summary := firstText(raw, "message", "content", "prompt", "tool_response", "tool_output")
	if summary == "" || kind == "" {
		return sqlite.ObservationInsert{}, false
	}
	summary = engine.ClipString(engine.RedactPrivateAndSecrets(summary), 1200)
	occurredAt := time.Now().UTC()
	if value := firstText(raw, "timestamp", "occurred_at", "created_at"); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			occurredAt = parsed
		} else if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			occurredAt = parsed
		}
	}
	return sqlite.ObservationInsert{Workspace: workspace, SessionID: sessionID, OccurredAt: occurredAt, Kind: kind, ToolName: text(raw["tool_name"]), Summary: summary, SourceAgent: firstText(raw, "agent", "source_agent"), SourceAdapter: "jsonl-import", HookEvent: text(raw["hook_event"]), ExternalEventID: firstText(raw, "uuid", "id", "event_id"), SchemaVersion: "v1", CaptureMode: "imported"}, true
}

func firstText(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := text(raw[key]); value != "" {
			return value
		}
		if raw[key] != nil {
			encoded, _ := json.Marshal(raw[key])
			if string(encoded) != "null" {
				return string(encoded)
			}
		}
	}
	return ""
}

func text(value any) string { result, _ := value.(string); return strings.TrimSpace(result) }
