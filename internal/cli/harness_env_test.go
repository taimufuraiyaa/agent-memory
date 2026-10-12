package cli

import (
	"os"
	"strings"
	"testing"
)

// The harness composition reads AGENT_MEMORY_HARNESS_* from the environment. A developer's
// own shell may set them for real use, which would change what these tests compose, so
// every test starts from none of them and sets what it needs with t.Setenv.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		if name, _, ok := strings.Cut(entry, "="); ok && strings.HasPrefix(name, "AGENT_MEMORY_HARNESS_") {
			_ = os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}
