package embeddings

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// MiniLMFiles are the minimum expected local assets for ONNX inference.
var MiniLMFiles = []string{
	"model.onnx",
	"tokenizer.json",
}

// ModelLifecycleOptions controls strict checks and optional auto-download.
type ModelLifecycleOptions struct {
	AutoDownload bool
	URLs         map[string]string
}

// EnsureModelFiles checks that required model files exist.
func EnsureModelFiles(modelDir string, requiredFiles []string) error {
	if modelDir == "" {
		return errors.New("model dir is required")
	}
	if len(requiredFiles) == 0 {
		requiredFiles = MiniLMFiles
	}
	for _, name := range requiredFiles {
		path := filepath.Join(modelDir, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("required model file missing: %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("required model file is directory: %s", path)
		}
	}
	return nil
}

// EnsureMiniLMModel validates required files and optionally downloads missing assets.
func EnsureMiniLMModel(modelDir string, opt ModelLifecycleOptions) error {
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return fmt.Errorf("mkdir model dir: %w", err)
	}
	if err := EnsureModelFiles(modelDir, MiniLMFiles); err == nil {
		return nil
	} else if !opt.AutoDownload {
		return err
	}
	for _, name := range MiniLMFiles {
		path := filepath.Join(modelDir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		url := ""
		if opt.URLs != nil {
			url = opt.URLs[name]
		}
		if url == "" {
			return fmt.Errorf("missing download URL for %s", name)
		}
		if err := DownloadFile(url, path); err != nil {
			return fmt.Errorf("download %s: %w", name, err)
		}
	}
	return EnsureModelFiles(modelDir, MiniLMFiles)
}

// DownloadFile downloads URL content to destination path.
func DownloadFile(url, dstPath string) error {
	if url == "" || dstPath == "" {
		return errors.New("url and dstPath are required")
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	f, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}
	return nil
}
