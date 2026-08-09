package audit

import "testing"

func TestValidateMetadataRejectsContentAndCredentials(t *testing.T) {
	for _, value := range []map[string]any{
		{"content": "book text"},
		{"nested": map[string]any{"prompt": "private"}},
		{"header": "Bearer abc"},
	} {
		if err := ValidateMetadata(value); err == nil {
			t.Fatalf("ValidateMetadata(%v) accepted unsafe value", value)
		}
	}
	if err := ValidateMetadata(map[string]any{"job_state": "failed", "attempts": 2}); err != nil {
		t.Fatalf("safe metadata rejected: %v", err)
	}
}
