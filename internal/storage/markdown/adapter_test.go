package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdapterRoundTripNonManagedPreserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.md")
	initial := "# Notes\nKeep this section untouched.\n"
	if err := os.WriteFile(p, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := NewAdapter(p, 1000)
	if err := a.Upsert("m1", "first memory"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if !strings.Contains(got, "Keep this section untouched.") {
		t.Fatalf("non-managed content lost")
	}
	if !strings.Contains(got, "AGENT_MEMORY:START id=m1") {
		t.Fatalf("managed block missing")
	}
}

func TestAdapterUpsertAndRemove(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.md")
	a := NewAdapter(p, 1000)
	if err := a.Upsert("m1", "hello world"); err != nil {
		t.Fatalf("upsert #1: %v", err)
	}
	if err := a.Upsert("m1", "updated content"); err != nil {
		t.Fatalf("upsert #2: %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "AGENT_MEMORY:START id=m1") != 1 {
		t.Fatalf("expected one managed block")
	}
	if err := a.Remove("m1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after, _ := os.ReadFile(p)
	if strings.Contains(string(after), "AGENT_MEMORY:START id=m1") {
		t.Fatalf("expected block removed")
	}
}

func TestAdapterBudgetGuard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.md")
	a := NewAdapter(p, 3)
	err := a.Upsert("m1", "this has too many words for budget")
	if err == nil {
		t.Fatalf("expected budget error")
	}
}

