package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

func observeEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBSERVE_ENABLED")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func memoryEnabled() bool {
	return engine.MemoryEnabled()
}

func buildObservationSummary(req ObserveRequest) string {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case "session_start":
		return "session_start"
	case "session_end":
		return "session_end"
	}
	tool := strings.TrimSpace(req.ToolName)
	prompt := strings.TrimSpace(req.Prompt)

	var b strings.Builder
	if kind != "" {
		b.WriteString(kind)
	}
	if tool != "" {
		if b.Len() > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("tool=")
		b.WriteString(tool)
	}
	if prompt != "" {
		if b.Len() > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("prompt=")
		b.WriteString(engine.ClipString(prompt, 240))
	}
	if req.ToolInput != nil {
		if input := stringifyJSON(req.ToolInput); strings.TrimSpace(input) != "" {
			if b.Len() > 0 {
				b.WriteString(" | ")
			}
			b.WriteString("input=")
			b.WriteString(engine.ClipString(input, 320))
		}
	}
	if b.Len() == 0 {
		return kind
	}
	return b.String()
}

func stringifyJSON(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func computeObservationHash(workspace, sessionID, kind, toolName, summary string) string {
	h := sha256.New()
	parts := []string{workspace, sessionID, kind, toolName, summary}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
