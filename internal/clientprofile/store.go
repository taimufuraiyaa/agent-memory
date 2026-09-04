package clientprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const RegistryFilename = "client-profiles.json"

const registrySchemaVersion = 1

const (
	KindCodex  = "codex"
	KindClaude = "claude"
	KindCursor = "cursor"
	KindKiro   = "kiro"
	KindOther  = "other"

	ProfileDefault  = "default"
	ProfileExpanded = "expanded"
)

var (
	ErrNotFound         = errors.New("client profile not found")
	ErrConflict         = errors.New("client profile already exists")
	ErrRevisionConflict = errors.New("client profile revision conflict")
	ErrValidation       = errors.New("invalid client profile")
	ErrStorage          = errors.New("client profile storage unavailable")

	clientIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

type Profile struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	ClientKind  string    `json:"client_kind"`
	ToolProfile string    `json:"tool_profile"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Input struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ClientKind  string `json:"client_kind"`
	ToolProfile string `json:"tool_profile"`
}

type registry struct {
	SchemaVersion int       `json:"schema_version"`
	Profiles      []Profile `json:"profiles"`
}

type Store struct {
	mu       sync.RWMutex
	path     string
	now      func() time.Time
	profiles map[string]Profile
}

func Open(baseDir string, now func() time.Time) (*Store, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("%w: base directory is required", ErrStorage)
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: create base directory: %v", ErrStorage, err)
	}
	store := &Store{
		path:     filepath.Join(baseDir, RegistryFilename),
		now:      now,
		profiles: make(map[string]Profile),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedProfiles(s.profiles)
}

func (s *Store) Get(id string) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profiles[strings.TrimSpace(id)]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return profile, nil
}

func (s *Store) Create(input Input) (Profile, error) {
	input = normalizeInput(input)
	if err := validateInput(input, true); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.profiles[input.ID]; exists {
		return Profile{}, ErrConflict
	}
	now := s.now().UTC()
	profile := Profile{
		ID:          input.ID,
		DisplayName: input.DisplayName,
		ClientKind:  input.ClientKind,
		ToolProfile: input.ToolProfile,
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	next := cloneProfiles(s.profiles)
	next[profile.ID] = profile
	if err := s.persist(next); err != nil {
		return Profile{}, err
	}
	s.profiles = next
	return profile, nil
}

func (s *Store) Update(id string, expectedRevision int64, input Input) (Profile, error) {
	id = strings.TrimSpace(id)
	input = normalizeInput(input)
	input.ID = id
	if err := validateInput(input, true); err != nil {
		return Profile{}, err
	}
	if expectedRevision < 1 {
		return Profile{}, fmt.Errorf("%w: expected_revision must be positive", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.profiles[id]
	if !exists {
		return Profile{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Profile{}, ErrRevisionConflict
	}
	updated := current
	updated.DisplayName = input.DisplayName
	updated.ClientKind = input.ClientKind
	updated.ToolProfile = input.ToolProfile
	updated.Revision++
	updated.UpdatedAt = s.now().UTC()
	next := cloneProfiles(s.profiles)
	next[id] = updated
	if err := s.persist(next); err != nil {
		return Profile{}, err
	}
	s.profiles = next
	return updated, nil
}

func (s *Store) Delete(id string, expectedRevision int64) error {
	id = strings.TrimSpace(id)
	if !clientIDPattern.MatchString(id) || expectedRevision < 1 {
		return fmt.Errorf("%w: valid id and positive expected_revision are required", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.profiles[id]
	if !exists {
		return ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ErrRevisionConflict
	}
	next := cloneProfiles(s.profiles)
	delete(next, id)
	if err := s.persist(next); err != nil {
		return err
	}
	s.profiles = next
	return nil
}

func ValidateID(id string) error {
	id = strings.TrimSpace(id)
	if !clientIDPattern.MatchString(id) {
		return fmt.Errorf("%w: id must be a lowercase slug up to 64 characters", ErrValidation)
	}
	return nil
}

func (s *Store) load() error {
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read registry: %v", ErrStorage, err)
	}
	var saved registry
	if err := json.Unmarshal(content, &saved); err != nil {
		return fmt.Errorf("%w: decode registry: %v", ErrStorage, err)
	}
	if saved.SchemaVersion != registrySchemaVersion {
		return fmt.Errorf("%w: unsupported registry schema version %d", ErrStorage, saved.SchemaVersion)
	}
	for _, profile := range saved.Profiles {
		input := Input{ID: profile.ID, DisplayName: profile.DisplayName, ClientKind: profile.ClientKind, ToolProfile: profile.ToolProfile}
		if err := validateInput(input, true); err != nil || profile.Revision < 1 || profile.CreatedAt.IsZero() || profile.UpdatedAt.IsZero() {
			return fmt.Errorf("%w: invalid persisted record %q", ErrStorage, profile.ID)
		}
		if _, exists := s.profiles[profile.ID]; exists {
			return fmt.Errorf("%w: duplicate persisted record %q", ErrStorage, profile.ID)
		}
		s.profiles[profile.ID] = profile
	}
	return nil
}

func (s *Store) persist(profiles map[string]Profile) error {
	data, err := json.MarshalIndent(registry{SchemaVersion: registrySchemaVersion, Profiles: sortedProfiles(profiles)}, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode registry: %v", ErrStorage, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".client-profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create temporary registry: %v", ErrStorage, err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: secure temporary registry: %v", ErrStorage, err)
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: write temporary registry: %v", ErrStorage, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: sync temporary registry: %v", ErrStorage, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: close temporary registry: %v", ErrStorage, err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("%w: replace registry: %v", ErrStorage, err)
	}
	removeTemp = false
	return nil
}

func normalizeInput(input Input) Input {
	input.ID = strings.TrimSpace(input.ID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ClientKind = strings.TrimSpace(input.ClientKind)
	input.ToolProfile = strings.TrimSpace(input.ToolProfile)
	return input
}

func validateInput(input Input, requireID bool) error {
	if requireID {
		if err := ValidateID(input.ID); err != nil {
			return err
		}
	}
	if input.DisplayName == "" || len(input.DisplayName) > 80 {
		return fmt.Errorf("%w: display_name must contain 1 to 80 characters", ErrValidation)
	}
	if !oneOf(input.ClientKind, KindCodex, KindClaude, KindCursor, KindKiro, KindOther) {
		return fmt.Errorf("%w: unsupported client_kind %q", ErrValidation, input.ClientKind)
	}
	if !oneOf(input.ToolProfile, ProfileDefault, ProfileExpanded) {
		return fmt.Errorf("%w: unsupported tool_profile %q", ErrValidation, input.ToolProfile)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func cloneProfiles(source map[string]Profile) map[string]Profile {
	result := make(map[string]Profile, len(source))
	for id, profile := range source {
		result[id] = profile
	}
	return result
}

func sortedProfiles(source map[string]Profile) []Profile {
	result := make([]Profile, 0, len(source))
	for _, profile := range source {
		result = append(result, profile)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
