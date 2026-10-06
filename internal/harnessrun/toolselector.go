package harnessrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// ServiceToolSelector routes tool selection through the decision service of the run, which
// bounds, validates, journals and pauses it. It sees only tool names, which the harness
// defines; the manager still validates what comes back.
type ServiceToolSelector struct {
	// For returns the run's decision service, or nil when there is none.
	For func(runID string) *harnessdecide.Service
}

var _ ToolSelector = ServiceToolSelector{}

func (s ServiceToolSelector) Select(ctx context.Context, runID string, offered []string, keep int) ([]string, error) {
	if s.For == nil {
		return nil, errors.New("no decision service")
	}
	service := s.For(runID)
	if service == nil {
		return nil, errors.New("no decision service")
	}
	items := make([]harnessdecide.Item, len(offered))
	for i, id := range offered {
		items[i] = harnessdecide.Item{ID: id}
	}
	chosen, out := service.Tools(ctx, items, keep)
	if out.Status != harnessdecide.StatusApplied {
		return nil, fmt.Errorf("decision %s", out.Status)
	}
	return chosen, nil
}
