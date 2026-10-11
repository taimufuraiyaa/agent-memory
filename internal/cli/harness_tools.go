package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesstools"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// harnessToolsEnv turns on the project tools for the openai composition: a comma list of
// read, edit, git and commands. Read is implied. Everything that changes a file or runs a
// program asks a person at the terminal first, through the approval store.
const (
	harnessToolsEnv      = "AGENT_MEMORY_HARNESS_TOOLS"
	harnessToolLimitEnv  = "AGENT_MEMORY_HARNESS_MAX_TOOLS"
	defaultOfferedTools  = 8
	maxConfigurableTools = 32
)

type toolsComposition struct {
	binding    harnessrun.Binding
	policy     harnessrun.ToolPolicy
	approvals  *harnessapproval.Store
	selector   harnessrun.ToolSelector
	maxOffered int
	names      []string
}

// composeTools returns nil, nil unless tools were asked for. A typo or an unknown name is an
// error, so a misspelling can never leave a mutating tool half-enabled or silently off.
func composeTools(registry *harness.Registry, workspaces *workspace.Manager, baseDir string, decisions *jevComposition) (*toolsComposition, error) {
	raw := strings.TrimSpace(os.Getenv(harnessToolsEnv))
	if raw == "" {
		return nil, nil
	}
	on := map[string]bool{"read": true}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "read", "edit", "git", "commands":
			on[name] = true
		default:
			return nil, fmt.Errorf("%s: %q is not a tool group; choose from read, edit, git, commands", harnessToolsEnv, name)
		}
	}
	work := filepath.Join(baseDir, "harness", "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, errors.New("cannot prepare the tool work directory")
	}
	cfg := harnesstools.Config{
		Root: func(name string) (string, error) {
			project, err := workspaces.Project(name)
			if err != nil || strings.TrimSpace(project.WorkspaceRoot) == "" {
				return "", errors.New("workspace has no project root")
			}
			return project.WorkspaceRoot, nil
		},
		Redact: engine.RedactSecretsAndPII,
	}
	if on["edit"] {
		cfg.Edit = harnesstools.EditConfig{Enabled: true, PreimageDir: harnessSavedDir(baseDir)}
	}
	if on["git"] {
		cfg.Git = harnesstools.GitConfig{Enabled: true, TempRoot: filepath.Join(work, "git")}
	}
	if on["commands"] {
		cfg.Command = harnesstools.CommandConfig{Enabled: true, TempRoot: filepath.Join(work, "cmd")}
	}
	provider, err := harnesstools.NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	if err := provider.Register(registry); err != nil {
		return nil, err
	}
	var policy harnessrun.ToolPolicy = harnesstools.ProjectPolicy()
	var selector harnessrun.ToolSelector
	limit := 0
	if decisions != nil {
		if decisions.enabled[harnessdecide.KindCommandRisk] {
			policy = harnesstools.NewAdvisedPolicy(harnesstools.ProjectPolicy(), harnesstools.ServiceRiskAdvisor{For: decisions.hub.Service})
		}
		if decisions.enabled[harnessdecide.KindTools] {
			selector, limit = harnessrun.ServiceToolSelector{For: decisions.hub.Service}, defaultOfferedTools
		}
	}
	if v := strings.TrimSpace(os.Getenv(harnessToolLimitEnv)); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 2 || n > maxConfigurableTools || fmt.Sprint(n) != v {
			return nil, fmt.Errorf("%s must be a whole number between 2 and %d", harnessToolLimitEnv, maxConfigurableTools)
		}
		limit = n
	}
	names := []string{}
	for _, c := range provider.Manifest().Capabilities {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return &toolsComposition{binding: harnessrun.Binding{Provider: harnesstools.ProviderID, Capability: harnesstools.ToolReadFile}, policy: policy,
		approvals: harnessapproval.Open(baseDir), selector: selector, maxOffered: limit, names: names}, nil
}

// toolDescriber describes the tools a model call may be offered, by identifier. The clarify
// request is a pseudo-tool: a question for the person who started the run.
func toolDescriber() func(ids []string) []openaiapi.Tool {
	known := map[string]openaiapi.Tool{"clarify": {Name: "clarify", Description: "Ask the person who started this run one question when you cannot proceed without the answer.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"question": map[string]any{"type": "string", "maxLength": 1024}}, "required": []string{"question"}, "additionalProperties": false}}}
	for _, group := range [][]harnesstools.Schema{harnesstools.Schemas(), harnesstools.EditSchemas(), harnesstools.CommandSchemas(), harnesstools.GitSchemas()} {
		for _, schema := range group {
			known[schema.Name] = openaiapi.Tool{Name: schema.Name, Description: schema.Description, Parameters: schema.Parameters}
		}
	}
	return func(ids []string) []openaiapi.Tool {
		out := make([]openaiapi.Tool, 0, len(ids))
		for _, id := range ids {
			if tool, ok := known[id]; ok {
				out = append(out, tool)
			}
		}
		return out
	}
}
