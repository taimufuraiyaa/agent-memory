package retention

import "testing"

func TestDataClassesAreUniqueAndExplicit(t *testing.T) {
	seen := map[string]bool{}
	for _, class := range DataClasses {
		if class == "" || seen[class] {
			t.Fatalf("invalid data class %q", class)
		}
		seen[class] = true
	}
	for _, required := range []string{"source_originals", "source_derived", "audit_events", "backups", "billing_records"} {
		if !seen[required] {
			t.Errorf("missing policy class %s", required)
		}
	}
}
