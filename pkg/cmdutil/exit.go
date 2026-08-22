// Package cmdutil holds what the commands share but no one command owns: the
// options every command takes, the exit contract the binary reports, and, in
// the packages under it, the settings file and the flag groups more than one
// command declares.
//
// It sits outside pkg/cmd on purpose. Everything there is a command, so a
// directory there that is not one reads like a subcommand that does not exist.
package cmdutil

import "errors"

// ErrUsage is what a failure that is not findings resolves to: the command
// could not run as asked. It is never returned on its own -- a package states
// its own error with NewUsageError, or marks one in hand with AsUsageError --
// so a caller can tell "the invocation was wrong" from "the environment
// failed" without knowing which command raised what. An error carrying neither
// this nor ErrFilesInvalid is the latter.
var ErrUsage = errors.New("the command could not run")

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

// NewUsageError declares a usage failure: the static error a package states
// once and returns wherever the condition arises, already marked.
func NewUsageError(text string) error {
	//nolint:err113 // building the static errors packages declare is what this is for
	return usageError{err: errors.New(text)}
}

// AsUsageError marks an error already in hand. The mark adds nothing to the
// message the user sees: errors.Is finds ErrUsage through it, and whatever err
// already was through it as well.
func AsUsageError(err error) error {
	if err == nil {
		return nil
	}

	return usageError{err: err}
}

// usageError is the mark itself. It answers to ErrUsage and otherwise gets out
// of the way, which is what keeps a marked error reading as the sentence the
// package that raised it wrote.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }

func (e usageError) Unwrap() error { return e.err }

func (e usageError) Is(target error) bool { return target == ErrUsage }

// ExitCode maps the error a command run returned to a process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrFilesInvalid):
		return ExitInvalid
	default:
		// Anything else could not run, whether it was marked with ErrUsage or
		// not: a failure that is not findings is not a clean run either.
		return ExitUsage
	}
}
