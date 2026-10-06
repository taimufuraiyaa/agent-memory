package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSkillOrchestratorLeaderLeaseContentionRenewalReleaseAndExpiry(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	now := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	fence, acquired, err := store.AcquireSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-a", time.Minute, now)
	if err != nil || !acquired || fence != 1 {
		t.Fatalf("first acquisition fence=%d acquired=%v err=%v", fence, acquired, err)
	}
	if _, acquired, err := store.AcquireSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-b", time.Minute, now); err != nil || acquired {
		t.Fatalf("contended acquisition acquired=%v err=%v", acquired, err)
	}
	if err := store.RenewSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-a", fence, time.Minute, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-b", fence, time.Minute, now.Add(10*time.Second)); !errors.Is(err, ErrSkillOrchestratorStaleLease) {
		t.Fatalf("stale renewal = %v", err)
	}
	if err := store.ReleaseSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-a", fence, now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	fence, acquired, err = store.AcquireSkillOrchestratorLeader(context.Background(), "installation", "database", "owner-b", time.Minute, now.Add(20*time.Second))
	if err != nil || !acquired || fence != 2 {
		t.Fatalf("post-release acquisition fence=%d acquired=%v err=%v", fence, acquired, err)
	}

	fence, acquired, err = store.AcquireSkillOrchestratorLeader(context.Background(), "installation-2", "database", "owner-a", time.Second, now)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	next, acquired, err := store.AcquireSkillOrchestratorLeader(context.Background(), "installation-2", "database", "owner-b", time.Second, now.Add(2*time.Second))
	if err != nil || !acquired || next != fence+1 {
		t.Fatalf("expired takeover fence=%d acquired=%v err=%v", next, acquired, err)
	}
}
