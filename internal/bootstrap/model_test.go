package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateModelFile(t *testing.T) {
	tests := []struct {
		name    string
		fname   string
		content string
		wantErr bool
	}{
		{
			name:    "valid json",
			fname:   "config.json",
			content: `{"key": "value"}`,
			wantErr: false,
		},
		{
			name:    "invalid json",
			fname:   "config.json",
			content: `{invalid}`,
			wantErr: true,
		},
		{
			name:    "html instead of json",
			fname:   "config.json",
			content: `<!DOCTYPE html><html></html>`,
			wantErr: true,
		},
		{
			name:    "empty file",
			fname:   "config.json",
			content: ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, tt.fname)
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			err := ValidateModelFile(tt.fname, path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateModelFile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateModelDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create valid model files
	files := map[string]string{
		"config.json":                `{"model": "test"}`,
		"tokenizer.json":             `{"vocab": {}}`,
		"tokenizer_config.json":      `{"do_lower_case": true}`,
		"special_tokens_map.json":    `{"unk_token": "[UNK]"}`,
		"model.onnx":                 string(make([]byte, 2*1024*1024)), // 2MB fake onnx
	}

	for fname, content := range files {
		path := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := ValidateModelDir(tmpDir); err != nil {
		t.Errorf("ValidateModelDir() with valid files failed: %v", err)
	}

	// Remove one file to test failure
	os.Remove(filepath.Join(tmpDir, "config.json"))
	if err := ValidateModelDir(tmpDir); err == nil {
		t.Error("ValidateModelDir() should fail with missing file")
	}
}
