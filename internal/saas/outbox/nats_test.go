package outbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNATSBrokerPersistsPublishedEventInJetStream(t *testing.T) {
	queueURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_QUEUE_URL"))
	if queueURL == "" {
		t.Skip("AGENT_MEMORY_TEST_QUEUE_URL is not configured")
	}
	broker, err := NewNATSBroker(queueURL)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	before, err := broker.jetstream.StreamInfo("AGENT_MEMORY_EVENTS")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := []byte(`{"event_id":"` + uuid.NewString() + `"}`)
	if err := broker.Publish(ctx, "agent_memory.v1.test.persisted", body); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, "agent_memory.v1.test.persisted", body); err != nil {
		t.Fatal(err)
	}
	after, err := broker.jetstream.StreamInfo("AGENT_MEMORY_EVENTS")
	if err != nil {
		t.Fatal(err)
	}
	if after.State.Msgs != before.State.Msgs+1 {
		t.Fatalf("stream messages before=%d after=%d", before.State.Msgs, after.State.Msgs)
	}
}
