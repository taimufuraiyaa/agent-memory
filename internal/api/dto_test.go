package api

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestWriteMemoryRequestValidate(t *testing.T) {
	ok := &WriteMemoryRequest{
		Content:   "OPS listens on orders.events",
		Type:      core.SemanticMemory,
		Workspace: "ws",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("expected valid request, got: %v", err)
	}

	bad := &WriteMemoryRequest{
		Content:   "",
		Type:      "invalid",
		Workspace: "",
	}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected invalid request error")
	}
}

func TestSearchRequestValidate(t *testing.T) {
	ok := &SearchRequest{
		Query:       "OPS",
		Workspace:   "ws",
		TopK:        5,
		TokenBudget: 2000,
		Types:       []core.MemoryType{core.SemanticMemory},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("expected valid request, got: %v", err)
	}

	bad := &SearchRequest{
		Query:       "",
		Workspace:   "",
		TopK:        0,
		TokenBudget: -1,
	}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected invalid request error")
	}
}
