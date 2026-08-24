// Package connectors defines isolated, checkpointed observation producers.
package connectors

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/hooks"
)

type Health struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	CheckpointAge string    `json:"checkpoint_age,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type Emitter interface {
	// Emit returns only after the normalized observation is durably accepted.
	Emit(context.Context, hooks.Event) error
}

type CheckpointStore interface {
	Load(context.Context, string) (Checkpoint, error)
	Save(context.Context, Checkpoint) error
}

type Checkpoint struct {
	ConnectorID    string
	State          map[string]string
	UpdatedAt      time.Time
	LastError      string
	EmittedCount   int64
	CoalescedCount int64
	RescannedCount int64
}

type Connector interface {
	ID() string
	Validate() error
	Start(context.Context, Emitter, CheckpointStore) error
	Stop(context.Context) error
	Health() Health
}

type Manager struct {
	connectors []Connector
	mu         sync.RWMutex
	health     map[string]Health
}

func NewManager(items ...Connector) *Manager {
	return &Manager{connectors: items, health: map[string]Health{}}
}

func (m *Manager) Start(ctx context.Context, emitter Emitter, checkpoints CheckpointStore) map[string]error {
	errs := map[string]error{}
	for _, connector := range m.connectors {
		if err := connector.Validate(); err != nil {
			errs[connector.ID()] = fmt.Errorf("validate: %w", err)
			continue
		}
		if err := connector.Start(ctx, emitter, checkpoints); err != nil {
			errs[connector.ID()] = err
		}
	}
	return errs
}

func (m *Manager) Health() []Health {
	result := make([]Health, 0, len(m.connectors))
	for _, connector := range m.connectors {
		result = append(result, connector.Health())
	}
	return result
}
