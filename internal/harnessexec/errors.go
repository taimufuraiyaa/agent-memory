package harnessexec

import "errors"

// ErrUnsupported: this platform has no process groups, so commands are refused rather than
// half supported.
var ErrUnsupported = errors.New("running commands is not supported on this platform")
