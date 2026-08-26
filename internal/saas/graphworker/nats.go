package graphworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	graphStreamName        = "AGENT_MEMORY_GRAPH"
	graphJobSubjectPrefix  = "agent_memory.graph.jobs."
	graphDoneSubjectPrefix = "agent_memory.graph.completions."
)

// NATSTransport provides durable content-free graph job and completion
// delivery. Object content remains in object custody and never enters NATS.
type NATSTransport struct {
	connection *nats.Conn
	jetstream  nats.JetStreamContext
	jobs       *nats.Subscription
	mu         sync.Mutex
	inflight   map[string]*nats.Msg
}

func NewNATSTransport(queueURL, consumerName string) (*NATSTransport, error) {
	if strings.TrimSpace(queueURL) == "" || strings.TrimSpace(consumerName) == "" {
		return nil, fmt.Errorf("graph NATS URL and consumer name are required")
	}
	connection, err := nats.Connect(queueURL, nats.Name(consumerName), nats.Timeout(5*time.Second), nats.RetryOnFailedConnect(false))
	if err != nil {
		return nil, err
	}
	jetstream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, err
	}
	if err := ensureGraphStream(jetstream); err != nil {
		connection.Close()
		return nil, err
	}
	jobs, err := jetstream.PullSubscribe(graphJobSubjectPrefix+">", consumerName+"-jobs", nats.BindStream(graphStreamName), nats.ManualAck(), nats.AckExplicit(), nats.MaxDeliver(6), nats.AckWait(6*time.Hour))
	if err != nil {
		connection.Close()
		return nil, err
	}
	return &NATSTransport{connection: connection, jetstream: jetstream, jobs: jobs, inflight: map[string]*nats.Msg{}}, nil
}

func ensureGraphStream(jetstream nats.JetStreamContext) error {
	if _, err := jetstream.StreamInfo(graphStreamName); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}
	_, err := jetstream.AddStream(&nats.StreamConfig{
		Name: graphStreamName, Subjects: []string{graphJobSubjectPrefix + ">", graphDoneSubjectPrefix + ">"},
		Storage: nats.FileStorage, Retention: nats.LimitsPolicy, Discard: nats.DiscardOld,
		MaxAge: 30 * 24 * time.Hour, Duplicates: 10 * time.Minute,
	})
	return err
}

func (t *NATSTransport) PublishJob(ctx context.Context, job JobEnvelope) (bool, error) {
	if t == nil || t.jetstream == nil {
		return false, fmt.Errorf("graph NATS transport is required")
	}
	if err := validateJob(job); err != nil {
		return false, err
	}
	body, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	message := nats.NewMsg(graphJobSubjectPrefix + job.Scope.TenantID + "." + job.Scope.WorkspaceID)
	message.Data = body
	message.Header.Set(nats.MsgIdHdr, graphJobIdentity(job))
	ack, err := t.jetstream.PublishMsg(message, nats.Context(ctx))
	return ack != nil && ack.Duplicate, err
}

