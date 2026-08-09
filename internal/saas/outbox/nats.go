package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
)

type NATSBroker struct {
	connection *nats.Conn
	jetstream  nats.JetStreamContext
}

func NewNATSBroker(queueURL string) (*NATSBroker, error) {
	connection, err := nats.Connect(queueURL, nats.Name("agent-memory-outbox"), nats.Timeout(5*time.Second), nats.RetryOnFailedConnect(false))
	if err != nil {
		return nil, err
	}
	jetstream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, err
	}
	if _, err = jetstream.StreamInfo("AGENT_MEMORY_EVENTS"); err != nil {
		if !errors.Is(err, nats.ErrStreamNotFound) {
			connection.Close()
			return nil, err
		}
		_, err = jetstream.AddStream(&nats.StreamConfig{
			Name: "AGENT_MEMORY_EVENTS", Subjects: []string{"agent_memory.v1.>"}, Storage: nats.FileStorage,
			Retention: nats.LimitsPolicy, Discard: nats.DiscardOld, MaxAge: 30 * 24 * time.Hour, Duplicates: 2 * time.Minute,
		})
		if err != nil {
			connection.Close()
			return nil, err
		}
	}
	return &NATSBroker{connection: connection, jetstream: jetstream}, nil
}
func (b *NATSBroker) Publish(ctx context.Context, subject string, body []byte) error {
	if b == nil || b.connection == nil || b.jetstream == nil {
		return errors.New("NATS broker is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	message := nats.NewMsg(subject)
	message.Data = body
	var identity struct {
		EventID string `json:"event_id"`
	}
	if json.Unmarshal(body, &identity) == nil && identity.EventID != "" {
		message.Header.Set(nats.MsgIdHdr, identity.EventID)
	}
	_, err := b.jetstream.PublishMsg(message, nats.Context(ctx))
	return err
}
func (b *NATSBroker) Close() {
	if b != nil && b.connection != nil {
		b.connection.Close()
	}
}
