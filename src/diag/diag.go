package diag

import (
	"fmt"
	"os"
)

// Error writes an error diagnostic to stderr.
// Use for hard module failures where configured contract could not be fulfilled.
func Error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
}

// Warn writes a warning diagnostic to stderr.
// Use for non-fatal degradation the user should know about.
func Warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Info writes an informational diagnostic to stderr.
// Use for notable fallback paths that succeeded but via a secondary method.
func Info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Debug writes a verbose trace to stderr when enabled.
// Use for exec traces, internal state, fallback reasoning. Gate calls with
// Verbose() so traces stay out of normal structured output: Debug(Verbose(), ...).
func Debug(verbose bool, format string, args ...any) {
	if !verbose {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Verbose reports whether debug tracing is enabled, via the SF_DEBUG env var
// (any non-empty value). It is the single toggle for Debug() calls so internal
// traces are silent in normal runs and appear only when debugging.
func Verbose() bool {
	return os.Getenv("SF_DEBUG") != ""
}
