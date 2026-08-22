// Package list is the command that prints what the linter knows about: the
// rules it carries and the schema versions it can read. Each subcommand takes
// only the flags that change what it prints.
package list

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/rulepolicy"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/schemaflags"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// ErrNoSchemas reports that no schema version could be found.
var ErrNoSchemas = errors.New("no schemas available")

// NewCommand builds "list" and its subcommands. Each one carries only the
// flags that change what it prints, so their help stays short.
func NewCommand(global *settings.Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print what the linter knows about",
		Example: `  otelcol-config-lint list rules
  otelcol-config-lint list versions`,
	}

	cmd.AddCommand(
		newRulesCommand(global),
		newVersionsCommand(global),
	)

	return cmd
}

// rulesOptions is what "list rules" was asked to print. It takes the rule
// flags and nothing else: the severities it lists are the ones the rules would
// run at, and no other flag changes those.
type rulesOptions struct {
	*settings.Options

	ruleFlags rulepolicy.Flags

	// internal state
	// policy is which rules run and at what level, once the flags and the
	// rules block have both had their say.
	policy rulepolicy.Policy
}

// newRulesCommand builds "list rules". The rule flags apply because they
// decide the severity the rules will actually run at.
func newRulesCommand(global *settings.Options) *cobra.Command {
	//nolint:exhaustruct // the flags fill themselves in, and the policy is resolved by prepare
	opts := &rulesOptions{Options: global}

	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Print the rules and their default severities",
		Long: "Print the rules and their default severities.\n\n" +
			"A severity changed by --default, --enable, --disable, --severity or\n" +
			"the settings file is marked as overridden; a rule that will not run\n" +
			"is listed at severity \"off\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := opts.prepare(cmd)
			if err != nil {
				return err
			}

			return opts.run(cmd)
		},
	}

	opts.declareFlags(cmd)

	return cmd
}

// declareFlags declares the flags the listing takes.
func (o *rulesOptions) declareFlags(cmd *cobra.Command) {
	o.RegisterFlags(cmd)
	o.ruleFlags.Register(cmd)
}

// prepare folds the rules block of the settings file into the flags.
func (o *rulesOptions) prepare(cmd *cobra.Command) error {
	err := o.Prepare(cmd)
	if err != nil {
		return err
	}

	o.policy = o.ruleFlags.Policy(o.File())

	return nil
}

// run prints one row per rule: its name, the severity it will run at, and what
// it reports.
func (o *rulesOptions) run(cmd *cobra.Command) error {
	overrides, err := o.policy.Severities()
	if err != nil {
		return err
	}

	rules, err := o.policy.Rules()
	if err != nil {
		return err
	}

	w := newColumns(cmd.OutOrStdout())

	for _, r := range rules {
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

// versionsOptions is what "list versions" was asked to print. It takes the
// schema flags, which are what decide the schemas there are to list.
type versionsOptions struct {
	*settings.Options

	schemaFlags schemaflags.Flags

	// internal state
	// store is where the schemas are read from.
	store schema.Store
}

// newVersionsCommand builds "list versions". --collector-version is
// deliberately absent: this is the command that says which ones exist.
func newVersionsCommand(global *settings.Options) *cobra.Command {
	//nolint:exhaustruct // the flags fill themselves in, and the store is built by prepare
	opts := &versionsOptions{Options: global}

	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Print the available schema versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := opts.prepare(cmd)
			if err != nil {
				return err
			}

			return opts.run(cmd)
		},
	}

	opts.declareFlags(cmd)

	return cmd
}

// declareFlags declares the flags the listing takes.
func (o *versionsOptions) declareFlags(cmd *cobra.Command) {
	o.RegisterFlags(cmd)
	o.schemaFlags.Register(cmd)
}

// prepare folds the schema keys of the settings file into the flags and builds
// the store the listing reads.
func (o *versionsOptions) prepare(cmd *cobra.Command) error {
	err := o.Prepare(cmd)
	if err != nil {
		return err
	}

	o.schemaFlags.ApplySettings(o.File(), o.Fold(cmd))
	o.store = o.schemaFlags.Store(o.FS())

	return nil
}

// run prints one row per schema version, newest first.
func (o *versionsOptions) run(cmd *cobra.Command) error {
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

		w.row(v, fmt.Sprintf("%d components", cat.Count()), cat.Distribution+" "+note)
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
