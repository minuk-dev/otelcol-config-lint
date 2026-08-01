// Command otelcol-config-lint validates OpenTelemetry Collector config files against
// a specific collector release.
package main

import (
	"os"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
)

func main() {
	cmd := otelcolconfiglint.NewCommand(
		&otelcolconfiglint.Options{},
	)
	err := cmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
