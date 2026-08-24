package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

type apiEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type runtimeConfig struct {
	workspace string
	dbPath    string
	modelDir  string
	apiURL    string
}

func resolveRuntime(flags commonFlags) (runtimeConfig, error) {
	workspace, err := resolveWorkspace(flags.workspace)
	if err != nil {
		return runtimeConfig{}, err
	}
	dbPath := strings.TrimSpace(flags.dbPath)
	if dbPath == "" {
		dbPath, err = defaultDBPath(workspace)
		if err != nil {
			return runtimeConfig{}, err
		}
	}
	modelDir := strings.TrimSpace(flags.modelDir)
	if modelDir == "" {
		home, _ := os.UserHomeDir()
		modelDir = embeddings.DefaultModelDir(home)
	}
	return runtimeConfig{
		workspace: workspace,
		dbPath:    dbPath,
		modelDir:  modelDir,
		apiURL:    resolveAPIURL(flags.apiURL),
	}, nil
}

func resolveWorkspace(flagWorkspace string) (string, error) {
	if ws := strings.TrimSpace(flagWorkspace); ws != "" {
		if err := validation.ValidateWorkspaceName(ws); err != nil {
			return "", fmt.Errorf("invalid workspace: %w", err)
		}
		return ws, nil
	}
	if ws := strings.TrimSpace(os.Getenv("MEMORY_WORKSPACE")); ws != "" {
		if err := validation.ValidateWorkspaceName(ws); err != nil {
			return "", fmt.Errorf("invalid workspace: %w", err)
		}
		return ws, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", errors.New("workspace is required (set --workspace or MEMORY_WORKSPACE)")
	}
	for _, marker := range []string{".git", "go.mod", "package.json"} {
		if _, err := os.Stat(filepath.Join(cwd, marker)); err == nil {
			return filepath.Base(cwd), nil
		}
	}
	return "", errors.New("workspace is required (set --workspace or MEMORY_WORKSPACE)")
}

func defaultDBPath(workspace string) (string, error) {
	base, err := defaultDBBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, workspace+".db"), nil
}

func defaultDBBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return base, nil
}

func resolveAPIURL(flagAPI string) string {
	if v := strings.TrimSpace(flagAPI); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("MEMORY_API_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return ""
}

func postAPI(ctx context.Context, baseURL, path string, reqBody any, out any) error {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return errors.New("api url is empty")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return fmt.Errorf("invalid api url: %w", err)
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("api request failed: status %d", resp.StatusCode)
		}
		return err
	}
	if !env.OK {
		if env.Error != nil && strings.TrimSpace(env.Error.Message) != "" {
			return errors.New(env.Error.Message)
		}
		return fmt.Errorf("api request failed: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func getAPI(ctx context.Context, baseURL, path string, out any) error {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return errors.New("api url is empty")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return fmt.Errorf("invalid api url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("api request failed: status %d", resp.StatusCode)
		}
		return err
	}
	if !env.OK {
		if env.Error != nil && strings.TrimSpace(env.Error.Message) != "" {
			return errors.New(env.Error.Message)
		}
		return fmt.Errorf("api request failed: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}
