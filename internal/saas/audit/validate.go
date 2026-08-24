package audit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	unsafeKey = regexp.MustCompile(`(?i)^(content|prompt|response|raw_text|full_text|source_bytes|password|secret|access_token|refresh_token|authorization)$`)
	unsafeVal = regexp.MustCompile(`(?i)^(Bearer\s|AKIA[0-9A-Z]{16}|-----BEGIN\s)`)
)

func ValidateMetadata(value map[string]any) error {
	return validateObject(value, 0)
}

func validateObject(value map[string]any, depth int) error {
	if depth > 6 {
		return errors.New("audit metadata exceeds nesting limit")
	}
	for key, item := range value {
		if unsafeKey.MatchString(strings.TrimSpace(key)) {
			return fmt.Errorf("unsafe audit metadata key %q", key)
		}
		switch typed := item.(type) {
		case string:
			if len(typed) > 256 || unsafeVal.MatchString(typed) {
				return fmt.Errorf("unsafe audit metadata value for %q", key)
			}
		case map[string]any:
			if err := validateObject(typed, depth+1); err != nil {
				return err
			}
		case []any:
			for _, child := range typed {
				if nested, ok := child.(map[string]any); ok {
					if err := validateObject(nested, depth+1); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
