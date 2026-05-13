package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMemoryEntryJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	sessionID := "sess-1"
	entry := MemoryEntry{
		ID:             "m_001",
		Type:           SemanticMemory,
		Content:        "OPS listens on orders.events",
		Workspace:      "ws",
		SessionID:      &sessionID,
		Source:         MemorySource{Type: SourceCodeAnalysis, FilePath: "src/main.go", LineRange: []int{10, 20}},
		Entities:       []string{"OPS", "orders.events"},
		Tags:           []string{"kafka"},
		Confidence:     0.9,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
		StorageTier:    TierVector,
	}

	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded MemoryEntry
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ID != entry.ID || decoded.Type != entry.Type || decoded.Workspace != entry.Workspace {
		t.Fatalf("decoded values mismatch: %+v", decoded)
	}
	if decoded.SessionID == nil || *decoded.SessionID != sessionID {
		t.Fatalf("session pointer semantics lost")
	}
	if len(decoded.Source.LineRange) != 2 || decoded.Source.LineRange[0] != 10 || decoded.Source.LineRange[1] != 20 {
		t.Fatalf("line range mismatch: %+v", decoded.Source.LineRange)
	}
}

func TestMemoryEntryValidate(t *testing.T) {
	valid := &MemoryEntry{
		ID:          "m_001",
		Type:        EpisodicMemory,
		Content:     "valid",
		Workspace:   "ws",
		Confidence:  0.5,
		StorageTier: TierVector,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid entry, got err: %v", err)
	}

	invalid := []MemoryEntry{
		{ID: "", Type: EpisodicMemory, Content: "x", Workspace: "ws", Confidence: 0.5},
		{ID: "id", Type: "bad", Content: "x", Workspace: "ws", Confidence: 0.5},
		{ID: "id", Type: EpisodicMemory, Content: "", Workspace: "ws", Confidence: 0.5},
		{ID: "id", Type: EpisodicMemory, Content: "x", Workspace: "", Confidence: 0.5},
		{ID: "id", Type: EpisodicMemory, Content: "x", Workspace: "ws", Confidence: 1.5},
		{ID: "id", Type: EpisodicMemory, Content: "x", Workspace: "ws", Confidence: 0.5, StorageTier: "unknown"},
	}
	for i := range invalid {
		if err := invalid[i].Validate(); err == nil {
			t.Fatalf("case %d expected validation error", i)
		}
	}
}
