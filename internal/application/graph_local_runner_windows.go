//go:build windows

package application

import (
	"fmt"
	"os/exec"
	"time"
)

func configureGraphProcessGroup(_ *exec.Cmd) {}
func terminateGraphProcessGroup(pid int, _ time.Duration) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid)).Run()
}
func graphProcessGroupUsage(_ int) (int64, time.Duration, error) { return 0, 0, nil }
