package source

import (
	"context"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type rolloutGateFixture struct{ enabled bool }

func (g rolloutGateFixture) FeatureEnabled(context.Context, auth.RequestContext, string) (bool, error) {
	return g.enabled, nil
}

func TestUploadGrantStopsBeforeCustodyWhenRolloutPausesUploads(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	service.SetRolloutGate(rolloutGateFixture{enabled: false})
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:write": {}}})
	_, err := service.Issue(ctx, GrantRequest{})
	if err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("paused rollout error=%v", err)
	}
}
