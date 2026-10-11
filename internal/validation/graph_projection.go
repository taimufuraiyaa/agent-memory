package validation

import (
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const MaxGraphProjectionRecordBytes = 1 << 20

type GraphProjectionRecordValidation struct {
	Scope        core.GraphScope
	ID           string
	Kind         string
	Fingerprint  string
	ContentBytes int
}

func ValidateGraphProjectionRecord(expected core.GraphScope, record GraphProjectionRecordValidation) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if record.Scope != expected {
		return fmt.Errorf("graph projection record scope does not match request")
	}
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Fingerprint) == "" {
		return fmt.Errorf("graph projection record identity is required")
	}
	switch record.Kind {
	case "source_text", "memory", "agent_memory", "solution_summary", "approved_derived":
	default:
		return fmt.Errorf("unsupported graph projection record kind %q", record.Kind)
	}
	if record.ContentBytes < 1 || record.ContentBytes > MaxGraphProjectionRecordBytes {
		return fmt.Errorf("graph projection record content exceeds bounds")
	}
	return nil
}
