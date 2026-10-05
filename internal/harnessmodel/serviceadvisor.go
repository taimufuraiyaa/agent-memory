package harnessmodel

import (
	"context"
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// ServiceAdvisor routes the router's model preference through the decision service, which
// bounds, validates, journals and pauses it. The router has already excluded every provider
// that may not serve the call; the service sees only their identifiers, and the router
// still checks the answer against the same set and applies its own fallbacks.
type ServiceAdvisor struct{ Service *harnessdecide.Service }

var _ Advisor = ServiceAdvisor{}

func (a ServiceAdvisor) Prefer(ctx context.Context, providers []harness.ProviderID) (harness.ProviderID, float64, error) {
	if a.Service == nil {
		return "", 0, errors.New("no decision service")
	}
	items := make([]harnessdecide.Item, len(providers))
	for i, p := range providers {
		items[i] = harnessdecide.Item{ID: string(p)}
	}
	chosen, out := a.Service.Model(ctx, items)
	if out.Status != harnessdecide.StatusApplied {
		return "", 0, fmt.Errorf("decision %s", out.Status)
	}
	return harness.ProviderID(chosen), out.Confidence, nil
}
