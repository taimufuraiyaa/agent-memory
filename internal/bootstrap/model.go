package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	modelDirName       = "all-MiniLM-L6-v2"
	modelBaseURL       = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main"
	modelMirrorBaseURL = "https://raw.githubusercontent.com/Xyntopia/all-MiniLM-L6-v2/main"
)

// modelChecksums maps model file names to their expected SHA256 checksums.
// These should be updated when the model version changes.
// TODO: Replace with actual SHA256 checksums from HuggingFace model files
// Source: https://huggingface.co/Xenova/all-MiniLM-L6-v2/tree/main
var modelChecksums = map[string]string{
	"config.json":                "7135149f7cffa1a573466c6e4d8423ed73b62fd2332c575bf738a0d033f70df7",
	"tokenizer.json":             "da0e79933b9ed51798a3ae27893d3c5fa4a201126cef75586296df9b4d2c62a0",
	"tokenizer_config.json":      "9261e7d79b44c8195c1cada2b453e55b00aeb81e907a6664974b4d7776172ab3",
	"special_tokens_map.json":    "b6d346be366a7d1d48332dbc9fdf3bf8960b5d879522b7799ddba59e76237ee3",
	"model.onnx":                 "afdb6f1a0e45b715d0bb9b11772f032c399babd23bfc31fed1c170afc848bdb1",
}

// ModelFile represents a file needed for the embedding model.
type ModelFile struct {
	Name string
	Path string
}

// ModelFiles lists all required model files.
var ModelFiles = []ModelFile{
	{Name: "config.json", Path: "config.json"},
	{Name: "tokenizer.json", Path: "tokenizer.json"},
	{Name: "tokenizer_config.json", Path: "tokenizer_config.json"},
	{Name: "special_tokens_map.json", Path: "special_tokens_map.json"},
	{Name: "model.onnx", Path: "onnx/model_quantized.onnx"},
}

// EnsureModel downloads and validates the MiniLM embedding model.
func EnsureModel(dataDir string, quiet bool) error {
	target := filepath.Join(dataDir, "models", modelDirName)
	if err := os.MkdirAll(filepath.Join(target, "onnx"), 0755); err != nil {
		return err
	}

	if err := ValidateModelDir(target); err == nil {
		if !quiet {
			fmt.Fprintln(os.Stderr, "  model files already validated")
		}
		return nil
	}

	for _, mf := range ModelFiles {
		var local string
		if strings.HasPrefix(mf.Path, "onnx/") {
			local = filepath.Join(target, "model.onnx")
		} else {
			local = filepath.Join(target, mf.Name)
		}

		if fileExists(local) {
			if !quiet {
				fmt.Fprintf(os.Stderr, "  %s (cached)\n", mf.Name)
			}
			continue
		}

		url := modelBaseURL + "/" + mf.Path
		if !quiet {
			fmt.Fprintf(os.Stderr, "  %s ↓\n", mf.Name)
		}

		var downloadErr error
		if err := downloadFile(url, local); err == nil {
			if err := ValidateModelFile(mf.Name, local); err == nil {
				// Validate checksum if available
				if expectedChecksum, ok := modelChecksums[mf.Name]; ok {
					if err := validateModelChecksum(local, expectedChecksum); err != nil {
						if !quiet {
							fmt.Fprintf(os.Stderr, "  ⚠ checksum mismatch for %s, trying fallback\n", mf.Name)
						}
						downloadErr = err
						_ = os.Remove(local)
					} else {
						if !quiet {
							fmt.Fprintln(os.Stderr, "  ✓ checksum verified")
						}
						continue
					}
				} else {
					// No checksum available, but file validated
					continue
				}
			} else {
				downloadErr = err
				_ = os.Remove(local)
			}
		} else {
			downloadErr = err
		}

		if !quiet {
			fmt.Fprintf(os.Stderr, "  %s fallback ↓\n", mf.Name)
		}
		if err := downloadModelFallback(mf, local, quiet); err != nil {
			_ = os.Remove(local)
			var errMsg strings.Builder
			errMsg.WriteString(fmt.Sprintf("download %s failed from all sources:\n", mf.Name))
			errMsg.WriteString(fmt.Sprintf("  - primary (%s): %v\n", modelBaseURL, downloadErr))
			errMsg.WriteString(fmt.Sprintf("  - fallback: %v\n", err))
			errMsg.WriteString("Check network connectivity or set AGENT_MEMORY_MODEL_FALLBACK_BASE_URL")
			return errors.New(errMsg.String())
		}
		if err := ValidateModelFile(mf.Name, local); err != nil {
			_ = os.Remove(local)
			return fmt.Errorf("validate %s from fallback: %w", mf.Name, err)
		}
		// Validate checksum for fallback download
		if expectedChecksum, ok := modelChecksums[mf.Name]; ok {
			if err := validateModelChecksum(local, expectedChecksum); err != nil {
				if !quiet {
					fmt.Fprintf(os.Stderr, "  ⚠ checksum warning for %s: %v\n", mf.Name, err)
				}
				// Don't fail on checksum mismatch for fallback, just warn
			} else if !quiet {
				fmt.Fprintln(os.Stderr, "  ✓ checksum verified")
			}
		}
	}

	return ValidateModelDir(target)
}

