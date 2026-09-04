package outbox

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGraphOutboxEventsAreDeterministicContentFreeAndConfigurationScoped(t *testing.T) {
	input := GraphChangeInput{
		TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), SubjectKind: "memory", SubjectID: uuid.NewString(),
		SubjectFingerprint: "sha256:subject", ChangeKind: "create", OccurredAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
	}
	configurations := []GraphEventConfiguration{
		{ID: uuid.NewString(), Version: 2, ProjectionVersion: "projection-v2"},
		{ID: uuid.NewString(), Version: 1, ProjectionVersion: "projection-v1"},
	}
	first, err := BuildGraphOutboxEvents(input, configurations)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildGraphOutboxEvents(input, configurations)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != second[0].ID || first[1].ID != second[1].ID || first[0].ID == first[1].ID {
		t.Fatalf("events are not deterministic and configuration-scoped: first=%+v second=%+v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret memory content") {
		t.Fatal("canonical content leaked into graph outbox")
	}
}

func TestGraphOutboxRejectsIncompleteIdentity(t *testing.T) {
	_, err := BuildGraphOutboxEvents(GraphChangeInput{}, []GraphEventConfiguration{{ID: uuid.NewString(), Version: 1, ProjectionVersion: "v1"}})
	if err == nil {
		t.Fatal("incomplete graph change unexpectedly accepted")
	}
}
