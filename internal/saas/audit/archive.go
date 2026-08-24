package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ArchiveStore interface {
	PutImmutable(ctx context.Context, key string, value []byte, checksum string) error
	Get(ctx context.Context, key string) ([]byte, error)
}

type ArchiveRepository interface {
	ClaimArchive(ctx context.Context, tenantID string, limit int, now time.Time) ([]ArchiveRecord, error)
	MarkArchived(ctx context.Context, record ArchiveRecord, key, checksum string, at time.Time) error
	MarkArchiveFailed(ctx context.Context, record ArchiveRecord, code string, at time.Time) error
}

type TenantLister interface {
	ActiveTenantIDs(ctx context.Context) ([]string, error)
}

type Archiver struct {
	repository ArchiveRepository
	store      ArchiveStore
	now        func() time.Time
}

func NewArchiver(repository ArchiveRepository, store ArchiveStore, now func() time.Time) *Archiver {
	return &Archiver{repository: repository, store: store, now: now}
}

func (a *Archiver) RunOnce(ctx context.Context, tenantID string, limit int) (int, error) {
	if a == nil || a.repository == nil || a.store == nil || a.now == nil {
		return 0, errors.New("audit archiver is not configured")
	}
	now := a.now().UTC()
	records, err := a.repository.ClaimArchive(ctx, tenantID, limit, now)
	if err != nil {
		return 0, err
	}
	archived := 0
	for _, record := range records {
		value, archiveErr := record.Event.JSON()
		checksum := SHA256(value)
		key := ArchiveKey(record.Event)
		if archiveErr == nil {
			archiveErr = a.store.PutImmutable(ctx, key, value, checksum)
		}
		if archiveErr == nil {
			archiveErr = VerifyArchive(value, checksum, record.Event.EventHash)
		}
		if archiveErr != nil {
			_ = a.repository.MarkArchiveFailed(ctx, record, safeArchiveError(archiveErr), now)
			continue
		}
		if err := a.repository.MarkArchived(ctx, record, key, checksum, now); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}

func (a *Archiver) Run(ctx context.Context, tenants TenantLister, interval time.Duration, report func(int, error)) {
	if interval <= 0 {
		interval = time.Minute
	}
	if report == nil {
		report = func(int, error) {}
	}
	run := func() {
		ids, err := tenants.ActiveTenantIDs(ctx)
		if err != nil {
			report(0, err)
			return
		}
		total := 0
		for _, tenantID := range ids {
			count, err := a.RunOnce(ctx, tenantID, 200)
			total += count
			if err != nil {
				report(total, err)
				return
			}
		}
		report(total, nil)
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func ArchiveKey(event Event) string {
	day := event.OccurredAt.UTC().Format("2006/01/02")
	return fmt.Sprintf("audit/%s/%s/%s-%s.json", event.TenantID, day, event.ID, event.EventHash)
}

func SHA256(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func VerifyArchive(value []byte, expectedChecksum, expectedEventHash string) error {
	if SHA256(value) != expectedChecksum {
		return errors.New("audit archive checksum mismatch")
	}
	var event Event
	if err := json.Unmarshal(value, &event); err != nil {
		return errors.New("invalid audit archive envelope")
	}
	if event.EventHash == "" || event.EventHash != expectedEventHash {
		return errors.New("audit archive event hash mismatch")
	}
	return nil
}

func safeArchiveError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "checksum") || strings.Contains(message, "hash"):
		return "integrity_failed"
	case strings.Contains(message, "exist"):
		return "immutable_conflict"
	default:
		return "archive_unavailable"
	}
}
