// Package outbox publishes committed business events with at-least-once delivery.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const MaxAttempts = 5

var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_.]+$`)

type Event struct {
	TenantID      string
	ID            string
	Type          string
	SpecVersion   string
	AggregateType string
	AggregateID   string
	OccurredAt    time.Time
	ClaimToken    string
	Attempts      int
}

type Envelope struct {
	SpecVersion   string         `json:"spec_version"`
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	OccurredAt    time.Time      `json:"occurred_at"`
	TenantID      string         `json:"tenant_id"`
	Actor         Actor          `json:"actor"`
	RequestID     string         `json:"request_id"`
	CorrelationID string         `json:"correlation_id"`
	Producer      string         `json:"producer"`
	Subject       Subject        `json:"subject"`
	Data          map[string]any `json:"data"`
}
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Subject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Repository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	Claim(context.Context, string, int, time.Duration, time.Time) ([]Event, error)
	MarkPublished(context.Context, Event, time.Time) error
	MarkFailed(context.Context, Event, string, time.Time) error
}
type Broker interface {
	Publish(context.Context, string, []byte) error
}

type Publisher struct {
	repository Repository
	broker     Broker
	now        func() time.Time
	batchSize  int
	lease      time.Duration
}

func NewPublisher(repository Repository, broker Broker, now func() time.Time) *Publisher {
	if now == nil {
		now = time.Now
	}
	return &Publisher{repository: repository, broker: broker, now: now, batchSize: 50, lease: 30 * time.Second}
}

func (p *Publisher) Run(ctx context.Context, pollInterval time.Duration, report func(error)) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	run := func() {
		_, err := p.RunOnce(ctx)
		if err != nil && report != nil {
			report(err)
		}
	}
	run()
	ticker := time.NewTicker(pollInterval)
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

func (p *Publisher) RunOnce(ctx context.Context) (int, error) {
	if p == nil || p.repository == nil || p.broker == nil {
		return 0, errors.New("outbox publisher is not configured")
	}
	tenants, err := p.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	published := 0
	var failures []error
	for _, tenantID := range tenants {
		events, err := p.repository.Claim(ctx, tenantID, p.batchSize, p.lease, p.now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, event := range events {
			if err := p.publish(ctx, event); err != nil {
				failures = append(failures, fmt.Errorf("publish outbox event %s: %w", event.ID, err))
				code := "publish_failed"
				if errors.Is(err, ErrInvalidEvent) {
					code = "invalid_event"
				}
				if markErr := p.repository.MarkFailed(ctx, event, code, p.now().UTC()); markErr != nil {
					failures = append(failures, markErr)
				}
				continue
			}
			if err := p.repository.MarkPublished(ctx, event, p.now().UTC()); err != nil {
				failures = append(failures, err)
				continue
			}
			published++
		}
	}
	return published, errors.Join(failures...)
}

var ErrInvalidEvent = errors.New("invalid outbox event")

func (p *Publisher) publish(ctx context.Context, event Event) error {
	envelope := Envelope{SpecVersion: event.SpecVersion, EventID: event.ID, EventType: event.Type, OccurredAt: event.OccurredAt.UTC(), TenantID: event.TenantID, Actor: Actor{Type: "system", ID: "outbox-publisher"}, RequestID: event.ID, CorrelationID: event.ID, Producer: "agent-memory-worker", Subject: Subject{Type: event.AggregateType, ID: event.AggregateID}, Data: map[string]any{}}
	if err := Validate(envelope); err != nil {
		return err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return p.broker.Publish(ctx, "agent_memory.v1."+event.Type, body)
}
func Validate(event Envelope) error {
	if event.SpecVersion != "1.0" || len(event.EventID) < 16 || len(event.EventID) > 128 || !eventTypePattern.MatchString(event.EventType) || len(event.EventType) > 128 || strings.TrimSpace(event.TenantID) == "" || event.OccurredAt.IsZero() || event.Actor.Type != "system" || event.Actor.ID == "" || event.RequestID == "" || event.CorrelationID == "" || event.Producer == "" || event.Subject.Type == "" || event.Subject.ID == "" || event.Data == nil || len(event.Data) != 0 {
		return fmt.Errorf("%w: required envelope field or content-free data is invalid", ErrInvalidEvent)
	}
	return nil
}
