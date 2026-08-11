package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestEnsureOllamaPlannerReusesRuntimePullsAndVerifiesExactModel(t *testing.T) {
	var mu sync.Mutex
	pulled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"test"}`)
		case "/api/tags":
			mu.Lock()
			ready := pulled
			mu.Unlock()
			if ready {
				_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:8b"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:8b-extra"}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runs := []string{}
	result, err := EnsureOllamaPlanner(context.Background(), OllamaPlannerOptions{
		Endpoint: server.URL, Model: "qwen3:8b", DataDir: t.TempDir(),
		Lookup: func(name string) (string, error) {
			if name == "ollama" {
				return "/fake/ollama", nil
			}
			return "", fmt.Errorf("not found")
		},
		Run: func(_ context.Context, name string, args []string, _, _ io.Writer) error {
			runs = append(runs, name+" "+strings.Join(args, " "))
			mu.Lock()
			pulled = true
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RuntimeReused || !result.ModelPulled || !result.ModelAvailable || len(runs) != 1 || runs[0] != "/fake/ollama pull qwen3:8b" {
		t.Fatalf("result=%+v runs=%v", result, runs)
	}
}

func TestEnsureOllamaPlannerRejectsRedirectAndRemoteEndpoint(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect destination reached")
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	options := OllamaPlannerOptions{
		Endpoint: redirect.URL, Model: "qwen3:8b", DataDir: t.TempDir(),
		Lookup:       func(string) (string, error) { return "/fake/ollama", nil },
		Start:        func(string, []string, string) error { return nil },
		PollAttempts: 1,
	}
	if _, err := EnsureOllamaPlanner(context.Background(), options); err == nil {
		t.Fatal("redirect was accepted")
	}
	options.Endpoint = "http://example.com:11434"
	if _, err := EnsureOllamaPlanner(context.Background(), options); err == nil {
		t.Fatal("remote endpoint was accepted")
	}
}

func TestEnsureOllamaPlannerRejectsOversizedInventoryAndPreservesFailure(t *testing.T) {
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			_, _ = io.WriteString(w, `{"version":"test"}`)
			return
		}
		_, _ = io.WriteString(w, strings.Repeat(" ", maxOllamaResponse+1)+`{"models":[]}`)
	}))
	defer oversized.Close()
	base := OllamaPlannerOptions{
		Endpoint: oversized.URL, Model: "qwen3:8b", DataDir: t.TempDir(),
		Lookup: func(string) (string, error) { return "/fake/ollama", nil },
	}
	if result, err := EnsureOllamaPlanner(context.Background(), base); err == nil || result.ModelAvailable {
		t.Fatalf("oversized inventory result=%+v err=%v", result, err)
	}

	pullFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			_, _ = io.WriteString(w, `{"version":"test"}`)
			return
		}
		_, _ = io.WriteString(w, `{"models":[]}`)
	}))
	defer pullFailure.Close()
	base.Endpoint = pullFailure.URL
	base.Run = func(context.Context, string, []string, io.Writer, io.Writer) error { return fmt.Errorf("pull failed") }
	if result, err := EnsureOllamaPlanner(context.Background(), base); err == nil || result.ModelAvailable {
		t.Fatalf("pull failure result=%+v err=%v", result, err)
	}
}

func TestOllamaAvailableModelsReturnsExactRequestedInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"test"}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:4b"},{"model":"qwen3:8b-extra"},{"model":"qwen3:14b"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	available, err := OllamaAvailableModels(context.Background(), server.URL, []string{"qwen3:4b", "qwen3:8b", "qwen3:14b"})
	if err != nil {
		t.Fatal(err)
	}
	if !available["qwen3:4b"] || available["qwen3:8b"] || !available["qwen3:14b"] {
		t.Fatalf("available=%v", available)
	}
}

func TestOllamaRuntimeInstallPlanIsPlatformSpecific(t *testing.T) {
	lookup := func(name string) (string, error) { return "/tools/" + name, nil }
	for _, test := range []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "/tools/brew install ollama"},
		{goos: "windows", want: "/tools/winget install --id Ollama.Ollama -e --accept-package-agreements --accept-source-agreements"},
		{goos: "linux", want: "official-install-script"},
	} {
		plan, err := ollamaRuntimeInstallPlan(test.goos, lookup)
		if err != nil {
			t.Fatalf("%s: %v", test.goos, err)
		}
		if plan.String() != test.want {
			t.Fatalf("%s plan=%q want=%q", test.goos, plan.String(), test.want)
		}
	}
}
