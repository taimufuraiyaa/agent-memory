//go:build unix

package application

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func configureGraphProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateGraphProcessGroup(pid int, grace time.Duration) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	<-timer.C
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func graphProcessGroupUsage(pid int) (int64, time.Duration, error) {
	output, err := exec.Command("/bin/ps", "-o", "rss=", "-o", "time=", "-g", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0, err
	}
	var memory int64
	var cpu time.Duration
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		kilobytes, parseErr := strconv.ParseInt(fields[0], 10, 64)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("parse process memory: %w", parseErr)
		}
		elapsed, parseErr := parseGraphCPUTime(fields[1])
		if parseErr != nil {
			return 0, 0, parseErr
		}
		memory += kilobytes * 1024
		cpu += elapsed
	}
	return memory, cpu, nil
}
