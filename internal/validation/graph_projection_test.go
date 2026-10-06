package validation

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestValidateGraphProjectionRecordRejectsCrossWorkspaceAndUnsupportedKind(t *testing.T) {
	t.Parallel()
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	record := GraphProjectionRecordValidation{
		Scope: scope, ID: "memory-1", Kind: "memory", Fingerprint: "sha256:memory", ContentBytes: 32,
	}
	if err := ValidateGraphProjectionRecord(scope, record); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	record.Scope.WorkspaceID = "workspace-b"
	if err := ValidateGraphProjectionRecord(scope, record); err == nil {
		t.Fatal("cross-workspace projection record must fail")
	}
	record.Scope = scope
	record.Kind = "tool_payload"
	if err := ValidateGraphProjectionRecord(scope, record); err == nil {
		t.Fatal("unsupported projection kind must fail")
	}
}
