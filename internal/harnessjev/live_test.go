package harnessjev

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

// live runs one synthetic decision of each opaque kind against the real service, using the
// credential in the private store. It spends a few tokens and sends only fixed vocabulary
// and made-up numbers, never any project or run content.
func live(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_MEMORY_LIVE_JEV_TEST") != "1" {
		t.Skip("set AGENT_MEMORY_LIVE_JEV_TEST=1 for a few synthetic decisions against TypeSafe")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	token, configured, err := jevconfig.NewTokenStore(filepath.Join(home, ".agent-memory")).Load(t.Context())
	if err != nil || !configured {
		t.Fatalf("TypeSafe credential unavailable: configured=%v err=%v", configured, err)
	}
	p, err := NewProvider(Config{Client: jev.NewClient(), Token: func(context.Context) (string, error) { return token, nil }, AllowEgress: true})
	if err != nil {
		t.Fatal(err)
	}
	session := openSession(t, p)
	if session.State(Capability) != "available" {
		t.Fatalf("live access = %s", session.State(Capability))
	}
	s, err := harnessdecide.New(harnessdecide.Config{Asker: session, Capability: Capability, Enabled: harnessdecide.Kinds(), RunBudget: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range kindCalls() {
		spec, _ := harnessdecide.SpecOf(call.kind)
		if spec.Egress == harnessdecide.ClassInternal {
			continue
		}
		got, out := call.run(s, "synthetic")
		t.Logf("%s: %s confidence %.2f latency %v advice %q", call.kind, out.Status, out.Confidence, out.Latency, got)
		if out.Status != harnessdecide.StatusApplied && out.Status != harnessdecide.StatusLowConfidence {
			t.Errorf("%s: %+v", call.kind, out)
		}
	}
}
