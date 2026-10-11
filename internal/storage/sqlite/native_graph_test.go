package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestNativeGraphPublicationPreservesChangesAfterSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openGraphControlStore(t)
	cfg := graphConfigurationFixture()
	if err := s.UpsertGraphConfiguration(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	write := func(id string) {
		t.Helper()
		if err := s.UpsertMemory(ctx, &core.MemoryEntry{ID: id, Workspace: cfg.Scope.WorkspaceID, Type: core.SemanticMemory, Content: "canonical text " + id, Confidence: 0.9, StorageTier: core.TierVector}); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	work, err := s.LoadNativeGraphWork(ctx, cfg.Scope, cfg.ID, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.JournalIDs) != 1 || len(work.Memories) != 1 {
		t.Fatalf("snapshot=%+v", work)
	}
	write("later")
	now := time.Now().UTC()
	r := graphRevisionFixture("native-ready", core.GraphRevisionReady)
	if err := s.CreateGraphRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	j := graphJobFixture("native-job", r.ID, "native-key")
	expires := now.Add(time.Minute)
	j.State = core.GraphJobRunning
	j.LeaseOwner = "owner"
	j.LeaseExpiresAt = &expires
	if _, _, err := s.EnqueueGraphJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishNativeGraphJob(ctx, j, cfg.Version, "", work.JournalIDs, now); err != nil {
		t.Fatal(err)
	}
	status, err := s.GraphIndexStatus(ctx, cfg.Scope, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveRevisionID != r.ID || status.PendingChanges != 1 || status.LastJobState != core.GraphJobCompleted {
		t.Fatalf("status=%+v", status)
	}
	cfg.Version++
	if err = s.UpsertGraphConfiguration(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	changed, err := s.LoadNativeGraphWork(ctx, cfg.Scope, cfg.ID, 5000)
	if err != nil || !changed.Rebuild {
		t.Fatalf("configuration change did not force rebuild: %+v %v", changed, err)
	}
	snapshot, err := s.LoadActiveGraphSnapshot(ctx, cfg.Scope, 32, 32, 0)
	if err != nil || snapshot.Fresh {
		t.Fatalf("old configuration advertised fresh: %+v %v", snapshot, err)
	}
}

func TestNativeGraphPublicationRejectsDisabledOrLostLease(t *testing.T) {
	for _, scenario := range []string{"disabled", "version_changed", "lease_lost", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s := openGraphControlStore(t)
			cfg := graphConfigurationFixture()
			if err := s.UpsertGraphConfiguration(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			r := graphRevisionFixture("native-ready", core.GraphRevisionReady)
			if err := s.CreateGraphRevision(ctx, r); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			j := graphJobFixture("native-job", r.ID, "native-key")
			expires := now.Add(time.Minute)
			j.State = core.GraphJobRunning
			j.LeaseOwner = "owner"
			j.LeaseExpiresAt = &expires
			if scenario == "expired" {
				expires = now.Add(-time.Second)
			}
			if _, _, err := s.EnqueueGraphJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "disabled":
				cfg.Enabled = false
				if err := s.UpsertGraphConfiguration(ctx, cfg); err != nil {
					t.Fatal(err)
				}
			case "version_changed":
				cfg.Version++
				if err := s.UpsertGraphConfiguration(ctx, cfg); err != nil {
					t.Fatal(err)
				}
			case "lease_lost":
				if _, err := s.db.ExecContext(ctx, "UPDATE graph_jobs SET lease_owner='other'"); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if err := s.CancelGraphJob(ctx, j.Scope, j.ID, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.PublishNativeGraphJob(ctx, j, 1, "", nil, now); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			active, _, err := s.ActiveGraphRevisions(ctx, cfg.Scope, cfg.ID)
			if err != nil || active != "" {
				t.Fatalf("active=%q err=%v", active, err)
			}
		})
	}
}

func TestNativeGraphReviewedPromptUpgradeIncrementsConfigurationVersion(t *testing.T) {
	s := openGraphControlStore(t)
	ctx := context.Background()
	cfg := graphConfigurationFixture()
	cfg.PromptFingerprint = "sha256:35635c8c6d1aad0005484e2b0a9f3c8424d173bdf682e49e43ccfeefc89e3f99"
	if err := s.UpsertGraphConfiguration(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	current := "sha256:54293829a35e2f911cbd85bcdfc5733423c3f7dd12327869f22fc6daee6104fd"
	if err := s.AlignNativeGraphPrompt(ctx, cfg.Scope, cfg.ID, current, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	work, err := s.LoadNativeGraphWork(ctx, cfg.Scope, cfg.ID, 100)
	if err != nil || work.Configuration.Version != 2 || work.Configuration.PromptFingerprint != current {
		t.Fatalf("reviewed prompt not upgraded: %+v %v", work.Configuration, err)
	}
	if err = s.AlignNativeGraphPrompt(ctx, cfg.Scope, cfg.ID, current, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	work, err = s.LoadNativeGraphWork(ctx, cfg.Scope, cfg.ID, 100)
	if err != nil || work.Configuration.Version != 2 {
		t.Fatalf("no-op alignment changed version: %+v %v", work.Configuration, err)
	}
}
