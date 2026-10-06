package engine

import "strings"

// IsHowOrientedTask reports whether a caller is explicitly asking for a
// method, sequence, or prior approach. Keeping this opt-in avoids changing the
// ranking and output of factual recall requests.
func IsHowOrientedTask(task string) bool {
	words := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	for _, word := range words {
		switch word {
		case "how", "steps", "workflow", "process", "approach", "procedure", "method":
			return true
		}
	}
	return false
}

// AppendHowRecallContext keeps the established recall block intact and adds a
// clearly separated solution-path section when one contains useful context.
func AppendHowRecallContext(base string, how HowRecallResult) string {
	if strings.TrimSpace(how.ContextBlock) == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return how.ContextBlock
	}
	return strings.TrimRight(base, "\n") + "\n\n" + how.ContextBlock
}
