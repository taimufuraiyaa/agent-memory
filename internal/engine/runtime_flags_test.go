package engine

import "testing"

func TestLibraryRecoveryGateIsEnabledByDefaultWithEmergencyDisable(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "")
	if !LibraryEnabled() {
		t.Fatal("library should be enabled after I4 drills")
	}
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "false")
	if LibraryEnabled() {
		t.Fatal("emergency disable flag ignored")
	}
}
