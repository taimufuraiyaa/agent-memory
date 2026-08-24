package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type archiveRepoStub struct {
	records  []ArchiveRecord
	archived int
	failed   int
}

func (r *archiveRepoStub) ClaimArchive(context.Context, string, int, time.Time) ([]ArchiveRecord, error) {
	return r.records, nil
}
func (r *archiveRepoStub) MarkArchived(context.Context, ArchiveRecord, string, string, time.Time) error {
	r.archived++
	return nil
}
func (r *archiveRepoStub) MarkArchiveFailed(context.Context, ArchiveRecord, string, time.Time) error {
	r.failed++
	return nil
}

type archiveStoreStub struct {
	values map[string][]byte
	fail   bool
}

func (s *archiveStoreStub) PutImmutable(_ context.Context, key string, value []byte, _ string) error {
	if s.fail {
		return errors.New("storage unavailable")
	}
	if _, ok := s.values[key]; ok {
		return errors.New("exists")
	}
	s.values[key] = append([]byte(nil), value...)
	return nil
}
func (s *archiveStoreStub) Get(_ context.Context, key string) ([]byte, error) {
	return s.values[key], nil
}

func TestArchiverWritesContentAddressedImmutableEnvelope(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 2, 3, 0, time.UTC)
	event := Event{TenantID: "tenant", ID: "event", OccurredAt: now, EventHash: "event-hash", SafeMetadata: map[string]any{}}
	repo := &archiveRepoStub{records: []ArchiveRecord{{Event: event, ClaimToken: "claim"}}}
	store := &archiveStoreStub{values: map[string][]byte{}}
	count, err := NewArchiver(repo, store, func() time.Time { return now }).RunOnce(context.Background(), "tenant", 10)
	if err != nil || count != 1 || repo.archived != 1 || repo.failed != 0 {
		t.Fatalf("RunOnce count=%d archived=%d failed=%d err=%v", count, repo.archived, repo.failed, err)
	}
	key := ArchiveKey(event)
	original := store.values[key]
	if err := VerifyArchive(original, SHA256(original), event.EventHash); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), original...)
	tampered[0] ^= 1
	if err := VerifyArchive(tampered, SHA256(original), event.EventHash); err == nil {
		t.Fatal("tampered archive verified")
	}
}
