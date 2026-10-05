//go:build unix

// Package harnessexec runs one approved command and nothing more. It is a bounded runner,
// not a sandbox: the process runs as the user and can do what the user can do. What it
// guarantees is the envelope around the process: it receives only the environment it is
// given, has no standard input, runs in its own process group in a checked directory, is
// stopped by a timeout, a cancellation or a shutdown, leaves no stray children in its group,
// and returns bounded, control-character-free output.
package harnessexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxTimeout is the longest a command may run.
	MaxTimeout = 10 * time.Minute
	// DefaultGrace is how long a terminated process group has to exit before it is killed.
	DefaultGrace = 2 * time.Second
	// MinOutput and MaxOutputCap bound the output capture.
	MinOutput    = 1 << 10
	MaxOutputCap = 1 << 20
	maxArgs      = 128
	maxEnv       = 64
	maxEnvBytes  = 32 << 10
)

var (
	// ErrInvalid: the specification was refused before anything started.
	ErrInvalid = errors.New("invalid command specification")
	// ErrStart: the process could not be started.
	ErrStart = errors.New("the command could not be started")
)

// Spec describes one run. Everything is explicit: the program is an absolute path, the
// environment is the whole environment, and nothing is looked up or inherited.
type Spec struct {
	Program   string
	Args      []string
	Dir       string
	Env       []string
	Timeout   time.Duration
	MaxOutput int
	// Grace overrides how long a terminated group has before it is killed; zero uses the default.
	Grace time.Duration
}

// Result is what happened. A command that exits non-zero is not an error: it is a result.
type Result struct {
	Started   bool
	ExitCode  int // -1 when the process was killed by a signal or never started
	Signal    string
	TimedOut  bool
	Cancelled bool
	// Incomplete reports that a process outside the group kept the output open after the
	// command ended, so the output may be missing its end.
	Incomplete   bool
	OmittedBytes int64
	Output       string
	Duration     time.Duration
}

func (s Spec) validate() error {
	if !filepath.IsAbs(s.Program) || filepath.Clean(s.Program) != s.Program {
		return fmt.Errorf("%w: program must be a clean absolute path", ErrInvalid)
	}
	if info, err := os.Stat(s.Program); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: program is not an executable file", ErrInvalid)
	}
	if !filepath.IsAbs(s.Dir) || filepath.Clean(s.Dir) != s.Dir {
		return fmt.Errorf("%w: directory must be a clean absolute path", ErrInvalid)
	}
	if real, err := filepath.EvalSymlinks(s.Dir); err != nil || real != s.Dir {
		return fmt.Errorf("%w: directory must be a real directory", ErrInvalid)
	}
	if info, err := os.Stat(s.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: directory is not a directory", ErrInvalid)
	}
	if s.Timeout <= 0 || s.Timeout > MaxTimeout {
		return fmt.Errorf("%w: timeout", ErrInvalid)
	}
	if s.MaxOutput < MinOutput || s.MaxOutput > MaxOutputCap {
		return fmt.Errorf("%w: output cap", ErrInvalid)
	}
	if len(s.Args) > maxArgs {
		return fmt.Errorf("%w: too many arguments", ErrInvalid)
	}
	for _, a := range s.Args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("%w: NUL in an argument", ErrInvalid)
		}
	}
	if len(s.Env) > maxEnv {
		return fmt.Errorf("%w: too many environment entries", ErrInvalid)
	}
	size := 0
	for _, e := range s.Env {
		if strings.ContainsRune(e, 0) || !strings.Contains(e, "=") || strings.HasPrefix(e, "=") {
			return fmt.Errorf("%w: environment entry", ErrInvalid)
		}
		size += len(e)
	}
	if size > maxEnvBytes {
		return fmt.Errorf("%w: environment too large", ErrInvalid)
	}
	return nil
}

// Run starts the program in its own process group and waits for it. ctx cancels the run;
// the timeout stops it too. Whether it ends normally, by timeout or by cancellation, every
// process left in its group is killed before Run returns.
func Run(ctx context.Context, spec Spec) (Result, error) {
	if err := spec.validate(); err != nil {
		return Result{ExitCode: -1}, err
	}
	if ctx.Err() != nil {
		// Already cancelled: do not start anything.
		return Result{ExitCode: -1, Cancelled: true}, nil
	}
	grace := spec.Grace
	if grace <= 0 {
		grace = DefaultGrace
	}
	out := newCapture(spec.MaxOutput)
	cmd := exec.Command(spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append([]string(nil), spec.Env...)
	cmd.Stdin = nil // the null device
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = grace

	began := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("%w: %v", ErrStart, startReason(err))
	}
	group := cmd.Process.Pid
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()

	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	result := Result{Started: true, ExitCode: -1}
	var waitErr error
	select {
	case waitErr = <-finished:
	case <-runCtx.Done():
		result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		result.Cancelled = ctx.Err() != nil
		_ = syscall.Kill(-group, syscall.SIGTERM)
		select {
		case waitErr = <-finished:
		case <-time.After(grace):
			_ = syscall.Kill(-group, syscall.SIGKILL)
			waitErr = <-finished
		}
	}
	// Whatever is still in the group is not meant to outlive the call.
	_ = syscall.Kill(-group, syscall.SIGKILL)
	result.Duration = time.Since(began)
	result.Incomplete = errors.Is(waitErr, exec.ErrWaitDelay)
	if state := cmd.ProcessState; state != nil {
		result.ExitCode = state.ExitCode()
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signal = status.Signal().String()
		}
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) && !errors.As(waitErr, &exitErr) {
		return result, fmt.Errorf("%w: %v", ErrStart, waitErr)
	}
	raw, omitted := out.result()
	result.OmittedBytes = omitted
	result.Output = sanitize(raw)
	return result, nil
}

// startReason reduces a start failure to a short fixed phrase, so a path never leaks.
func startReason(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "not found"
	}
	return "start failed"
}
