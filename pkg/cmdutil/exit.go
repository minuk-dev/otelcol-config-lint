// Package cmdutil holds what the commands share but no one command owns: the
// options every command takes, the exit contract the binary reports, and, in
// the packages under it, the settings file and the flag groups more than one
// command declares.
//
// It sits outside pkg/cmd on purpose. Everything there is a command, so a
// directory there that is not one reads like a subcommand that does not exist.
package cmdutil

import "errors"

// ErrFilesInvalid ends a run with ExitInvalid. It carries no message worth
// printing: the formatter has already reported every finding, so the caller
// should map it to an exit code and stay quiet.
var ErrFilesInvalid = errors.New("at least one file is invalid")

// The codes a run can end in, following the convention linters are expected to
// use in CI.
const (
	// ExitOK reports that every file passed.
	ExitOK = 0
	// ExitInvalid reports that at least one file failed the gate.
	ExitInvalid = 1
	// ExitUsage reports that the command could not run at all.
	ExitUsage = 2
)

// ExitCode maps the error a command run returned to a process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrFilesInvalid):
		return ExitInvalid
	default:
		return ExitUsage
	}
}
