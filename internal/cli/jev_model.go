package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
	"github.com/taimufuraiyaa/agent-memory/internal/modelroute"
)

// jevModelChoice returns a recommendation, never authority to execute or switch hosts.
func jevModelChoice(ctx context.Context, dataDir, host, task string, client jevDecisionClient) (string, bool) {
	question, ids, err := modelQuestion(dataDir, host)
	if err != nil {
		return "", false
	}
	token, configured, err := jevconfig.NewTokenStore(dataDir).Load(ctx)
	if err != nil || !configured {
		return "", false
	}
	state := core.TruncateUTF8(engine.RedactPrivateAndSecrets(task), 1200)
	if strings.TrimSpace(state) == "" {
		return "", false
	}
	answers, err := client.Choose(ctx, token, state, map[string]jev.ChoiceQuestion{"model": question})
	if err != nil {
		return "", false
	}
	slot, ok := confidentChoice(answers["model"], question.Criteria)
	if !ok {
		return "", false
	}
	return ids[slot], true
}

func modelQuestion(dataDir, host string) (jev.ChoiceQuestion, map[string]string, error) {
	catalog, err := modelroute.Load(dataDir)
	if err != nil {
		return jev.ChoiceQuestion{}, nil, err
	}
	candidates, err := catalog.Candidates(host)
	if err != nil {
		return jev.ChoiceQuestion{}, nil, err
	}
	criteria := make(map[string]string, len(candidates))
	ids := make(map[string]string, len(candidates))
	for i, candidate := range candidates {
		slot := fmt.Sprintf("model_%d", i+1)
		criteria[slot] = candidate.ID + ": " + candidate.Description
		ids[slot] = candidate.ID
	}
	return jev.ChoiceQuestion{Instructions: "Choose the best eligible model for this task from this host only. Consider complexity and fit; no execution authority is granted.", Criteria: criteria}, ids, nil
}
