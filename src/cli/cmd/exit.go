package cmd

import (
	"errors"
	"os/exec"
)

// ExitError wraps an error with a process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// SilentExitError signals a non-zero exit without printing anything.
// Used when the error has already been rendered (e.g. summary + Exit Reason).
type SilentExitError struct {
	Code int
}

func (e *SilentExitError) Error() string { return "" }

// silentExit wraps err in a SilentExitError, preserving exit codes from
// exec.ExitError and avoiding double-wrapping.
func silentExit(err error) error {
	var silent *SilentExitError
	if errors.As(err, &silent) {
		return err
	}
	code := 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	return &SilentExitError{Code: code}
}
