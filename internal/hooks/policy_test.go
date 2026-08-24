package hooks

import "testing"

func TestResolvePolicySeparatesCaptureAndInjection(t *testing.T) {
	t.Setenv("AGENT_MEMORY_CAPTURE_ENABLED", "1")
	t.Setenv("AGENT_MEMORY_SESSION_INJECTION_ENABLED", "0")
	t.Setenv("AGENT_MEMORY_PROMPT_INJECTION_ENABLED", "1")
	t.Setenv("AGENT_MEMORY_INJECTION_BUDGET", "321")

	policy := ResolvePolicy()

	if !policy.CaptureEnabled || policy.SessionInjectionEnabled || !policy.PromptInjectionEnabled || policy.InjectionBudget != 321 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
}

func TestResolvePolicyUsesSafeDefaults(t *testing.T) {
	for _, name := range []string{"AGENT_MEMORY_CAPTURE_ENABLED", "AGENT_MEMORY_SESSION_INJECTION_ENABLED", "AGENT_MEMORY_PROMPT_INJECTION_ENABLED", "AGENT_MEMORY_INJECTION_BUDGET"} {
		t.Setenv(name, "")
	}
	policy := ResolvePolicy()
	if !policy.CaptureEnabled || policy.SessionInjectionEnabled || policy.PromptInjectionEnabled || policy.InjectionBudget != 800 {
		t.Fatalf("unsafe defaults: %+v", policy)
	}
}
