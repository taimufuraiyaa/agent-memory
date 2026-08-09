// Package deploymentprofile persists installation-level deployment planning
// metadata for the local product. It never provisions infrastructure.
package deploymentprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ProfileFilename = "deployment-profile.json"

	StatusAssumed                                         = "assumed"
	StatusOperatorConfirmed                               = "operator_confirmed"
	DefaultMonthlyInfrastructureOperationsBudgetUSD int64 = 1_000

	legacyProfileSchemaVersion = 1
	profileSchemaVersion       = 2
	maximumProfileBytes        = 16 << 10
	maximumMonthlyBudget       = 1_000_000
)

var (
	ErrValidation       = errors.New("invalid deployment profile")
	ErrRevisionConflict = errors.New("deployment profile revision conflict")
	ErrStorage          = errors.New("deployment profile storage unavailable")
)

type Profile struct {
	MonthlyInfrastructureOperationsBudgetUSD int64     `json:"monthly_infrastructure_operations_budget_usd"`
	DecisionStatus                           string    `json:"decision_status"`
	Revision                                 int64     `json:"revision"`
	CreatedAt                                time.Time `json:"created_at"`
	UpdatedAt                                time.Time `json:"updated_at"`
}

type Input struct {
	MonthlyInfrastructureOperationsBudgetUSD int64  `json:"monthly_infrastructure_operations_budget_usd"`
	DecisionStatus                           string `json:"decision_status"`
}

type persistedProfile struct {
	SchemaVersion int     `json:"schema_version"`
	Profile       Profile `json:"profile"`
}

type persistedEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Profile       json.RawMessage `json:"profile"`
}

