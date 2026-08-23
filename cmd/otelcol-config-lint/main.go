// Command otelcol-config-lint validates OpenTelemetry Collector config files against
// a specific collector release.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
)

func main() {
	// The exit code is decided in run so that the signal handler is torn down
	// before the process ends; os.Exit runs no deferred call.
	os.Exit(run())
}

// run executes the command and reports the code the process should end with.
func run() int {
	// The run is carried on a context that an interrupt cancels, so a schema
	// fetch that is still in flight ends with the rest of the command instead
	// of holding the terminal until the client times out.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := otelcolconfiglint.NewCommand(nil)
	cmd.SetArgs(os.Args[1:])
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	err := cmd.ExecuteContext(ctx)
	if err != nil && !errors.Is(err, otelcolconfiglint.ErrFilesInvalid) {
		// Findings have already been printed by the formatter, so only
		// command-level failures are worth a message here.
		cmd.PrintErrf("otelcol-config-lint: %v\n", err)
	}

	return otelcolconfiglint.ExitCode(err)
}
