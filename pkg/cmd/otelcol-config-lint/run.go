package otelcolconfiglint

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newRunCommand builds "run", the command that actually lints. It is where
// every lint flag lives, and the only one whose exit code carries findings.
func newRunCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{ //nolint:exhaustruct // cobra's zero values are the defaults we want
		Use:   "run [flags] <file|dir|->...",
		Short: "Lint the given config files",
		Example: `  otelcol-config-lint run config.yaml
  otelcol-config-lint run --collector-version v0.157.0 --summary ./configs
  cat config.yaml | otelcol-config-lint run -
  otelcol-config-lint run --output json --strict ./configs > report.json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := opts.Prepare(cmd)
			if err != nil {
				return err
			}

			err = opts.Run(cmd, args)
			if err != nil {
				return err
			}

			return nil
		},
	}

	// Only this command can end in anything but 0 or 2, so the footer belongs
	// here rather than on the root where every subcommand would inherit it.
	cmd.SetUsageTemplate(cmd.UsageTemplate() + fmt.Sprintf(`
Exit codes:
  %d  every file passed
  %d  at least one file failed
  %d  the command could not run
`, ExitOK, ExitInvalid, ExitUsage))

	opts.RegisterFlags(cmd)

	return cmd
}
