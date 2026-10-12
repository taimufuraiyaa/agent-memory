package tui

import "strings"

type Field struct {
	Name, Label, Kind, Default, From string
	Choices                          []string
	Required                         bool
}

type Action struct {
	ID, Label, Screen, Method, Path string
	Fields                          []Field
	Mutation                        bool
}

type Screen struct {
	Name, Path, Array string
	Values            map[string]any
}

var consoleScreens = []Screen{
	{Name: "Ask"},
	{Name: "Memories", Path: "/api/v1/memories/recent", Array: "results", Values: map[string]any{"limit": 100}},
	{Name: "Notes", Path: "/api/v1/notes", Array: "notes", Values: map[string]any{"include_deleted": true}},
	{Name: "Sources", Path: "/api/v1/notes", Array: "notes"},
	{Name: "Graph", Path: "/api/v1/graph-index/explorer", Values: map[string]any{"configuration_id": "default"}},
	{Name: "How history", Path: "/api/v1/solutions/activity", Array: "episodes", Values: map[string]any{"limit": 100}},
	{Name: "Activity", Path: "/api/v1/sessions", Array: "sessions", Values: map[string]any{"limit": 100}},
	{Name: "Feedback", Path: "/api/v1/feedback"},
	{Name: "Lifecycle", Path: "/api/v1/scheduler/history", Array: "history", Values: map[string]any{"limit": 100}},
	{Name: "Skills", Path: "/api/v1/skills/lifecycle/list", Array: "skills"},
	{Name: "Clients", Path: "/api/v1/client-profiles", Array: "profiles"},
	{Name: "Settings", Path: "/api/v1/library/local-llm"},
	{Name: "Workspaces", Path: "/api/v1/projects/list", Array: "projects"},
	{Name: "Data"},
	{Name: "Rights", Path: "/api/v1/rights-attestation/status"},
	{Name: "Diagnostics", Path: "/api/v1/stats"},
	{Name: "Benchmarks", Path: "/api/v1/benchmark/runs", Array: "runs", Values: map[string]any{"limit": 50}},
	{Name: "Replay", Path: "/api/v1/replay/sessions", Array: "sessions", Values: map[string]any{"limit": 100}},
	{Name: "Observations", Path: "/api/v1/observations", Array: "observations", Values: map[string]any{"limit": 100}},
	{Name: "Chat and search history", Path: "/api/v1/tui/history", Array: "items"},
}

func field(name, kind, defaultValue, from string, required bool) Field {
	item := Field{Name: name, Label: strings.ReplaceAll(name, "_", " "), Kind: kind, Default: defaultValue, From: from, Required: required}
	if kind == "boolean" {
		item.Choices = []string{"false", "true"}
	}
	if kind == "text" {
		item.Choices = map[string][]string{
			"type":         {"episodic", "semantic", "procedural", "outcome"},
			"format":       {"pdf", "epub", "markdown", "text"},
			"rights_basis": {"author_owned", "licensed", "public_domain", "lawfully_acquired_private_use"},
			"library_kind": {"personal", "organization"},
			"depth":        {"shallow", "medium", "deep"},
			"graph_mode":   {"basic", "auto", "local_graph", "global"},
			"client_kind":  {"codex", "claude", "cursor", "kiro", "other"},
			"tool_profile": {"default", "expanded"},
			"outcome":      {"helpful", "ignored", "rejected", "harmful"},
			"decision":     {"accept", "reject"},
		}[name]
	}
	return item
}
func textField(name, defaultValue, from string) Field {
	return field(name, "text", defaultValue, from, true)
}
func optionalField(name, defaultValue, from string) Field {
	return field(name, "text", defaultValue, from, false)
}
func intField(name, defaultValue, from string) Field {
	return field(name, "integer", defaultValue, from, true)
}
func boolField(name, defaultValue, from string) Field {
	return field(name, "boolean", defaultValue, from, true)
}
func jsonField(name, defaultValue, from string) Field {
	return field(name, "json", defaultValue, from, true)
}
func multiField(name, defaultValue, from string) Field {
	return field(name, "multiline", defaultValue, from, true)
}
func principalField() Field {
	return optionalField("principal_id", "", "properties.library_principal_id")
}
func configField() Field { return textField("configuration_id", "default", "") }
func noteIDField() Field { return textField("note_id", "", "id") }
func action(id, label, screen, method, path string, mutation bool, fields ...Field) Action {
	return Action{ID: id, Label: label, Screen: screen, Method: method, Path: path, Mutation: mutation, Fields: fields}
}

