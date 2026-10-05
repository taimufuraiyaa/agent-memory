package harnesstools

import (
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessgit"
)

// Schema describes one tool for a model: its name, purpose and argument schema. The
// argument schemas are strict and carry the same bounds the provider enforces.
type Schema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func object(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{} // an empty list, never null, which is not valid JSON Schema
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

// Schemas returns the tool descriptions in a fixed order.
func Schemas() []Schema {
	pathProp := map[string]any{"type": "string", "maxLength": 512, "description": "Path relative to the project root"}
	return []Schema{
		{Name: ToolListDir, Description: "List one directory of the project. Hidden and credential-like entries are not shown.",
			Parameters: object(map[string]any{"path": pathProp})},
		{Name: ToolReadFile, Description: "Read lines of one text file in the project, with line numbers and a revision.",
			Parameters: object(map[string]any{
				"path":       pathProp,
				"start_line": map[string]any{"type": "integer", "minimum": 1},
				"max_lines":  map[string]any{"type": "integer", "minimum": 1, "maximum": MaxReadLines},
			}, "path")},
		{Name: ToolSearch, Description: "Search project text files for a literal or regular expression. Dependency and build directories are skipped.",
			Parameters: object(map[string]any{
				"query":       map[string]any{"type": "string", "minLength": 1, "maxLength": MaxQueryBytes},
				"regex":       map[string]any{"type": "boolean"},
				"ignore_case": map[string]any{"type": "boolean"},
				"path":        pathProp,
				"glob":        map[string]any{"type": "string", "maxLength": MaxGlobBytes, "description": "Match file base names, such as *.go"},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxSearchResults},
				"context":     map[string]any{"type": "integer", "minimum": 0, "maximum": MaxContextLines},
			}, "query")},
	}
}

// EditSchemas returns the mutating tool descriptions, offered only when editing is enabled.
// Every change is shown to a person who must approve it, so the descriptions tell a model
// to keep changes small and exact.
func EditSchemas() []Schema {
	pathProp := map[string]any{"type": "string", "maxLength": 512, "description": "Path relative to the project root"}
	return []Schema{
		{Name: ToolCreateFile, Description: "Create a new text file, and any missing directories, in the project. Fails if the path exists. A person must approve each call.",
			Parameters: object(map[string]any{
				"path":    pathProp,
				"content": map[string]any{"type": "string", "maxLength": MaxCreateBytes},
			}, "path", "content")},
		{Name: ToolDeleteFile, Description: "Delete one text file in the project. A person must approve each call, with extra care.",
			Parameters: object(map[string]any{"path": pathProp}, "path")},
		{Name: ToolEditFile, Description: "Replace exact text in one existing text file. Each old_text must appear exactly once in the file as it is now, and edits must not overlap. Keep each change small. A person must approve each call.",
			Parameters: object(map[string]any{
				"path": pathProp,
				"edits": map[string]any{"type": "array", "minItems": 1, "maxItems": MaxEdits, "items": object(map[string]any{
					"old_text": map[string]any{"type": "string", "minLength": 1, "maxLength": MaxEditTextBytes},
					"new_text": map[string]any{"type": "string", "maxLength": MaxEditTextBytes},
				}, "old_text", "new_text")},
			}, "path", "edits")},
	}
}

// CommandSchemas returns the command tool's description, offered only when commands are
// enabled. The description tells a model what the harness will and will not do.
func CommandSchemas() []Schema {
	return []Schema{{Name: ToolRunCommand, Description: "Run one program with an exact argument list, without a shell, in the project or a subdirectory. " +
		"Use it for tests, builds and linters, for example [\"go\",\"test\",\"./...\"]. Pipes, redirection, globbing, && and environment assignments are not interpreted. " +
		"Shells, network tools, deletion tools and version control are refused. The command gets a minimal environment and no network settings, runs for at most the timeout, " +
		"and reports its output and the files it changed. A person must approve each call.",
		Parameters: object(map[string]any{
			"argv":            map[string]any{"type": "array", "minItems": 1, "maxItems": maxArgvItems, "items": map[string]any{"type": "string", "maxLength": maxArgvItemBytes}},
			"cwd":             map[string]any{"type": "string", "maxLength": 512, "description": "Directory relative to the project root; default is the root"},
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(MaxCommandTimeout / time.Second), "description": "Default 60"},
		}, "argv")}}
}

// GitSchemas returns the Git tool descriptions, offered only when Git is enabled.
func GitSchemas() []Schema {
	pathProp := map[string]any{"type": "string", "maxLength": 512, "description": "Path relative to the project root"}
	return []Schema{
		{Name: ToolGitStatus, Description: "Show the current branch and the changed paths of the project's Git repository, without file content.",
			Parameters: object(map[string]any{})},
		{Name: ToolGitDiff, Description: "Show the unified diff of staged or unstaged changes, optionally for one path. Hidden and credential-like files are left out.",
			Parameters: object(map[string]any{"staged": map[string]any{"type": "boolean"}, "path": pathProp})},
		{Name: ToolGitLog, Description: "List recent commits (hash, author name, date, subject), newest first, optionally for one path.",
			Parameters: object(map[string]any{"count": map[string]any{"type": "integer", "minimum": 1, "maximum": harnessgit.MaxLogEntries}, "path": pathProp})},
		{Name: ToolGitStage, Description: "Stage named files for a commit. Only regular, visible files; nothing is committed. A person must approve each call.",
			Parameters: object(map[string]any{"paths": map[string]any{"type": "array", "minItems": 1, "maxItems": MaxGitStagePaths, "items": pathProp}}, "paths")},
		{Name: ToolGitCommit, Description: "Commit exactly what is staged, on the current branch, with the given message. It never stages, amends, commits all files, pushes or switches branches, and repository hooks are not run. A person must approve each call.",
			Parameters: object(map[string]any{"message": map[string]any{"type": "string", "minLength": 1, "maxLength": harnessgit.MaxMessageBytes}}, "message")},
	}
}
