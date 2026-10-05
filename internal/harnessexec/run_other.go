//go:build !unix

package harnessexec

import (
	"context"
	"errors"
	"time"
)

const (
	MaxTimeout   = 10 * time.Minute
	DefaultGrace = 2 * time.Second
	MinOutput    = 1 << 10
	MaxOutputCap = 1 << 20
)

var (
	ErrInvalid = errors.New("invalid command specification")
	ErrStart   = errors.New("the command could not be started")
)

type Spec struct {
	Program   string
	Args      []string
	Dir       string
	Env       []string
	Timeout   time.Duration
	MaxOutput int
	Grace     time.Duration
	Split     bool
}

type Result struct {
	Started       bool
	ExitCode      int
	Signal        string
	TimedOut      bool
	Cancelled     bool
	Incomplete    bool
	OmittedBytes  int64
	Output        string
	Duration      time.Duration
	Stdout        []byte
	StdoutOmitted int64
}

func Run(context.Context, Spec) (Result, error) { return Result{ExitCode: -1}, ErrUnsupported }

// Supported reports whether this platform can run commands.
func Supported() bool { return false }
