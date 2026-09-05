package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestCursorAndKiroAdaptersAreIdempotentAndReversible(t *testing.T) {
	for _, adapter := range []Adapter{NewCursorAdapter(), NewKiroAdapter()} {
		t.Run(adapter.Name(), func(t *testing.T) {
			root := t.TempDir()
			options := Options{Root: root, DataDir: t.TempDir(), Workspace: "demo"}
			for range 2 {
				result, err := adapter.Connect(context.Background(), options)
				if err != nil || !result.Verified {
					t.Fatalf("connect: result=%+v err=%v", result, err)
				}
				want := 1
				if adapter.Name() == "kiro" {
					want = 2
				}
				if len(result.Applied) != want {
					t.Fatalf("%s connection touched unrelated client artifacts: %+v", adapter.Name(), result.Applied)
				}
			}
			result, err := adapter.Disconnect(context.Background(), options)
			if err != nil || !result.Verified {
				t.Fatalf("disconnect: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestKiroAdapterWritesCurrentContractToBothHooks(t *testing.T) {
	root := t.TempDir()
	adapter := NewKiroAdapter()
	if _, err := adapter.Connect(context.Background(), Options{Root: root, DataDir: t.TempDir(), Workspace: "demo"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory-recall-gate.json", "memory-consolidation-gate.json"} {
		content, err := os.ReadFile(filepath.Join(root, ".kiro", "hooks", name))
		if err != nil || !strings.Contains(string(content), workspace.MemoryContractMarker) || !strings.Contains(string(content), "--workspace demo") {
			t.Fatalf("%s missing current contract: %v %s", name, err, content)
		}
	}
}
