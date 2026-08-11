package dashboard

import (
	"os"
	"strings"
	"testing"
)

func TestDockerContextIncludesEmbeddedDashboardDistribution(t *testing.T) {
	value, err := os.ReadFile("../../../.dockerignore")
	if err != nil {
		t.Fatalf("read Docker ignore rules: %v", err)
	}
	rules := string(value)
	for _, exception := range []string{"!internal/api/dashboard/dist", "!internal/api/dashboard/dist/**"} {
		if !strings.Contains(rules, exception) {
			t.Fatalf("Docker context must retain embedded dashboard assets with %q", exception)
		}
	}
}
