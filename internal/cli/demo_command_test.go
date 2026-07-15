package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestDemoProvesWriteSearchRecallAndCleansUp(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("demo: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if data["written_count"].(float64) != 3 || data["search_hit_count"].(float64) < 1 || data["recall_context"] == "" {
		t.Fatalf("demo did not prove round trip: %+v", data)
	}
	path := data["path"].(string)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("demo path should be cleaned up: %s err=%v", path, err)
	}
}
