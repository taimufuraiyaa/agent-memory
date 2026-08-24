package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/hooks"
)

type FilesystemConfig struct {
	ID, Workspace string
	Roots, Ignore []string
	PreviewBytes  int
	PollInterval  time.Duration
}

type Filesystem struct {
	cfg    FilesystemConfig
	cancel context.CancelFunc
	mu     sync.RWMutex
	health Health
}

func NewFilesystem(cfg FilesystemConfig) *Filesystem {
	if cfg.PreviewBytes <= 0 {
		cfg.PreviewBytes = 1024
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	return &Filesystem{cfg: cfg, health: Health{ID: cfg.ID, State: "stopped"}}
}
func (f *Filesystem) ID() string { return f.cfg.ID }
func (f *Filesystem) Validate() error {
	if strings.TrimSpace(f.cfg.ID) == "" || strings.TrimSpace(f.cfg.Workspace) == "" {
		return errors.New("id and workspace are required")
	}
	if len(f.cfg.Roots) == 0 {
		return errors.New("at least one explicit root is required")
	}
	for _, root := range f.cfg.Roots {
		info, err := os.Lstat(root)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("root must be a real directory: %s", root)
		}
	}
	return nil
}
func (f *Filesystem) Start(ctx context.Context, emitter Emitter, store CheckpointStore) error {
	ctx, f.cancel = context.WithCancel(ctx)
	f.setHealth("running", "")
	go f.loop(ctx, emitter, store)
	return nil
}
func (f *Filesystem) Stop(context.Context) error {
	if f.cancel != nil {
		f.cancel()
	}
	f.setHealth("stopped", "")
	return nil
}
func (f *Filesystem) Health() Health { f.mu.RLock(); defer f.mu.RUnlock(); return f.health }
func (f *Filesystem) setHealth(state, errText string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.health.State, f.health.LastError, f.health.UpdatedAt = state, errText, time.Now().UTC()
}
func (f *Filesystem) loop(ctx context.Context, emitter Emitter, store CheckpointStore) {
	ticker := time.NewTicker(f.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if err := f.Scan(ctx, emitter, store); err != nil {
			f.setHealth("degraded", err.Error())
		} else {
			f.setHealth("running", "")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (f *Filesystem) Scan(ctx context.Context, emitter Emitter, store CheckpointStore) error {
	cp, err := store.Load(ctx, f.cfg.ID)
	if err != nil {
		cp = Checkpoint{ConnectorID: f.cfg.ID, State: map[string]string{}}
	}
	current, err := f.snapshot()
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(current)+len(cp.State))
	for p := range current {
		paths = append(paths, p)
	}
	for p := range cp.State {
		if _, ok := current[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			cp.CoalescedCount++
			continue
		}
		seen[path] = true
		old, existed := cp.State[path]
		now, exists := current[path]
		if existed && exists && old == now {
			continue
		}
		kind := "filesystem.modify"
		if !existed {
			kind = "filesystem.create"
		}
		if !exists {
			kind = "filesystem.delete"
		}
		summary := path
		if exists {
			summary += "\n" + f.preview(path)
		}
		event := hooks.Event{Workspace: f.cfg.Workspace, Kind: kind, Summary: summary, SourceAgent: "filesystem", SourceAdapter: f.cfg.ID, HookEvent: kind, ExternalEventID: eventID(f.cfg.ID, path, now, kind), SchemaVersion: "v1", CaptureMode: "connector"}
		if err := emitter.Emit(ctx, event); err != nil {
			cp.LastError = err.Error()
			_ = store.Save(ctx, cp)
			return err
		}
		cp.EmittedCount++
	}
	cp.State = current
	cp.RescannedCount++
	cp.LastError = ""
	cp.UpdatedAt = time.Now().UTC()
	return store.Save(ctx, cp)
}
func (f *Filesystem) snapshot() (map[string]string, error) {
	state := map[string]string{}
	for _, root := range f.cfg.Roots {
		canonical, _ := filepath.EvalSymlinks(root)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == root {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if f.ignored(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			resolved, _ := filepath.EvalSymlinks(path)
			if resolved != "" && resolved != canonical && !strings.HasPrefix(resolved, canonical+string(os.PathSeparator)) {
				return nil
			}
			if info.Mode().IsRegular() {
				state[path] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}
func (f *Filesystem) ignored(rel string) bool {
	for _, pattern := range f.cfg.Ignore {
		if ok, _ := filepath.Match(pattern, rel); ok {
			return true
		}
		if strings.HasPrefix(rel, strings.TrimSuffix(pattern, "/")+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
func (f *Filesystem) preview(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(f.cfg.PreviewBytes)))
	if err != nil {
		return ""
	}
	return engine.RedactPrivateAndSecrets(string(data))
}
func eventID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
