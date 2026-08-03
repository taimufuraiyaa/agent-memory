//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

func configureBackgroundProcess(cmd *exec.Cmd) {
	const (
		detachedProcess       = 0x00000008
		createNewProcessGroup = 0x00000200
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}
