// Command schemagen builds component schemas from the upstream collector
// sources.
package main

import (
	"os"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
)

func main() {
	cmd := schemagen.NewCommand(nil)
	cmd.SetArgs(os.Args[1:])
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	err := cmd.Execute()
	if err != nil {
		cmd.PrintErrf("schemagen: %v\n", err)
	}

	os.Exit(schemagen.ExitCode(err))
}
