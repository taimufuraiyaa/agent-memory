package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

// ClaudeListenState is local user preference, not a Jev/provider readiness flag.
type ClaudeListenState struct {
	Workspace  string `json:"workspace"`
	Root       string `json:"root"`
	Enabled    bool   `json:"enabled"`
	JevEnabled bool   `json:"jev_enabled,omitempty"`
}

type ClaudeListenStore struct{ DataDir string }

func NewClaudeListenStore(dataDir string) *ClaudeListenStore {
	return &ClaudeListenStore{DataDir: dataDir}
}

func (s *ClaudeListenStore) Enable(workspaceName, registeredRoot string) (ClaudeListenState, error) {
	root, err := canonicalDirectory(registeredRoot)
	if err != nil {
		return ClaudeListenState{}, err
	}
	previous, err := s.Status(workspaceName)
	if err != nil {
		return ClaudeListenState{}, err
	}
	state := ClaudeListenState{Workspace: workspaceName, Root: root, Enabled: true, JevEnabled: previous.Enabled && previous.Root == root && previous.JevEnabled}
	return state, s.save(state)
}

func (s *ClaudeListenStore) SetJev(workspaceName, registeredRoot string, enabled bool) (ClaudeListenState, error) {
	state, err := s.Status(workspaceName)
	if err != nil {
		return ClaudeListenState{}, err
	}
	root, err := canonicalDirectory(registeredRoot)
	if err != nil || !state.Enabled || state.Root != root {
		return ClaudeListenState{}, errors.New("Claude listen is not enabled for this project")
	}
	state.JevEnabled = enabled
	return state, s.save(state)
}

func (s *ClaudeListenStore) save(state ClaudeListenState) error {
	path, err := s.path(state.Workspace, true)
	if err != nil {
		return err
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".listen-*.tmp")
	if err != nil {
		return errors.New("cannot create Claude listen state")
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.New("cannot secure Claude listen state")
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return errors.New("cannot write Claude listen state")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("cannot sync Claude listen state")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close Claude listen state")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot replace Claude listen state")
	}
	return nil
}

func (s *ClaudeListenStore) Status(workspaceName string) (ClaudeListenState, error) {
	path, err := s.path(workspaceName, false)
	if err != nil {
		return ClaudeListenState{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ClaudeListenState{Workspace: workspaceName}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > 2048 {
		return ClaudeListenState{}, errors.New("unsafe Claude listen state")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ClaudeListenState{}, errors.New("cannot read Claude listen state")
	}
	var state ClaudeListenState
	if json.Unmarshal(content, &state) != nil || state.Workspace != workspaceName || !state.Enabled || state.Root == "" {
		return ClaudeListenState{}, errors.New("invalid Claude listen state")
	}
	return state, nil
}

func (s *ClaudeListenStore) Disable(workspaceName string) error {
	state, err := s.Status(workspaceName)
	if err != nil || !state.Enabled {
		return err
	}
	path, err := s.path(workspaceName, false)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove Claude listen state")
	}
	return nil
}

// Allows requires an enabled listener and a current cwd within its registered root.
func (s *ClaudeListenStore) Allows(workspaceName, registeredRoot, cwd string) bool {
	state, err := s.Status(workspaceName)
	if err != nil || !state.Enabled || strings.TrimSpace(cwd) == "" {
		return false
	}
	root, err := canonicalDirectory(registeredRoot)
	if err != nil || root != state.Root {
		return false
	}
	return ProjectDirectoryWithin(root, cwd)
}

func (s *ClaudeListenStore) AllowsJev(workspaceName, registeredRoot, cwd string) bool {
	state, err := s.Status(workspaceName)
	return err == nil && state.JevEnabled && s.Allows(workspaceName, registeredRoot, cwd)
}

func ProjectDirectoryWithin(root, candidate string) bool {
	resolvedRoot, err := canonicalDirectory(root)
	if err != nil {
		return false
	}
	current, err := canonicalDirectory(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, current)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *ClaudeListenStore) path(workspaceName string, create bool) (string, error) {
	if err := validation.ValidateWorkspaceName(workspaceName); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.DataDir) == "" {
		return "", errors.New("Claude listen data directory is required")
	}
	base := filepath.Join(s.DataDir, "claude-listen")
	if create {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", errors.New("cannot create Claude listen directory")
		}
	}
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) && !create {
		return filepath.Join(base, listenFilename(workspaceName)), nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("unsafe Claude listen directory")
	}
	path := filepath.Join(base, listenFilename(workspaceName))
	if create {
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return "", errors.New("unsafe Claude listen state")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("cannot inspect Claude listen state")
		}
	}
	return path, nil
}

func listenFilename(workspaceName string) string {
	sum := sha256.Sum256([]byte(workspaceName))
	return hex.EncodeToString(sum[:16]) + ".json"
}

func canonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("registered project root is required")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("cannot resolve project directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("project root is not a directory")
	}
	return filepath.Abs(resolved)
}
