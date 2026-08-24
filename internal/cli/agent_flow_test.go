package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
)

func TestCLIAsAgentDeterministicEnvelopeFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent-flow.db")
	version := runCLIJSON(t, "version", "--format", "json")
	if version["command"] != "version" || version["version"] != envelopeMajor {
		t.Fatalf("unexpected version envelope: %+v", version)
	}

	write := runCLIJSON(t,
		"write",
		"--db", dbPath,
		"--workspace", "ws",
		"--type", "semantic",
		"--content", "service emits order.created",
		"--keyword", "orders",
		"--keyword", "order.created",
		"--format", "json",
	)
	if write["command"] != "write" || write["version"] != envelopeMajor {
		t.Fatalf("unexpected write envelope: %+v", write)
	}

	search := runCLIJSON(t,
		"search",
		"--db", dbPath,
		"--workspace", "ws",
		"--query", "order event",
		"--top-k", "3",
		"--format", "json",
	)
	if search["command"] != "search" || search["version"] != envelopeMajor {
		t.Fatalf("unexpected search envelope: %+v", search)
	}

	termSearch := runCLIJSON(t,
		"search",
		"--db", dbPath,
		"--workspace", "ws",
		"--query", "ORDERS order.created",
		"--mode", "terms",
		"--operator", "and",
		"--format", "json",
	)
	termData, _ := termSearch["data"].(map[string]any)
	termHits, _ := termData["hits"].([]any)
	if termData["strategy"] != "exact_terms" || len(termHits) != 1 {
		t.Fatalf("unexpected term search envelope: %+v", termSearch)
	}

	stats := runCLIJSON(t,
		"stats",
		"--db", dbPath,
		"--workspace", "ws",
		"--format", "json",
	)
	if stats["command"] != "stats" || stats["version"] != envelopeMajor {
		t.Fatalf("unexpected stats envelope: %+v", stats)
	}
}

func runCLIJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json envelope for %v: %v raw=%q", args, err, out.String())
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true envelope for %v, got %+v", args, payload)
	}
	if _, ok := payload["data"]; !ok {
		t.Fatalf("expected data field for %v", args)
	}
	return payload
}
