package modelroute

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSeparatesClaudeAndChatGPTCandidates(t *testing.T) {
	root := t.TempDir()
	data := `{"version":1,"hosts":{"claude":[{"id":"claude-sonnet-5-5","description":"Balanced coding"},{"id":"claude-opus-5-5","description":"Complex coding"}],"chatgpt":[{"id":"gpt-6-luna","description":"Fast scoped tasks"},{"id":"gpt-6.1-sol","description":"Complex tasks"}],"openai_api":[{"id":"gpt-6-luna","description":"Fast API tasks"},{"id":"gpt-6.1-sol","description":"Complex API tasks"}]}}`
	if err := os.WriteFile(filepath.Join(root, "model-catalog.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := catalog.Candidates("claude")
	if err != nil || len(claude) != 2 || claude[0].ID != "claude-sonnet-5-5" {
		t.Fatalf("Claude candidates: %+v %v", claude, err)
	}
	chatgpt, err := catalog.Candidates("chatgpt")
	if err != nil || len(chatgpt) != 2 || chatgpt[0].ID != "gpt-6-luna" {
		t.Fatalf("ChatGPT candidates: %+v %v", chatgpt, err)
	}
	api, err := catalog.Candidates("openai_api")
	if err != nil || len(api) != 2 || api[0].ID != "gpt-6-luna" {
		t.Fatalf("OpenAI API candidates: %+v %v", api, err)
	}
	if _, err := catalog.Candidates("unknown"); err == nil {
		t.Fatal("unknown host accepted")
	}
}

func TestLoadRejectsUnsafeOrAmbiguousCatalog(t *testing.T) {
	for name, data := range map[string]string{
		"duplicate":     `{"version":1,"hosts":{"claude":[{"id":"same","description":"A"},{"id":"same","description":"B"}]}}`,
		"unknown_host":  `{"version":1,"hosts":{"other":[{"id":"a","description":"A"},{"id":"b","description":"B"}]}}`,
		"unknown_field": `{"version":1,"hosts":{},"token":"leak"}`,
		"bad_id":        `{"version":1,"hosts":{"claude":[{"id":"a\nsecret","description":"A"},{"id":"b","description":"B"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "model-catalog.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("unsafe catalog accepted")
			}
		})
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte(`{"version":1,"hosts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "model-catalog.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("symlink accepted")
	}
}
