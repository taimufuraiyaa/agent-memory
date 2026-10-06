//go:build unix

package harnessexec

import (
	"os/signal"
	"syscall"
)

func ignoreTerm() { signal.Ignore(syscall.SIGTERM) }