var consoleActions = []Action{
	action("history-rename", "Rename saved chat or search", "Chat and search history", "POST", "/api/v1/tui/history/rename", true, textField("id", "", "id"), textField("title", "", "title")),
	action("history-delete", "Delete saved chat or search", "Chat and search history", "POST", "/api/v1/tui/history/delete", true, textField("id", "", "id")),
	action("history-ask", "Ask again from saved query", "Chat and search history", "POST", "/api/v1/memories/recall/preview", false, multiField("task_description", "", "query"), intField("top_k", "50", ""), intField("token_budget", "4000", ""), boolField("include_memories", "true", "")),
	action("history-search", "Repeat saved search", "Chat and search history", "POST", "/api/v1/memories/search", false, textField("query", "", "query"), intField("top_k", "20", "")),
	action("ask", "Ask with grounded memory", "Ask", "POST", "/api/v1/memories/recall/preview", false, multiField("task_description", "", ""), intField("top_k", "50", ""), intField("token_budget", "4000", ""), boolField("include_memories", "true", ""), textField("graph_mode", "auto", ""), boolField("graph_required", "false", ""), boolField("graph_allow_stale", "false", "")),
	action("translate", "Translate an answer", "Ask", "POST", "/api/v1/library/local-llm/translate", false, multiField("text", "", "context_block"), textField("target_language", "English", "")),
	action("memory-write", "Write a memory", "Memories", "POST", "/api/v1/memories/write", true, textField("type", "semantic", ""), multiField("content", "", ""), jsonField("source", `{"type":"user_input"}`, ""), jsonField("keywords", "[]", ""), field("outcome", "json", "", "", false)),
	action("memory-pin", "Pin or unpin memory", "Memories", "POST", "/api/v1/memories/pin", true, textField("memory_id", "", "id"), boolField("pinned", "true", "pinned")),
	action("memory-delete", "Delete memories", "Memories", "POST", "/api/v1/memories/delete", true, field("memory_ids", "array", "", "id", true)),
	action("memory-filter", "Browse pinned, type or ungrouped", "Memories", "GET", "/api/v1/memories/recent", false, intField("limit", "200", ""), boolField("pinned_only", "false", ""), optionalField("memory_type", "", ""), boolField("ungrouped", "false", "")),
	action("memory-search", "Search with filters", "Memories", "POST", "/api/v1/memories/search", false, textField("query", "", ""), intField("top_k", "50", ""), boolField("explain", "true", ""), jsonField("filters", "{}", "")),
	action("note-create", "Create a note", "Notes", "POST", "/api/v1/notes/create", true, textField("title", "", ""), textField("path", "", ""), multiField("body", "", ""), jsonField("properties", "{}", "")),
	action("note-update", "Edit a note", "Notes", "POST", "/api/v1/notes/update", true, noteIDField(), intField("expected_revision", "", "revision"), textField("title", "", "title"), textField("path", "", "path"), multiField("body", "", "body"), jsonField("properties", "{}", "properties")),
	action("note-trash", "Move note to trash", "Notes", "POST", "/api/v1/notes/trash", true, noteIDField()),
	action("note-restore", "Restore note from trash", "Notes", "POST", "/api/v1/notes/restore", true, noteIDField()),
	action("note-delete", "Delete note permanently", "Notes", "POST", "/api/v1/notes/delete", true, noteIDField()),
	action("note-retry", "Retry source/note indexing", "Notes", "POST", "/api/v1/notes/index/retry", true, noteIDField()),
	action("note-revisions", "Inspect note revisions", "Notes", "GET", "/api/v1/notes/revisions", false, noteIDField()),
	action("note-revision-restore", "Restore a note revision", "Notes", "POST", "/api/v1/notes/revisions/restore", true, noteIDField(), intField("revision", "", "revision"), intField("expected_revision", "", "revision")),
	action("note-backlinks", "Inspect note backlinks", "Notes", "GET", "/api/v1/notes/backlinks", false, noteIDField()),
	action("source-import", "Import PDF, EPUB, Markdown or text", "Sources", "POST", "/api/v1/library/imports", true, field("source_file", "upload", "", "", true), textField("title", "", ""), textField("format", "markdown", ""), textField("rights_basis", "", ""), principalField(), textField("library_kind", "personal", ""), optionalField("organization_id", "", ""), optionalField("library_id", "", ""), textField("edition_label", "original", ""), textField("language", "en", "")),
	action("source-job", "Inspect source import job", "Sources", "GET", "/api/v1/library/jobs", false, textField("id", "", "properties.library_import_job_id")),
	action("source-structure", "Inspect book structure", "Sources", "GET", "/api/v1/library/structure", false, textField("edition_id", "", "properties.library_edition_id"), principalField()),
	action("source-query", "Ask a source question", "Sources", "POST", "/api/v1/library/query", false, multiField("question", "", ""), principalField(), intField("limit", "10", ""), boolField("propose_memory", "false", "")),
	action("source-review", "Review proposed book memory", "Sources", "POST", "/api/v1/library/memory-review", true, textField("proposal_id", "", "id"), principalField(), textField("decision", "reject", "")),
	action("study", "Study registered project", "Sources", "POST", "/api/v1/projects/study", true, textField("depth", "medium", ""), boolField("dry_run", "true", ""), intField("max_files", "100", ""), intField("offset", "0", "")),
	action("seminar-start", "Start source seminar", "Sources", "POST", "/api/v1/library/seminars/start", true, jsonField("payload", "{}", "")),
	action("seminar-status", "Inspect source seminar", "Sources", "GET", "/api/v1/library/seminars/status", false, textField("id", "", "id"), principalField()),
	action("seminar-cancel", "Cancel source seminar", "Sources", "POST", "/api/v1/library/seminars/cancel", true, textField("id", "", "id"), principalField()),
	action("source-wiki", "Inspect source wiki", "Sources", "GET", "/api/v1/library/wiki", false, textField("edition_id", "", "properties.library_edition_id"), principalField()),
	action("solution-detail", "Inspect solution episode", "How history", "GET", "/api/v1/solutions/activity", false, textField("episode_id", "", "id")),
	action("solution-recall", "Recall a solution path", "How history", "POST", "/api/v1/solutions/recall", false, multiField("task", "", ""), textField("principal_id", "codex", "principal_id")),
	action("solution-review", "Review solution episode", "How history", "POST", "/api/v1/solutions/review", true, textField("episode_id", "", "id"), textField("principal_id", "codex", "principal_id"), textField("action", "pin", ""), optionalField("step_id", "", ""), optionalField("reason", "", ""), optionalField("summary", "", ""), optionalField("successor_episode_id", "", ""), boolField("pinned", "true", "")),
	action("request-feedback", "Score a retrieval request", "Feedback", "POST", "/api/v1/requests/feedback", true, textField("request_id", "", "request_id"), intField("score", "4", ""), multiField("reason", "", ""), intField("useful_count", "0", ""), intField("total_count", "0", "")),
	action("memory-feedback", "Review a memory outcome", "Feedback", "POST", "/api/v1/memories/feedback", true, textField("memory_id", "", "id"), textField("outcome", "helpful", ""), optionalField("reason", "", "")),
	action("scheduler-status", "Inspect scheduler status", "Lifecycle", "GET", "/api/v1/scheduler/status", false),
	action("scheduler-run", "Run lifecycle maintenance", "Lifecycle", "POST", "/api/v1/scheduler/run", true, boolField("force", "false", "")),
	action("skill-list-files", "Browse installed skill files", "Skills", "GET", "/api/v1/skills", false),
	action("skill-inspect", "Inspect skill lifecycle", "Skills", "GET", "/api/v1/skills/inspect", false, textField("skill_id", "", "id"), textField("environment", "local", "")),
	action("skill-lifecycle", "Operate skill lifecycle", "Skills", "POST", "/api/v1/skills/lifecycle", true, textField("actor", "codex", ""), textField("operation", "", ""), jsonField("payload", "{}", "")),
	action("skill-status", "Inspect skill orchestration", "Skills", "GET", "/api/v1/skills/orchestration/status", false, textField("skill_id", "", "id"), textField("actor", "codex", ""), textField("environment", "local", ""), intField("limit", "50", "")),
	action("skill-control", "Control skill orchestration", "Skills", "POST", "/api/v1/skills/orchestration/control", true, textField("actor", "codex", ""), textField("environment", "local", ""), textField("action", "pause", ""), optionalField("workflow_id", "", "workflow_id"), optionalField("job_id", "", "id"), field("expected_generation", "integer", "", "generation", false), optionalField("reason_code", "", ""), optionalField("idempotency_key", "", "")),
	action("client-create", "Create client profile", "Clients", "POST", "/api/v1/client-profiles", true, textField("id", "", ""), textField("display_name", "", ""), textField("client_kind", "codex", ""), textField("tool_profile", "default", "")),
	action("client-update", "Edit client profile", "Clients", "PUT", "/api/v1/client-profiles/{id}", true, textField("id", "", "id"), textField("display_name", "", "display_name"), textField("client_kind", "", "client_kind"), textField("tool_profile", "", "tool_profile"), intField("expected_revision", "", "revision")),
	action("client-delete", "Delete client profile", "Clients", "DELETE", "/api/v1/client-profiles/{id}", true, textField("id", "", "id"), intField("expected_revision", "", "revision")),
	action("graph-readiness", "Inspect graph readiness", "Graph", "GET", "/api/v1/graph-index/readiness", false, configField()),
	action("graph-status", "Inspect graph status", "Graph", "GET", "/api/v1/graph-index/status", false, configField()),
	action("graph-basic", "Browse basic memory graph", "Graph", "GET", "/api/v1/graph", false),
	action("graph-operation", "Enable, update, rebuild or control graph", "Graph", "POST", "/api/v1/graph-index/operations", true, configField(), textField("action", "enable", ""), optionalField("idempotency_key", "", ""), optionalField("expected_revision", "", "revision"), optionalField("job_id", "", "id")),
	action("graph-review", "Review graph evidence", "Graph", "POST", "/api/v1/graph-index/review", true, textField("target_kind", "entity", ""), textField("target_id", "", "id"), textField("action", "annotate", ""), textField("from", "", "trust_state"), textField("to", "", "trust_state"), intField("expected_version", "", "version"), optionalField("reason", "", "")),
	action("graph-feedback", "Submit graph feedback", "Graph", "POST", "/api/v1/graph-index/feedback", true, textField("request_id", "", "request_id"), textField("target_kind", "entity", ""), textField("target_id", "", "id"), textField("outcome", "helpful", ""), optionalField("reason", "", "")),
	action("llm-test", "Test local model settings", "Settings", "POST", "/api/v1/library/local-llm/test", false, boolField("enabled", "true", "enabled"), textField("base_url", "http://127.0.0.1:11434/v1", "base_url"), textField("text_model", "", "text_model"), optionalField("vision_model", "", "vision_model"), field("api_key", "secret", "", "", false), boolField("clear_api_key", "false", ""), intField("timeout_seconds", "3", "timeout_seconds")),
	action("llm-save", "Save local model settings", "Settings", "PUT", "/api/v1/library/local-llm", true, boolField("enabled", "true", "enabled"), textField("base_url", "http://127.0.0.1:11434/v1", "base_url"), textField("text_model", "", "text_model"), optionalField("vision_model", "", "vision_model"), field("api_key", "secret", "", "", false), boolField("clear_api_key", "false", ""), intField("timeout_seconds", "3", "timeout_seconds")),
	action("project-init", "Register a workspace", "Workspaces", "POST", "/api/v1/projects/init", true, textField("project_name", "", ""), textField("cwd", "", ""), boolField("study", "false", ""), boolField("no_rule", "false", "")),
	action("project-rename", "Rename selected workspace", "Workspaces", "POST", "/api/v1/projects/rename", true, textField("to", "", "")),
	action("project-delete", "Delete selected workspace", "Workspaces", "POST", "/api/v1/projects/delete", true, boolField("keep_data", "true", "")),
	action("portable-export", "Export encrypted portable data", "Data", "POST", "/api/v1/migrations/portable-export", true, field("passphrase", "secret", "", "", true), field("output_file", "output", "", "", true)),
	action("memory-export", "Export memories as JSON", "Data", "GET", "/api/v1/memories/export", false, field("output_file", "output", "", "", true)),
	action("memory-import", "Import memory data", "Data", "POST", "/api/v1/memories/import", true, field("input_file", "input", "", "", true)),
	action("memory-reconstruct", "Reconstruct archived memories", "Data", "POST", "/api/v1/memories/reconstruct", true, textField("query", "", ""), boolField("confirm", "false", "")),
	action("rights-accept", "Accept the displayed rights policy", "Rights", "POST", "/api/v1/rights-attestation/accept", true, textField("policy_version", "", "policy.version"), field("accepted_statement_ids", "array", "", "", true)),
	action("advisor", "Inspect workspace advisor", "Diagnostics", "GET", "/api/v1/advisor", false),
	action("audit", "Inspect audit events", "Diagnostics", "GET", "/api/v1/audit", false, intField("limit", "100", "")),
	action("usage", "Inspect model usage", "Diagnostics", "GET", "/api/v1/llm-usage", false),
	action("replay-events", "Inspect replay events", "Replay", "GET", "/api/v1/replay/events", false, textField("session_id", "", "session_id"), intField("limit", "100", ""), optionalField("cursor", "", "")),
	action("replay-import", "Import replay JSONL", "Replay", "POST", "/api/v1/replay/import-jsonl", true, textField("path", "", "")),
	action("observations-promote", "Promote observations", "Observations", "POST", "/api/v1/observations/promote", true, textField("session_id", "", "session_id"), intField("max_items", "100", ""), textField("type", "episodic", ""), optionalField("from", "", ""), optionalField("to", "", "")),
}

func actionByID(id string) Action {
	for _, item := range consoleActions {
		if item.ID == id {
			return item
		}
	}
	return Action{}
}
func actionsForScreen(name string) []Action {
	result := []Action{}
	for _, item := range consoleActions {
		if item.Screen == name || (name == "Sources" && (item.ID == "note-retry" || item.ID == "note-trash" || item.ID == "note-delete")) {
			result = append(result, item)
		}
	}
	return result
}