func (t *NATSTransport) Claim(ctx context.Context, _ string, limit int, _ time.Duration, _ time.Time) ([]JobEnvelope, error) {
	if t == nil || t.jobs == nil || limit < 1 || limit > 32 {
		return nil, fmt.Errorf("graph NATS claim is invalid")
	}
	messages, err := t.jobs.Fetch(limit, nats.Context(ctx), nats.MaxWait(500*time.Millisecond))
	if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := make([]JobEnvelope, 0, len(messages))
	for _, message := range messages {
		var job JobEnvelope
		if err := strictGraphJSON(message.Data, &job); err != nil || validateJob(job) != nil || message.Subject != graphJobSubjectPrefix+job.Scope.TenantID+"."+job.Scope.WorkspaceID {
			_ = message.Term()
			continue
		}
		key := graphJobIdentity(job)
		t.mu.Lock()
		t.inflight[key] = message
		t.mu.Unlock()
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (t *NATSTransport) Ack(_ context.Context, job JobEnvelope) error {
	message := t.take(job)
	if message == nil {
		return fmt.Errorf("graph job is not inflight")
	}
	return message.AckSync()
}

func (t *NATSTransport) Release(_ context.Context, job JobEnvelope, _ string) error {
	message := t.take(job)
	if message == nil {
		return fmt.Errorf("graph job is not inflight")
	}
	delay := time.Duration(1<<min(job.Attempt, 6)) * time.Second
	return message.NakWithDelay(delay)
}

func (t *NATSTransport) Emit(ctx context.Context, event CompletionEvent) (bool, error) {
	if t == nil || t.jetstream == nil {
		return false, fmt.Errorf("graph NATS transport is required")
	}
	if err := validateCompletion(event); err != nil {
		return false, err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return false, err
	}
	message := nats.NewMsg(graphDoneSubjectPrefix + event.Scope.TenantID + "." + event.Scope.WorkspaceID)
	message.Data = body
	message.Header.Set(nats.MsgIdHdr, event.ID)
	ack, err := t.jetstream.PublishMsg(message, nats.Context(ctx))
	return ack != nil && ack.Duplicate, err
}

type CompletionHandler interface {
	HandleCompletion(context.Context, CompletionEvent) error
}

func (t *NATSTransport) RunCompletions(ctx context.Context, durable string, handler CompletionHandler, report func(error)) error {
	if t == nil || t.jetstream == nil || handler == nil || strings.TrimSpace(durable) == "" {
		return fmt.Errorf("graph completion consumer is incomplete")
	}
	subscription, err := t.jetstream.PullSubscribe(graphDoneSubjectPrefix+">", durable, nats.BindStream(graphStreamName), nats.ManualAck(), nats.AckExplicit(), nats.MaxDeliver(6), nats.AckWait(10*time.Minute))
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		messages, fetchErr := subscription.Fetch(8, nats.Context(ctx), nats.MaxWait(time.Second))
		if errors.Is(fetchErr, nats.ErrTimeout) || errors.Is(fetchErr, context.DeadlineExceeded) {
			continue
		}
		if fetchErr != nil {
			if report != nil {
				report(fetchErr)
			}
			continue
		}
		for _, message := range messages {
			var event CompletionEvent
			if decodeErr := strictGraphJSON(message.Data, &event); decodeErr != nil || validateCompletion(event) != nil || message.Subject != graphDoneSubjectPrefix+event.Scope.TenantID+"."+event.Scope.WorkspaceID {
				_ = message.Term()
				continue
			}
			if handleErr := handler.HandleCompletion(ctx, event); handleErr != nil {
				_ = message.NakWithDelay(5 * time.Second)
				if report != nil {
					report(handleErr)
				}
				continue
			}
			_ = message.AckSync()
		}
	}
	return ctx.Err()
}

func (t *NATSTransport) take(job JobEnvelope) *nats.Msg {
	if t == nil {
		return nil
	}
	key := graphJobIdentity(job)
	t.mu.Lock()
	defer t.mu.Unlock()
	message := t.inflight[key]
	delete(t.inflight, key)
	return message
}

func (t *NATSTransport) Close() {
	if t != nil && t.connection != nil {
		t.connection.Close()
	}
}

func graphJobIdentity(job JobEnvelope) string {
	return job.Scope.TenantID + "/" + job.Scope.WorkspaceID + "/" + job.JobID + "/" + job.RevisionID + fmt.Sprintf("/%d", job.Attempt)
}

func validateCompletion(event CompletionEvent) error {
	if err := event.Scope.Validate(); err != nil || event.Scope.TenantID == "" {
		return fmt.Errorf("hosted graph completion scope is invalid")
	}
	for _, value := range []string{event.ID, event.JobID, event.ConfigurationID, event.RevisionID} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "/\\") {
			return fmt.Errorf("hosted graph completion identity is invalid")
		}
	}
	if event.Status != "completed" && event.Status != "failed" {
		return fmt.Errorf("hosted graph completion status is invalid")
	}
	if event.Status == "completed" && (event.ArtifactPrefix == "" || event.FailureCode != "") {
		return fmt.Errorf("completed graph event is incomplete")
	}
	if event.Status == "failed" && (event.FailureCode == "" || event.ArtifactPrefix != "") {
		return fmt.Errorf("failed graph event is incomplete")
	}
	return nil
}

func strictGraphJSON(value []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple graph envelope values")
	} else if err != io.EOF {
		return err
	}
	return nil
}
