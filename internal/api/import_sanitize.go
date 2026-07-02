package api

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

// sanitizeImportedMemory applies the same input-validation, secret/PII
// redaction, and content-safety checks that the write pipeline applies to
// newly written memories (see internal/validation and
// internal/engine.RedactPrivateAndSecrets / RegexSecurityFilter) to a memory
// arriving via /api/v1/memories/import.
//
// Without this, an imported bundle from an untrusted source could inject
// unredacted secrets, oversized content, or invalid records directly into
// the store, bypassing the protections normal writes go through.
//
// It mutates m in place (assigning an ID if missing, and redacting Content
// and any Diagram code) and returns a non-empty skip reason if the memory
// should not be imported.
func sanitizeImportedMemory(ctx context.Context, m *core.MemoryEntry, filter engine.SecurityFilter) string {
	if strings.TrimSpace(m.ID) == "" {
		m.ID = uuid.NewString()
	}
	if err := validation.ValidateWorkspaceName(m.Workspace); err != nil {
		return "invalid workspace: " + err.Error()
	}
	if err := validation.ValidateContentLength(m.Content); err != nil {
		return "invalid content: " + err.Error()
	}
	if m.Diagram != nil && m.Diagram.Code != "" {
		if err := validation.ValidateDiagramCode(m.Diagram.Code); err != nil {
			return "invalid diagram: " + err.Error()
		}
	}

	// Redact secrets/private blocks before persisting or running the
	// security filter, mirroring what the write pipeline does for new
	// memories.
	m.Content = engine.RedactPrivateAndSecrets(m.Content)
	if m.Diagram != nil && m.Diagram.Code != "" {
		m.Diagram.Code = engine.RedactPrivateAndSecrets(m.Diagram.Code)
	}

	validationContent := m.Content
	if m.Diagram != nil && strings.TrimSpace(m.Diagram.Code) != "" {
		validationContent = strings.TrimSpace(validationContent) + "\n" + m.Diagram.Code
	}
	if filter != nil {
		if err := filter.Validate(ctx, engine.SecurityValidationInput{
			Workspace: m.Workspace,
			Content:   validationContent,
			Tags:      m.Tags,
		}); err != nil {
			return "rejected by security filter: " + err.Error()
		}
	}

	if err := m.Validate(); err != nil {
		return "invalid memory: " + err.Error()
	}
	return ""
}
