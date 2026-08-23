// Package cmdutil holds what the commands share but no one command owns: the
// options every command takes, the codes a run ends in, and, in the package
// under it, the settings file they all read.
//
// It sits outside pkg/cmd on purpose. Everything there is a command, so a
// directory there that is not one reads like a subcommand that does not exist.
//
// It states no errors. An error belongs to the command that returns it, where
// its name says who raises it; what maps those errors to the codes below is the
// root command, which is the only place that knows all of them.
package cmdutil

// The codes a run can end in, following the convention linters are expected to
// use in CI. cmdutil states them because "run" prints them in its help and the
// root command returns them.
const (
	// ExitOK reports that every file passed.
	ExitOK = 0
	// ExitInvalid reports that at least one file failed the gate.
	ExitInvalid = 1
	// ExitUsage reports that the command could not run at all.
	ExitUsage = 2
)
