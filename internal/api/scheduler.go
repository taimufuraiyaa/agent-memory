package api

import (
	"context"
	"time"
)

type SchedulerWorkspaceStatus struct {
	Workspace         string    `json:"workspace"`
	MemoryCount       int       `json:"memory_count"`
	LastActivityAt    time.Time `json:"last_activity_at,omitempty"`
	LastScheduledAt   time.Time `json:"last_scheduled_at,omitempty"`
	LastCompletedAt   time.Time `json:"last_completed_at,omitempty"`
	LastResult        string    `json:"last_result,omitempty"`
	LastSkipReason    string    `json:"last_skip_reason,omitempty"`
	LastDurationMS    int       `json:"last_duration_ms,omitempty"`
	LastImpacts       int       `json:"last_impacts,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	HygieneOverdue    bool      `json:"hygiene_overdue"`
	EligibleDaily     bool      `json:"eligible_daily"`
	CurrentSkipReason string    `json:"current_skip_reason,omitempty"`
	RunInProgress     bool      `json:"run_in_progress"`
}

type SchedulerStatus struct {
	Enabled    bool                       `json:"enabled"`
	StartedAt  time.Time                  `json:"started_at,omitempty"`
	LastTickAt time.Time                  `json:"last_tick_at,omitempty"`
	NextTickAt time.Time                  `json:"next_tick_at,omitempty"`
	Workspaces []SchedulerWorkspaceStatus `json:"workspaces"`
}

type SchedulerRun struct {
	ID             string    `json:"id"`
	Workspace      string    `json:"workspace"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	Trigger        string    `json:"trigger"`
	Result         string    `json:"result"`
	SkipReason     string    `json:"skip_reason,omitempty"`
	DurationMS     int       `json:"duration_ms,omitempty"`
	DecayUpdated   int       `json:"decay_updated,omitempty"`
	Consolidated   int       `json:"consolidated,omitempty"`
	ConflictsFound int       `json:"conflicts_found,omitempty"`
	Evicted        int       `json:"evicted,omitempty"`
	Promoted       int       `json:"promoted,omitempty"`
	Demoted        int       `json:"demoted,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type Scheduler interface {
	Status(ctx context.Context) (*SchedulerStatus, error)
	History(ctx context.Context, workspace string, limit int) ([]SchedulerRun, error)
	RunNow(ctx context.Context, workspace string, force bool) (*SchedulerRun, error)
}
