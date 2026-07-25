//go:build !windows

package cli

import (
	"os/exec"
	"testing"
)

func TestConfigureBackgroundProcessCreatesDetachedSession(t *testing.T) {
	cmd := exec.Command("agent-memory", "dashboard")

	configureBackgroundProcess(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("expected background process to start in a detached session")
	}
}
