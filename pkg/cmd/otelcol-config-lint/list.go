package otelcolconfiglint

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// newListCommand builds "list" and its subcommands. Each one carries only the
// flags that change what it prints, so their help stays short.
func newListCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print what the linter knows about",
		Example: `  otelcol-config-lint list rules
  otelcol-config-lint list versions`,
	}

	cmd.AddCommand(opts.newListRulesCommand(), opts.newListVersionsCommand())

	return cmd
}

// newListRulesCommand builds "list rules". The severity flags apply because
// they decide the severity the rules will actually run at.
func (o *Options) newListRulesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Print the rules and their default severities",
		Long: "Print the rules and their default severities.\n\n" +
			"A severity changed by --disable, --severity or the settings file is\n" +
			"marked as overridden.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := o.Prepare(cmd)
			if err != nil {
				return err
			}

			return o.runListRules(cmd)
		},
	}

	o.registerSettingsFlag(cmd)
	o.registerRuleFlags(cmd)

	return cmd
}

// newListVersionsCommand builds "list versions". --collector-version is
// deliberately absent: this is the command that says which ones exist.
func (o *Options) newListVersionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Print the available schema versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := o.Prepare(cmd)
			if err != nil {
				return err
			}

			return o.runListVersions(cmd)
		},
	}

	o.registerSettingsFlag(cmd)
	o.registerSchemaLocationFlag(cmd)
	o.registerDistributionFlag(cmd)

	return cmd
}

func (o *Options) runListRules(cmd *cobra.Command) error {
	overrides, err := o.severityOverrides()
	if err != nil {
		return err
	}

	w := newColumns(cmd.OutOrStdout())

	for _, r := range rule.All() {
		sev := r.Severity()

		note := ""
		if s, ok := overrides[r.Name()]; ok && s != sev {
			sev, note = s, " (overridden)"
		}

		w.row(r.Name(), string(sev)+note, r.Description())
	}

	w.flush()

	return nil
}

func (o *Options) runListVersions(cmd *cobra.Command) error {
	versions := o.store.Versions()
	if len(versions) == 0 {
		return ErrNoSchemas
	}

	w := newColumns(cmd.OutOrStdout())

	for i, v := range versions {
		cat, err := o.store.Load(v)
		if err != nil {
			w.row(v, "unreadable: "+err.Error(), "")

			continue
		}

		note := ""
		if i == 0 {
			note = "(latest)"
		}

		w.row(v, fmt.Sprintf("%d components", cat.Count()),
			strings.Join(cat.Distributions, "+")+" "+note)
	}

	w.flush()

	return nil
}

// columns renders the aligned output the list subcommands print.
type columns struct{ w *tabwriter.Writer }

// columnPadding is the gap between columns, in spaces.
const columnPadding = 2

func newColumns(w io.Writer) columns {
	return columns{w: tabwriter.NewWriter(w, 0, 0, columnPadding, ' ', 0)}
}

func (c columns) row(cells ...string) {
	for i, cell := range cells {
		if i > 0 {
			_, _ = io.WriteString(c.w, "\t")
		}

		_, _ = io.WriteString(c.w, cell)
	}

	_, _ = io.WriteString(c.w, "\n")
}

func (c columns) flush() { _ = c.w.Flush() }
