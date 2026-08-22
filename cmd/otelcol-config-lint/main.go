// Command otelcol-config-lint validates OpenTelemetry Collector config files against
// a specific collector release.
package main

import (
	"errors"
	"os"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
)

func main() {
	cmd := otelcolconfiglint.NewCommand(nil)
	cmd.SetArgs(os.Args[1:])
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	err := cmd.Execute()
	if err != nil && !errors.Is(err, cmdutil.ErrFilesInvalid) {
		// Findings have already been printed by the formatter, so only
		// command-level failures are worth a message here.
		cmd.PrintErrf("otelcol-config-lint: %v\n", err)
	}

	os.Exit(cmdutil.ExitCode(err))
}
