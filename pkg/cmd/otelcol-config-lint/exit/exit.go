// Package exit is the exit contract the binary and every command agree on:
// the codes a run can end in, and the error that means findings rather than
// failure.
package exit

import "errors"

// ErrFilesInvalid ends a run with Invalid. It carries no message worth
// printing: the formatter has already reported every finding, so the caller
// should map it to an exit code and stay quiet.
var ErrFilesInvalid = errors.New("at least one file is invalid")

// The codes a run can end in, following the convention linters are expected to
// use in CI.
const (
	// OK reports that every file passed.
	OK = 0
	// Invalid reports that at least one file failed the gate.
	Invalid = 1
	// Usage reports that the command could not run at all.
	Usage = 2
)

// Code maps the error a command run returned to a process exit code.
func Code(err error) int {
	switch {
	case err == nil:
		return OK
	case errors.Is(err, ErrFilesInvalid):
		return Invalid
	default:
		return Usage
	}
}
