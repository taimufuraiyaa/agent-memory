package deploymentprofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenPersistsSelfManagedInfrastructureAssumptions(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	store, err := Open(root, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	profile := store.Get()
	if profile.MonthlyInfrastructureOperationsBudgetUSD != 1_000 || profile.DecisionStatus != StatusAssumed {
		t.Fatalf("unexpected first-run assumptions: %+v", profile)
	}
	if profile.Revision != 1 || !profile.CreatedAt.Equal(now) || !profile.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected first-run metadata: %+v", profile)
	}
	path := filepath.Join(root, ProfileFilename)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode = %v, want regular 0600", info.Mode())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cloud_provider", "paid_infrastructure_authorized"} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("current profile contains forbidden field %q: %s", forbidden, contents)
		}
	}
}

func TestUpdatePersistsOperationsBudgetAcrossRestart(t *testing.T) {
	root := t.TempDir()
	clock := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	store, err := Open(root, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	updated, err := store.Update(1, Input{
		MonthlyInfrastructureOperationsBudgetUSD: 750,
		DecisionStatus:                           StatusOperatorConfirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.MonthlyInfrastructureOperationsBudgetUSD != 750 || updated.DecisionStatus != StatusOperatorConfirmed {
		t.Fatalf("unexpected updated profile: %+v", updated)
	}
	reopened, err := Open(root, func() time.Time { return clock.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get(); got != updated {
		t.Fatalf("reopened profile = %+v, want %+v", got, updated)
	}
	if _, err := reopened.Update(1, Input{MonthlyInfrastructureOperationsBudgetUSD: 1_000, DecisionStatus: StatusAssumed}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v, want revision conflict", err)
	}
}

func TestOpenMigratesLegacyCloudProfileToSelfManagedSchema(t *testing.T) {
	root := t.TempDir()
	legacy := `{"schema_version":1,"profile":{"cloud_provider":"aws","monthly_staging_budget_usd":1000,"paid_infrastructure_authorized":true,"decision_status":"operator_confirmed","revision":4,"created_at":"2026-08-08T12:00:00Z","updated_at":"2026-08-09T12:00:00Z"}}`
	path := filepath.Join(root, ProfileFilename)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	profile := store.Get()
	if profile.MonthlyInfrastructureOperationsBudgetUSD != 1_000 || profile.DecisionStatus != StatusOperatorConfirmed || profile.Revision != 4 {
		t.Fatalf("migrated profile = %+v", profile)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"schema_version": 2`) {
		t.Fatalf("migration did not persist schema 2: %s", contents)
	}
	for _, forbidden := range []string{"cloud_provider", "monthly_staging_budget_usd", "paid_infrastructure_authorized"} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("migration retained forbidden field %q: %s", forbidden, contents)
		}
	}
}

func TestUpdateRejectsInvalidBudgetOrStatus(t *testing.T) {
	store, err := Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]Input{
		"unknown decision status": {MonthlyInfrastructureOperationsBudgetUSD: 1_000, DecisionStatus: "approved"},
		"negative budget":         {MonthlyInfrastructureOperationsBudgetUSD: -1, DecisionStatus: StatusAssumed},
		"excessive budget":        {MonthlyInfrastructureOperationsBudgetUSD: 1_000_001, DecisionStatus: StatusAssumed},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Update(1, input); !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v, want validation", err)
			}
			if got := store.Get(); got.Revision != 1 || got.MonthlyInfrastructureOperationsBudgetUSD != 1_000 {
				t.Fatalf("invalid update mutated profile: %+v", got)
			}
		})
	}
}

func TestOpenRejectsMalformedUnknownSymlinkAndPermissiveStorage(t *testing.T) {
	for name, contents := range map[string]string{
		"malformed":     `{`,
		"unknown field": `{"schema_version":2,"profile":{"monthly_infrastructure_operations_budget_usd":1000,"decision_status":"assumed","revision":1,"created_at":"2026-08-09T12:00:00Z","updated_at":"2026-08-09T12:00:00Z","extra":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ProfileFilename), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(root, time.Now); !errors.Is(err, ErrStorage) {
				t.Fatalf("error = %v, want storage error", err)
			}
		})
	}

	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 8)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ProfileFilename)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, time.Now); !errors.Is(err, ErrStorage) {
		t.Fatalf("symlink error = %v, want storage error", err)
	}

	permissiveRoot := t.TempDir()
	permissive := `{"schema_version":2,"profile":{"monthly_infrastructure_operations_budget_usd":1000,"decision_status":"assumed","revision":1,"created_at":"2026-08-09T12:00:00Z","updated_at":"2026-08-09T12:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(permissiveRoot, ProfileFilename), []byte(permissive), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(permissiveRoot, time.Now); !errors.Is(err, ErrStorage) {
		t.Fatalf("permissive mode error = %v, want storage error", err)
	}
}
