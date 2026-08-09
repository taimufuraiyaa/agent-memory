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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/clientauth"
)

var newHostedTokenStore = func() clientauth.Store { return clientauth.OSKeyring{} }
var runHostedMCP = func(ctx context.Context, executable string, environment []string, stdin io.Reader, stdout, stderr io.Writer) error {
	child := exec.CommandContext(ctx, executable)
	child.Env = environment
	child.Stdin, child.Stdout, child.Stderr = stdin, stdout, stderr
	return child.Run()
}
var hostedProfilePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type hostedProfile struct {
	Name     string `json:"name"`
	APIURL   string `json:"api_url"`
	TenantID string `json:"tenant_id"`
}

func newHostedCommand() *cobra.Command {
	command := &cobra.Command{Use: "hosted", Short: "Use the hosted service explicitly; local databases are never uploaded"}
	command.AddCommand(newHostedLoginCommand(), newHostedLogoutCommand(), newHostedWhoamiCommand(), newHostedWriteCommand(), newHostedQueryCommand(), newHostedImportCommand(), newHostedMCPCommand())
	return command
}

func newHostedMCPCommand() *cobra.Command {
	var profileName, server string
	command := &cobra.Command{Use: "mcp", Short: "Launch the MCP server with a keyring-backed hosted credential", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		server = strings.TrimSpace(server)
		if server == "" || strings.ContainsAny(server, "\x00\r\n") {
			return errors.New("MCP server executable is invalid")
		}
		environment := hostedMCPEnvironment(os.Environ(), profile, token)
		return runHostedMCP(cmd.Context(), server, environment, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().StringVar(&server, "server", "agent-memory-mcp", "MCP server executable")
	return command
}

func hostedMCPEnvironment(base []string, profile hostedProfile, token string) []string {
	blocked := map[string]bool{"AGENT_MEMORY_MODE": true, "AGENT_MEMORY_API_URL": true, "AGENT_MEMORY_TENANT_ID": true, "AGENT_MEMORY_TOKEN": true}
	result := make([]string, 0, len(base)+4)
	for _, value := range base {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			result = append(result, value)
		}
	}
	return append(result, "AGENT_MEMORY_MODE=hosted", "AGENT_MEMORY_API_URL="+profile.APIURL, "AGENT_MEMORY_TENANT_ID="+profile.TenantID, "AGENT_MEMORY_TOKEN="+token)
}

func newHostedLoginCommand() *cobra.Command {
	var profileName, apiURL, tenantID string
	var tokenStdin bool
	command := &cobra.Command{Use: "login", Short: "Store a hosted token in the operating-system keyring", RunE: func(cmd *cobra.Command, _ []string) error {
		if !tokenStdin {
			return errors.New("--token-stdin is required so credentials do not enter shell history")
		}
		profile, err := validateHostedProfile(hostedProfile{Name: profileName, APIURL: apiURL, TenantID: tenantID})
		if err != nil {
			return err
		}
		tokenBytes, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 8193))
		if err != nil {
			return err
		}
		token := strings.TrimSpace(string(tokenBytes))
		if token == "" || len(token) > 8192 {
			return errors.New("hosted token is invalid")
		}
		store := newHostedTokenStore()
		if err := store.Set(profile.Name, token); err != nil {
			return fmt.Errorf("store hosted token in OS keyring: %w", err)
		}
		if err := saveHostedProfile(profile); err != nil {
			_ = store.Delete(profile.Name)
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.login", map[string]any{"profile": profile.Name, "api_url": profile.APIURL, "tenant_id": profile.TenantID, "credential_storage": "os_keyring"})
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().StringVar(&apiURL, "api", "", "Hosted API base URL")
	command.Flags().StringVar(&tenantID, "tenant", "", "Hosted tenant UUID")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read the token from stdin")
	return command
}

func newHostedLogoutCommand() *cobra.Command {
	var profileName string
	var localOnly bool
	command := &cobra.Command{Use: "logout", Short: "Revoke the hosted credential and remove it from the OS keyring", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		if !localOnly {
			if _, err := hostedRequest(cmd.Context(), profile, token, http.MethodDelete, "/v1/current-credential", nil, nil); err != nil {
				return fmt.Errorf("revoke hosted credential before local removal: %w", err)
			}
		}
		if err := newHostedTokenStore().Delete(profile.Name); err != nil {
			return err
		}
		if err := os.Remove(hostedProfilePath(profile.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.logout", map[string]any{"profile": profile.Name, "revoked": !localOnly, "removed_from_keyring": true})
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().BoolVar(&localOnly, "local-only", false, "Remove the local keyring item without revoking it at the service")
	return command
}

func newHostedWhoamiCommand() *cobra.Command {
	var profileName string
	command := &cobra.Command{Use: "whoami", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		var data any
		if _, err := hostedRequest(cmd.Context(), profile, token, http.MethodGet, "/v1/whoami", nil, &data); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.whoami", data)
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	return command
}

func newHostedWriteCommand() *cobra.Command {
	var profileName, workspace, kind, content string
	command := &cobra.Command{Use: "write", Short: "Write one memory to an explicit hosted workspace", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		body := map[string]any{"workspace_id": workspace, "type": kind, "content": content, "source": map[string]any{"type": "user_input"}}
		var data any
		if _, err := hostedRequest(cmd.Context(), profile, token, http.MethodPost, "/v1/memories", body, &data); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.write", data)
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().StringVar(&workspace, "workspace", "", "Destination workspace UUID")
	command.Flags().StringVar(&kind, "type", "semantic", "Memory type")
	command.Flags().StringVar(&content, "content", "", "Memory content")
	_ = command.MarkFlagRequired("workspace")
	_ = command.MarkFlagRequired("content")
	return command
}

func newHostedQueryCommand() *cobra.Command {
	var profileName, query, provider, model string
	var sourceIDs []string
	command := &cobra.Command{Use: "query", Short: "Query explicitly selected hosted sources", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		body := map[string]any{"source_ids": sourceIDs, "query": query, "provider": provider, "model": model}
		var data any
		if _, err := hostedRequest(cmd.Context(), profile, token, http.MethodPost, "/v1/source-queries", body, &data); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.query", data)
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().StringSliceVar(&sourceIDs, "source", nil, "Authorized source UUID (repeatable)")
	command.Flags().StringVar(&query, "query", "", "Question")
	command.Flags().StringVar(&provider, "provider", "local-minilm-scaffold", "Hosted embedding provider")
	command.Flags().StringVar(&model, "model", "local-hash-v1", "Hosted embedding model")
	_ = command.MarkFlagRequired("source")
	_ = command.MarkFlagRequired("query")
	return command
}

func newHostedImportCommand() *cobra.Command {
	var profileName, workspace, bundlePath string
	var passphraseStdin bool
	command := &cobra.Command{Use: "import", Short: "Upload one explicitly selected encrypted portable bundle", RunE: func(cmd *cobra.Command, _ []string) error {
		if !passphraseStdin {
			return errors.New("--passphrase-stdin is required")
		}
		profile, token, err := loadHostedCredential(profileName)
		if err != nil {
			return err
		}
		info, err := os.Stat(bundlePath)
		if err != nil || info.IsDir() || info.Size() > 250<<20 {
			return errors.New("portable bundle file is unavailable or too large")
		}
		encrypted, err := os.ReadFile(bundlePath)
		if err != nil {
			return err
		}
		passphraseBytes, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1025))
		if err != nil {
			return err
		}
		passphrase := strings.TrimSpace(string(passphraseBytes))
		if len(passphrase) < 12 || len(passphrase) > 1024 {
			return errors.New("portable bundle passphrase is invalid")
		}
		headers := map[string]string{"Content-Type": "application/octet-stream", "X-Agent-Memory-Workspace": workspace, "X-Agent-Memory-Bundle-Passphrase": passphrase}
		var data any
		if _, err := hostedRequest(cmd.Context(), profile, token, http.MethodPost, "/v1/imports", encrypted, &data, headers); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "hosted.import", data)
	}}
	command.Flags().StringVar(&profileName, "profile", "default", "Hosted profile name")
	command.Flags().StringVar(&workspace, "workspace", "", "Destination workspace UUID")
	command.Flags().StringVar(&bundlePath, "bundle", "", "Explicit AMPB2 bundle path; SQLite databases are not accepted")
	command.Flags().BoolVar(&passphraseStdin, "passphrase-stdin", false, "Read the bundle passphrase from stdin")
	_ = command.MarkFlagRequired("workspace")
	_ = command.MarkFlagRequired("bundle")
	return command
}

func hostedRequest(ctx context.Context, profile hostedProfile, token, method, path string, body, out any, extraHeaders ...map[string]string) (*http.Response, error) {
	var reader io.Reader
	contentType := "application/json"
	if body != nil {
		switch value := body.(type) {
		case []byte:
			reader = bytes.NewReader(value)
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(encoded)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, profile.APIURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Agent-Memory-Tenant", profile.TenantID)
	request.Header.Set("Idempotency-Key", uuid.NewString())
	request.Header.Set("Content-Type", contentType)
	for _, headers := range extraHeaders {
		for name, value := range headers {
			request.Header.Set(name, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return response, err
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return response, fmt.Errorf("hosted API returned HTTP %d", response.StatusCode)
	}
	if !envelope.OK || response.StatusCode >= 400 {
		if envelope.Error != nil {
			return response, errors.New(envelope.Error.Message)
		}
		return response, fmt.Errorf("hosted API returned HTTP %d", response.StatusCode)
	}
	if out != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return response, err
		}
	}
	return response, nil
}

func validateHostedProfile(profile hostedProfile) (hostedProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.APIURL = strings.TrimRight(strings.TrimSpace(profile.APIURL), "/")
	profile.TenantID = strings.TrimSpace(profile.TenantID)
	if !hostedProfilePattern.MatchString(profile.Name) {
		return hostedProfile{}, errors.New("hosted profile name is invalid")
	}
	parsed, err := url.Parse(profile.APIURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) {
		return hostedProfile{}, errors.New("hosted API must use HTTPS except on localhost")
	}
	if _, err := uuid.Parse(profile.TenantID); err != nil {
		return hostedProfile{}, errors.New("hosted tenant must be a UUID")
	}
	return profile, nil
}

func loadHostedCredential(name string) (hostedProfile, string, error) {
	name = strings.TrimSpace(name)
	if !hostedProfilePattern.MatchString(name) {
		return hostedProfile{}, "", errors.New("hosted profile name is invalid")
	}
	encoded, err := os.ReadFile(hostedProfilePath(name))
	if err != nil {
		return hostedProfile{}, "", err
	}
	var profile hostedProfile
	if err := json.Unmarshal(encoded, &profile); err != nil {
		return hostedProfile{}, "", err
	}
	profile, err = validateHostedProfile(profile)
	if err != nil {
		return hostedProfile{}, "", err
	}
	token, err := newHostedTokenStore().Get(profile.Name)
	return profile, token, err
}

func saveHostedProfile(profile hostedProfile) error {
	directory := filepath.Dir(hostedProfilePath(profile.Name))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(hostedProfilePath(profile.Name), append(encoded, '\n'), 0o600)
}

func hostedProfilePath(name string) string {
	base := strings.TrimSpace(os.Getenv("AGENT_MEMORY_CONFIG_DIR"))
	if base == "" {
		base = defaultAgentMemoryDataDir()
	}
	return filepath.Join(base, "hosted", name+".json")
}
