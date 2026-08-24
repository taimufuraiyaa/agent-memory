package api

import (
	"slices"
	"testing"
)

func TestLocalSystemToolsFeatureRequiresEveryServiceContract(t *testing.T) {
	projects := &localProjectFixture{}
	if localSystemToolsAvailable(projects) {
		t.Fatal("project service without client-profile support advertised local system tools")
	}
	if !localSystemToolsAvailable(&completeLocalSystemFixture{localProjectFixture: projects}) {
		t.Fatal("complete local system service did not advertise local system tools")
	}
}

func TestHostedDashboardFeaturesSeparateOnboardingFromSystemTools(t *testing.T) {
	owner := &localOwnerFixture{}
	incomplete := hostedDashboardFeatures(Dependencies{LocalOwner: owner, LocalProjects: &localProjectFixture{}})
	if !slices.Contains(incomplete, "local_onboarding") || slices.Contains(incomplete, "local_system_tools") {
		t.Fatalf("incomplete service features=%v", incomplete)
	}

	complete := hostedDashboardFeatures(Dependencies{LocalOwner: owner, LocalProjects: &completeLocalSystemFixture{localProjectFixture: &localProjectFixture{}}})
	if !slices.Contains(complete, "local_onboarding") || !slices.Contains(complete, "local_system_tools") {
		t.Fatalf("complete service features=%v", complete)
	}
}

type completeLocalSystemFixture struct {
	*localProjectFixture
	localClientProfileFixture
}
