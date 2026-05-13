package embeddings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureModelFiles(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("onnx"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tokenizer: %v", err)
	}
	if err := EnsureModelFiles(modelDir, nil); err != nil {
		t.Fatalf("ensure model files: %v", err)
	}
}

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "model.onnx")
	if err := DownloadFile(srv.URL, dst); err != nil {
		t.Fatalf("download file: %v", err)
	}

	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected body: %q", string(b))
	}
}

func TestEnsureMiniLMModelAutoDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "model.onnx":
			_, _ = w.Write([]byte("onnx"))
		case "tokenizer.json":
			_, _ = w.Write([]byte("{}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	err := EnsureMiniLMModel(modelDir, ModelLifecycleOptions{
		AutoDownload: true,
		URLs: map[string]string{
			"model.onnx":     srv.URL + "/model.onnx",
			"tokenizer.json": srv.URL + "/tokenizer.json",
		},
	})
	if err != nil {
		t.Fatalf("ensure minilm model with download: %v", err)
	}
	if err := EnsureModelFiles(modelDir, MiniLMFiles); err != nil {
		t.Fatalf("expected downloaded model files to validate: %v", err)
	}
}