type legacyProfile struct {
	CloudProvider                string    `json:"cloud_provider"`
	MonthlyStagingBudgetUSD      int64     `json:"monthly_staging_budget_usd"`
	PaidInfrastructureAuthorized bool      `json:"paid_infrastructure_authorized"`
	DecisionStatus               string    `json:"decision_status"`
	Revision                     int64     `json:"revision"`
	CreatedAt                    time.Time `json:"created_at"`
	UpdatedAt                    time.Time `json:"updated_at"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	now     func() time.Time
	profile Profile
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
		return nil, fmt.Errorf("%w: create base directory", ErrStorage)
	}
	baseInfo, err := os.Lstat(baseDir)
	if err != nil || !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: base directory must be a non-symlink directory", ErrStorage)
	}
	store := &Store{path: filepath.Join(baseDir, ProfileFilename), now: now}
	if err := store.load(); err != nil {
		return nil, err
	}
	if store.profile.Revision == 0 {
		created := now().UTC()
		store.profile = Profile{
			MonthlyInfrastructureOperationsBudgetUSD: DefaultMonthlyInfrastructureOperationsBudgetUSD,
			DecisionStatus:                           StatusAssumed, Revision: 1, CreatedAt: created, UpdatedAt: created,
		}
		if err := store.persist(store.profile); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Get() Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profile
}

func (s *Store) Update(expectedRevision int64, input Input) (Profile, error) {
	input = normalizeInput(input)
	if expectedRevision < 1 {
		return Profile{}, fmt.Errorf("%w: expected_revision must be positive", ErrValidation)
	}
	if err := validateInput(input); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.Revision != expectedRevision {
		return Profile{}, ErrRevisionConflict
	}
	updated := s.profile
	updated.MonthlyInfrastructureOperationsBudgetUSD = input.MonthlyInfrastructureOperationsBudgetUSD
	updated.DecisionStatus = input.DecisionStatus
	updated.Revision++
	updated.UpdatedAt = s.now().UTC()
	if err := s.persist(updated); err != nil {
		return Profile{}, err
	}
	s.profile = updated
	return updated, nil
}

func normalizeInput(input Input) Input {
	input.DecisionStatus = strings.ToLower(strings.TrimSpace(input.DecisionStatus))
	return input
}

func validateInput(input Input) error {
	if input.DecisionStatus != StatusAssumed && input.DecisionStatus != StatusOperatorConfirmed {
		return fmt.Errorf("%w: decision_status is unsupported", ErrValidation)
	}
	if input.MonthlyInfrastructureOperationsBudgetUSD < 0 || input.MonthlyInfrastructureOperationsBudgetUSD > maximumMonthlyBudget {
		return fmt.Errorf("%w: monthly_infrastructure_operations_budget_usd must be between 0 and %d", ErrValidation, maximumMonthlyBudget)
	}
	return nil
}

func validatePersisted(profile Profile) error {
	if err := validateInput(Input{MonthlyInfrastructureOperationsBudgetUSD: profile.MonthlyInfrastructureOperationsBudgetUSD, DecisionStatus: profile.DecisionStatus}); err != nil {
		return err
	}
	if profile.Revision < 1 || profile.CreatedAt.IsZero() || profile.UpdatedAt.IsZero() || profile.UpdatedAt.Before(profile.CreatedAt) {
		return fmt.Errorf("%w: persisted metadata is invalid", ErrValidation)
	}
	return nil
}

func (s *Store) load() error {
	validated, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !validated.Mode().IsRegular() || validated.Mode().Perm()&0o077 != 0 || validated.Size() <= 0 || validated.Size() > maximumProfileBytes {
		return fmt.Errorf("%w: profile must be a bounded regular non-symlink file", ErrStorage)
	}
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("%w: open profile", ErrStorage)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(validated, opened) || !opened.Mode().IsRegular() {
		return fmt.Errorf("%w: profile changed before open", ErrStorage)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumProfileBytes+1))
	if err != nil || len(data) > maximumProfileBytes {
		return fmt.Errorf("%w: read profile", ErrStorage)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() {
		return fmt.Errorf("%w: profile changed while reading", ErrStorage)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: close profile", ErrStorage)
	}
	closed = true

	var envelope persistedEnvelope
	if err := decodeStrictJSON(data, &envelope); err != nil {
		return fmt.Errorf("%w: decode profile envelope", ErrStorage)
	}
	switch envelope.SchemaVersion {
	case profileSchemaVersion:
		var profile Profile
		if err := decodeStrictJSON(envelope.Profile, &profile); err != nil {
			return fmt.Errorf("%w: decode current profile", ErrStorage)
		}
		if err := validatePersisted(profile); err != nil {
			return fmt.Errorf("%w: invalid persisted profile", ErrStorage)
		}
		s.profile = profile
		return nil
	case legacyProfileSchemaVersion:
		var legacy legacyProfile
		if err := decodeStrictJSON(envelope.Profile, &legacy); err != nil {
			return fmt.Errorf("%w: decode legacy profile", ErrStorage)
		}
		profile := Profile{
			MonthlyInfrastructureOperationsBudgetUSD: legacy.MonthlyStagingBudgetUSD,
			DecisionStatus:                           legacy.DecisionStatus, Revision: legacy.Revision,
			CreatedAt: legacy.CreatedAt, UpdatedAt: legacy.UpdatedAt,
		}
		if err := validatePersisted(profile); err != nil {
			return fmt.Errorf("%w: invalid legacy profile", ErrStorage)
		}
		if err := s.persist(profile); err != nil {
			return err
		}
		s.profile = profile
		return nil
	default:
		return fmt.Errorf("%w: unsupported schema version", ErrStorage)
	}
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func (s *Store) persist(profile Profile) error {
	data, err := json.MarshalIndent(persistedProfile{SchemaVersion: profileSchemaVersion, Profile: profile}, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode profile", ErrStorage)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".deployment-profile-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create temporary profile", ErrStorage)
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
		return fmt.Errorf("%w: secure temporary profile", ErrStorage)
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: write temporary profile", ErrStorage)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("%w: sync temporary profile", ErrStorage)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: close temporary profile", ErrStorage)
	}
	if target, err := os.Lstat(s.path); err == nil && !target.Mode().IsRegular() {
		return fmt.Errorf("%w: destination is not a regular file", ErrStorage)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect destination", ErrStorage)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("%w: replace profile", ErrStorage)
	}
	removeTemp = false
	return nil
}
