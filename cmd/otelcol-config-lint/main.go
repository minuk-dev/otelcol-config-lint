// Command otelcol-config-lint validates OpenTelemetry Collector config files against
// a specific collector release.
package main

import (
	"os"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
)

func main() {
	os.Exit(otelcolconfiglint.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
