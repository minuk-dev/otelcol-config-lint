// Command otelcol-config-lint validates OpenTelemetry Collector config files against
// a specific collector release.
package main

import (
	"os"

	"github.com/minuk-dev/otelcol-config-lint/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
