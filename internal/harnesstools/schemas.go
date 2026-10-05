package harnesstools

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
