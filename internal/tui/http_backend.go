package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const maxResponseBytes = 16 << 20
const maxUploadBytes = 64 << 20

type Request struct {
	Method        string
	Path          string
	Values        map[string]any
	UploadPath    string
	OutputPath    string
	ProjectSource bool
	InputPath     string
}

type ConsoleBackend interface {
	Execute(context.Context, Request) (json.RawMessage, error)
}

type HTTPBackend struct {
	baseURL   string
	workspace string
	client    *http.Client
}

func NewHTTPBackend(baseURL, workspace string, client *http.Client) (*HTTPBackend, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("API URL must be an http or https address without credentials, query or fragment")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("workspace is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	// Never follow redirects to a different host with workspace payloads.
	copyClient := *client
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPBackend{baseURL: parsed.String(), workspace: workspace, client: &copyClient}, nil
}

func (b *HTTPBackend) Execute(ctx context.Context, request Request) (json.RawMessage, error) {
	result, err := b.execute(ctx, request)
	if err == nil && request.Path == "/api/v1/memories/recent" {
		pinned, _ := request.Values["pinned_only"].(bool)
		memoryType, _ := request.Values["memory_type"].(string)
		if pinned || memoryType != "" {
			var data map[string]any
			if json.Unmarshal(result, &data) == nil {
				records, _ := data["results"].([]any)
				filtered := []any{}
				for _, value := range records {
					record, ok := value.(map[string]any)
					if ok && (!pinned || record["pinned"] == true) && (memoryType == "" || record["type"] == memoryType) {
						filtered = append(filtered, record)
					}
				}
				data["results"] = filtered
				data["filter_scope"] = "latest bounded recent results"
				result, _ = json.Marshal(data)
			}
		}
	}
	if err != nil || !request.ProjectSource {
		return result, err
	}
	return b.projectImportedSource(ctx, request, result)
}

func (b *HTTPBackend) execute(ctx context.Context, request Request) (json.RawMessage, error) {
	if !strings.HasPrefix(request.Path, "/api/v1/") || path.Clean(request.Path) != request.Path || strings.ContainsAny(request.Path, "?#\\") {
		return nil, errors.New("unsupported terminal API route")
	}
	switch request.Method {
	case "GET", "POST", "PUT", "DELETE":
	default:
		return nil, errors.New("unsupported terminal API method")
	}
	values := make(map[string]any, len(request.Values)+1)
	for key, value := range request.Values {
		values[key] = value
	}
	if request.InputPath != "" {
		file, err := os.Open(request.InputPath)
		if err != nil {
			return nil, errors.New("cannot open import file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxResponseBytes {
			return nil, errors.New("import must be a regular JSON file of at most 16 MiB")
		}
		payload, err := io.ReadAll(io.LimitReader(file, maxResponseBytes+1))
		if err != nil || len(payload) > maxResponseBytes {
			return nil, errors.New("cannot read import file")
		}
		var imported map[string]any
		if err := json.Unmarshal(payload, &imported); err != nil {
			return nil, errors.New("import file must contain a JSON export bundle")
		}
		if data, ok := imported["data"].(map[string]any); ok {
			imported = data
		}
		for key, value := range imported {
			values[key] = value
		}
	}
	values["workspace"] = b.workspace
	bindConsoleValues(request.Path, values, b.workspace)
	query := url.Values{"workspace": {b.workspace}}
	if request.Path == "/api/v1/scheduler/run" {
		if force, ok := values["force"]; ok {
			query.Set("force", fmt.Sprint(force))
		}
	}
	var body io.Reader
	contentType := "application/json"
	if request.UploadPath != "" {
		file, err := os.Open(request.UploadPath)
		if err != nil {
			return nil, errors.New("cannot open source file")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxUploadBytes {
			return nil, errors.New("source must be a regular file of at most 64 MiB")
		}
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for key, value := range values {
			if err := writer.WriteField(key, fmt.Sprint(value)); err != nil {
				return nil, err
			}
		}
		part, err := writer.CreateFormFile("source", filepath.Base(request.UploadPath))
		if err != nil {
			return nil, err
		}
		count, err := io.Copy(part, io.LimitReader(file, maxUploadBytes+1))
		if err != nil {
			return nil, err
		}
		if count > maxUploadBytes {
			return nil, errors.New("source exceeds 64 MiB")
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		body = &buffer
		contentType = writer.FormDataContentType()
	} else if request.Method == "GET" || (request.Method == "DELETE" && strings.HasPrefix(request.Path, "/api/v1/client-profiles/")) {
		for key, value := range values {
			query.Set(key, fmt.Sprint(value))
		}
	} else {
		payload, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, b.baseURL+request.Path+"?"+query.Encode(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	response, err := b.client.Do(req)
	if err != nil {
		return nil, errors.New("API request failed; check connection or retry")
	}
	defer response.Body.Close()
	if request.OutputPath != "" && response.StatusCode >= 200 && response.StatusCode < 300 {
		file, err := os.OpenFile(request.OutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, errors.New("cannot create export file; choose a new path")
		}
		count, copyErr := io.Copy(file, io.LimitReader(response.Body, 256<<20+1))
		if copyErr == nil && count > 256<<20 {
			copyErr = errors.New("export exceeds 256 MiB")
		}
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(request.OutputPath)
			return nil, errors.New("export write failed")
		}
		result, _ := json.Marshal(map[string]any{"saved_to": request.OutputPath, "bytes": count})
		return result, nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxResponseBytes {
		return nil, errors.New("API response too large; narrow the query")
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("API returned an invalid response (HTTP %d)", response.StatusCode)
	}
	if !envelope.OK || response.StatusCode < 200 || response.StatusCode >= 300 {
		if envelope.Error != nil {
			message := envelope.Error.Message
			for key, value := range values {
				if isSecretField(key) && fmt.Sprint(value) != "" {
					message = strings.ReplaceAll(message, fmt.Sprint(value), "[hidden]")
				}
			}
			return nil, fmt.Errorf("%s: %s", envelope.Error.Code, sanitizeTerminalText(message, false))
		}
		return nil, fmt.Errorf("API rejected the operation (HTTP %d)", response.StatusCode)
	}
	if len(envelope.Data) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return envelope.Data, nil
}

func (b *HTTPBackend) LoadOverview(ctx context.Context) (Overview, error) {
	raw, err := b.Execute(ctx, Request{Method: "GET", Path: "/api/v1/stats"})
	if err != nil {
		return Overview{}, err
	}
	var stats struct {
		Total    int            `json:"memory_count"`
		Pinned   int            `json:"pinned_count"`
		Diagrams int            `json:"diagram_count"`
		Types    map[string]int `json:"memory_type_counts"`
		Tiers    map[string]int `json:"storage_tier_counts"`
		Updated  string         `json:"last_memory_updated_at"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		return Overview{}, err
	}
	updated, _ := time.Parse(time.RFC3339Nano, stats.Updated)
	return Overview{Workspace: b.workspace, Total: stats.Total, Pinned: stats.Pinned, Diagrams: stats.Diagrams, TypeCounts: stats.Types, TierCounts: stats.Tiers, UpdatedAt: updated}, nil
}

func (b *HTTPBackend) ListRecent(ctx context.Context, limit int) ([]MemoryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	raw, err := b.Execute(ctx, Request{Method: "GET", Path: "/api/v1/memories/recent", Values: map[string]any{"limit": strconv.Itoa(limit)}})
	return decodeMemoryItems(raw, err)
}

func (b *HTTPBackend) Search(ctx context.Context, query string, limit int) ([]MemoryItem, error) {
	if limit < 1 || limit > 200 {
		limit = 20
	}
	raw, err := b.Execute(ctx, Request{Method: "POST", Path: "/api/v1/memories/search", Values: map[string]any{"query": query, "top_k": limit, "mode": "search", "explain": true}})
	return decodeMemoryItems(raw, err)
}

func decodeMemoryItems(raw json.RawMessage, err error) ([]MemoryItem, error) {
	if err != nil {
		return nil, err
	}
	var data struct {
		Results []struct {
			core.MemoryEntry
			Score *float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	items := make([]MemoryItem, 0, len(data.Results))
	for _, result := range data.Results {
		memory := result.MemoryEntry
		item := MemoryItem{ID: memory.ID, Type: string(memory.Type), StorageTier: string(memory.StorageTier), Content: memory.Content, SourceType: string(memory.Source.Type), SourcePath: memory.Source.FilePath, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt, LastAccessed: memory.LastAccessedAt, Pinned: memory.Pinned, AccessCount: memory.AccessCount, Score: result.Score}
		if item.SourcePath == "" {
			item.SourcePath = memory.Source.NotePath
		}
		if memory.Outcome != nil {
			item.Outcome = &Outcome{Result: string(memory.Outcome.Result), Approach: memory.Outcome.Approach, Reason: memory.Outcome.Reason}
		}
		items = append(items, item)
	}
	return items, nil
}

func isSecretField(key string) bool {
	key = strings.ToLower(key)
	return key == "token" || strings.HasSuffix(key, "_token") || strings.Contains(key, "password") || strings.Contains(key, "passphrase") || key == "api_key" || strings.Contains(key, "secret")
}