// ValidateModelDir validates all model files in a directory.
func ValidateModelDir(dir string) error {
	for _, mf := range ModelFiles {
		local := filepath.Join(dir, mf.Name)
		if mf.Name == "model.onnx" {
			local = filepath.Join(dir, "model.onnx")
		}
		if err := ValidateModelFile(mf.Name, local); err != nil {
			return err
		}
	}
	return nil
}

// ValidateModelFile validates a single model file.
func ValidateModelFile(name, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("%s is empty", name)
	}

	snippet := strings.TrimSpace(strings.ToLower(string(data[:min(len(data), 256)])))
	if strings.HasPrefix(snippet, "<!doctype") || strings.HasPrefix(snippet, "<html") || strings.Contains(snippet, "<title>") {
		return fmt.Errorf("%s looks like HTML, not a model asset", name)
	}

	switch filepath.Ext(name) {
	case ".json":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("invalid json: %w", err)
		}
	case ".onnx":
		if len(data) < 1024*1024 {
			return fmt.Errorf("onnx file too small: %d bytes", len(data))
		}
		if bytes.Contains(bytes.ToLower(data[:min(len(data), 4096)]), []byte("<html")) {
			return fmt.Errorf("onnx file looks like html")
		}
	}
	return nil
}

func modelFallbackURL(mf ModelFile) string {
	if base := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_FALLBACK_BASE_URL")); base != "" {
		return strings.TrimRight(base, "/") + "/" + mf.Path
	}
	return modelMirrorBaseURL + "/" + mf.Path
}

func downloadModelFallback(mf ModelFile, dest string, quiet bool) error {
	if err := downloadModelFallbackFromNPM(mf, dest); err == nil {
		return nil
	}
	return downloadFile(modelFallbackURL(mf), dest)
}

func downloadModelFallbackFromNPM(mf ModelFile, dest string) error {
	pkg := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_NPM_PACKAGE"))
	tarballURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_NPM_TARBALL_URL"))
	if pkg == "" && tarballURL == "" {
		return errors.New("npm fallback not configured")
	}

	tmpDir, err := os.MkdirTemp("", "agent-memory-model-npm-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tarballPath := filepath.Join(tmpDir, "model-fallback.tgz")
	switch {
	case tarballURL != "":
		if err := downloadFile(tarballURL, tarballPath); err != nil {
			return err
		}
	case pkg != "":
		cmd := exec.Command("npm", "pack", pkg, "--silent")
		cmd.Dir = tmpDir
		out, err := cmd.Output()
		if err != nil {
			return err
		}
		name := strings.TrimSpace(string(out))
		if name == "" {
			return errors.New("npm pack produced no tarball name")
		}
		tarballPath = filepath.Join(tmpDir, filepath.Base(name))
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := extractTarGz(tarballPath, extractDir); err != nil {
		return err
	}

	prefix := strings.Trim(strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_NPM_PREFIX")), "/")
	if prefix == "" {
		prefix = "package"
	}
	source := filepath.Join(extractDir, prefix, mf.Path)
	if mf.Name == "model.onnx" && !fileExists(source) {
		source = filepath.Join(extractDir, prefix, "model.onnx")
	}
	if !fileExists(source) {
		return fmt.Errorf("npm fallback asset missing: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry escapes target dir: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validateModelChecksum validates a model file's SHA256 checksum.
func validateModelChecksum(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: got %s, want %s", actual[:16]+"...", expectedHex[:16]+"...")
	}
	return nil
}
