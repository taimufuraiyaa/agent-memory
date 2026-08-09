package localllm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	configFilename       = "local-llm.json"
	defaultTimeout       = 3 * time.Second
	maximumTimeout       = 30 * time.Second
	maxModelListResponse = 1 << 20
)

type Config struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	TextModel      string `json:"text_model"`
	VisionModel    string `json:"vision_model,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type PublicConfig struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	TextModel        string `json:"text_model"`
	VisionModel      string `json:"vision_model,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
}

type Status struct {
	Config               PublicConfig `json:"config"`
	Configured           bool         `json:"configured"`
	Enabled              bool         `json:"enabled"`
	Reachable            bool         `json:"reachable"`
	TextModelAvailable   bool         `json:"text_model_available"`
	VisionModelAvailable bool         `json:"vision_model_available,omitempty"`
	Error                string       `json:"error,omitempty"`
}

func (c Config) normalized() Config {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.TextModel = strings.TrimSpace(c.TextModel)
	c.VisionModel = strings.TrimSpace(c.VisionModel)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = int(defaultTimeout / time.Second)
	}
	return c
}

func (c Config) Validate() error {
	c = c.normalized()
	if !c.Enabled && c.BaseURL == "" && c.TextModel == "" && c.VisionModel == "" {
		return nil
	}
	if c.BaseURL == "" {
		return errors.New("local LLM base_url is required")
	}
	if c.TextModel == "" {
		return errors.New("local LLM text_model is required")
	}
	if c.TimeoutSeconds < 1 || time.Duration(c.TimeoutSeconds)*time.Second > maximumTimeout {
		return errors.New("local LLM timeout_seconds must be between 1 and 30")
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("local LLM base_url must be a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("local LLM base_url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("local LLM base_url cannot contain credentials, a query, or a fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("local LLM base_url must use a loopback host")
		}
	}
	return nil
}

func (c Config) Public() PublicConfig {
	c = c.normalized()
	return PublicConfig{Enabled: c.Enabled, BaseURL: c.BaseURL, TextModel: c.TextModel, VisionModel: c.VisionModel, APIKeyConfigured: c.APIKey != "", TimeoutSeconds: c.TimeoutSeconds}
}

type Store struct {
	BaseDir string
	mu      sync.Mutex
}

func NewStore(baseDir string) *Store {
	return &Store{BaseDir: strings.TrimSpace(baseDir)}
}

func (s *Store) Load() (Config, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (Config, bool, error) {
	if s.BaseDir == "" {
		return Config{}, false, errors.New("local LLM configuration directory is required")
	}
	content, err := os.ReadFile(filepath.Join(s.BaseDir, configFilename))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read local LLM configuration: %w", err)
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, false, fmt.Errorf("decode local LLM configuration: %w", err)
	}
	config = config.normalized()
	if err := config.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("validate local LLM configuration: %w", err)
	}
	return config, true, nil
}

func (s *Store) Save(config Config, clearSecret ...bool) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.BaseDir == "" {
		return Config{}, errors.New("local LLM configuration directory is required")
	}
	config = config.normalized()
	if existing, found, err := s.load(); err != nil {
		return Config{}, err
	} else if found && config.APIKey == "" && (len(clearSecret) == 0 || !clearSecret[0]) {
		config.APIKey = existing.APIKey
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(s.BaseDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create local LLM configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.BaseDir, ".local-llm-*.tmp")
	if err != nil {
		return Config{}, fmt.Errorf("create local LLM configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return Config{}, err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		_ = temporary.Close()
		return Config{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Config{}, err
	}
	if err := temporary.Close(); err != nil {
		return Config{}, err
	}
	if err := os.Rename(temporaryPath, filepath.Join(s.BaseDir, configFilename)); err != nil {
		return Config{}, fmt.Errorf("replace local LLM configuration: %w", err)
	}
	return config, nil
}

type Checker struct {
	HTTPClient *http.Client
}

func NewChecker(client *http.Client) *Checker {
	if client == nil {
		client = &http.Client{}
	}
	secured := *client
	secured.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("local LLM endpoint redirects are not allowed")
	}
	return &Checker{HTTPClient: &secured}
}

func (c *Checker) Check(ctx context.Context, config Config) Status {
	config = config.normalized()
	status := Status{Config: config.Public(), Configured: config.BaseURL != "" && config.TextModel != "", Enabled: config.Enabled}
	if err := config.Validate(); err != nil {
		status.Error = err.Error()
		return status
	}
	if !status.Configured || !status.Enabled {
		return status
	}
	checkContext, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(checkContext, http.MethodGet, config.BaseURL+"/models", nil)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		status.Error = fmt.Sprintf("local LLM model discovery returned HTTP %d", response.StatusCode)
		return status
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxModelListResponse)).Decode(&payload); err != nil {
		status.Error = "local LLM model discovery returned invalid JSON"
		return status
	}
	status.Reachable = true
	for _, model := range payload.Data {
		status.TextModelAvailable = status.TextModelAvailable || model.ID == config.TextModel
		status.VisionModelAvailable = status.VisionModelAvailable || config.VisionModel != "" && model.ID == config.VisionModel
	}
	if !status.TextModelAvailable {
		status.Error = fmt.Sprintf("configured text model %q was not advertised", config.TextModel)
	}
	return status
}
