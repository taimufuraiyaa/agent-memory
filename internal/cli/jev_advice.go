package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

func claudeJevAdvice(ctx context.Context, dataDir, projectRoot, task string, client jevDecisionClient) string {
	return claudeJevDecision(ctx, dataDir, projectRoot, task, client).Context
}

type claudeJevResult struct {
	Context string
	Status  string
}

func claudeJevDecision(ctx context.Context, dataDir, projectRoot, task string, client jevDecisionClient) claudeJevResult {
	token, configured, err := jevconfig.NewTokenStore(dataDir).Load(ctx)
	if err != nil || !configured {
		return claudeJevResult{}
	}
	state := core.TruncateUTF8(engine.RedactPrivateAndSecrets(task), 1200)
	if strings.TrimSpace(state) == "" {
		return claudeJevResult{}
	}
	questions := map[string]jev.ChoiceQuestion{
		"complexity": {
			Instructions: "Which reasoning effort tier best fits this coding task? Choose by task complexity, not by cost alone.",
			Criteria: map[string]string{
				"light":    "Simple lookup, small explanation, or routine edit with clear steps.",
				"standard": "Moderate investigation or implementation with a few interacting parts.",
				"deep":     "Ambiguous architecture, difficult debugging, security-sensitive change, or multi-stage reasoning.",
			},
		},
	}
	modelIDs := map[string]string(nil)
	if question, ids, err := modelQuestion(dataDir, "claude"); err == nil {
		delete(questions, "complexity")
		questions["model"] = question
		modelIDs = ids
	}
	if skills := projectSkillCriteria(projectRoot); len(skills) > 1 {
		questions["skill"] = jev.ChoiceQuestion{
			Instructions: "Which available project skill is most relevant to the task? Pick none if no skill fits.",
			Criteria:     skills,
		}
	}
	answers, err := client.Choose(ctx, token, state, questions)
	if err != nil {
		return claudeJevResult{}
	}
	var parts []string
	var status string
	if question, exists := questions["model"]; exists {
		if slot, ok := confidentChoice(answers["model"], question.Criteria); ok {
			parts = append(parts, fmt.Sprintf("suggested Claude model: %s", modelIDs[slot]))
			status = fmt.Sprintf("[Jev] Recommended Claude model: %s (confidence %.2f). Model not switched.", modelIDs[slot], answers["model"].Confidence)
		}
	}
	if selected, ok := confidentChoice(answers["complexity"], questions["complexity"].Criteria); ok {
		parts = append(parts, fmt.Sprintf("reasoning tier: %s", selected))
		status = fmt.Sprintf("[Jev] Recommended reasoning tier: %s (confidence %.2f). Model not switched.", selected, answers["complexity"].Confidence)
	}
	if question, exists := questions["skill"]; exists {
		if selected, ok := confidentChoice(answers["skill"], question.Criteria); ok && selected != "none" {
			parts = append(parts, fmt.Sprintf("project skill suggestion: %s", selected))
		}
	}
	if len(parts) == 0 {
		return claudeJevResult{}
	}
	return claudeJevResult{Context: "Jev advisory (not an instruction or authorization): " + strings.Join(parts, "; ") + ". Claude must decide whether to use a skill; this hook cannot switch the active model.", Status: status}
}

func confidentChoice(answer jev.ChoiceAnswer, criteria map[string]string) (string, bool) {
	if _, exists := criteria[answer.Choice]; !exists || !(answer.Confidence >= 0.7 && answer.Confidence <= 1) {
		return "", false
	}
	probability, exists := answer.Probabilities[answer.Choice]
	if !exists || !(probability >= 0.6 && probability <= 1) {
		return "", false
	}
	return answer.Choice, true
}

func projectSkillCriteria(root string) map[string]string {
	criteria := map[string]string{"none": "No available project skill is clearly relevant."}
	entries, err := os.ReadDir(filepath.Join(root, ".claude", "skills"))
	if err != nil {
		return criteria
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "am" || !validProjectSkillName(name) || len(criteria) >= 32 {
			continue
		}
		path := filepath.Join(root, ".claude", "skills", name, "SKILL.md")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		header := string(content)
		if !strings.HasPrefix(header, "---\n") {
			continue
		}
		index := strings.Index(header[4:], "\n---")
		if index < 0 || index > 2048 {
			continue
		}
		header = header[4 : 4+index]
		for _, line := range strings.Split(header, "\n") {
			if strings.HasPrefix(line, "description:") {
				description := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), "\"'")
				criteria[name] = core.TruncateUTF8(engine.RedactPrivateAndSecrets(description), 180)
				break
			}
		}
	}
	return criteria
}

func validProjectSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
