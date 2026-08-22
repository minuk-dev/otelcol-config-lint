package otelcolconfiglint

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// newListCommand builds "list" and its subcommands. Each one carries only the
// flags that change what it prints, so their help stays short.
func newListCommand(global *GlobalCmdOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print what the linter knows about",
		Example: `  otelcol-config-lint list rules
  otelcol-config-lint list versions`,
	}

	cmd.AddCommand(
		newListRulesCommand(newListRulesCmdOptions(global)),
		newListVersionsCommand(newListVersionsCmdOptions(global)),
	)

	return cmd
}

// ListRulesCmdOptions is what "list rules" was asked to print. It takes the
// rule flags and nothing else: the severities it lists are the ones the rules
// would run at, and no other flag changes those.
type ListRulesCmdOptions struct {
	*GlobalCmdOptions
	ruleFlags

	// internal state
	// policy is which rules run and at what level, once the flags and the
	// rules block have both had their say.
	policy rulePolicy
}

// newListRulesCmdOptions builds the options "list rules" starts from.
func newListRulesCmdOptions(global *GlobalCmdOptions) *ListRulesCmdOptions {
	//nolint:exhaustruct // the flags fill themselves in, and the policy is resolved by Prepare
	return &ListRulesCmdOptions{GlobalCmdOptions: global}
}

// newListRulesCommand builds "list rules". The rule flags apply because they
// decide the severity the rules will actually run at.
func newListRulesCommand(opts *ListRulesCmdOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Print the rules and their default severities",
		Long: "Print the rules and their default severities.\n\n" +
			"A severity changed by --default, --enable, --disable, --severity or\n" +
			"the settings file is marked as overridden; a rule that will not run\n" +
			"is listed at severity \"off\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := opts.Prepare(cmd)
			if err != nil {
				return err
			}

			return opts.Run(cmd)
		},
	}

	opts.RegisterFlags(cmd)

	return cmd
}

// RegisterFlags declares the flags the listing takes.
func (o *ListRulesCmdOptions) RegisterFlags(cmd *cobra.Command) {
	o.registerSettingsFlags(cmd)
	o.registerRuleFlags(cmd)
}

// Prepare folds the rules block of the settings file into the flags.
func (o *ListRulesCmdOptions) Prepare(cmd *cobra.Command) error {
	err := o.GlobalCmdOptions.Prepare(cmd)
	if err != nil {
		return err
	}

	o.policy = o.rulePolicy(o.settings)

	return nil
}

// Run prints one row per rule: its name, the severity it will run at, and what
// it reports.
func (o *ListRulesCmdOptions) Run(cmd *cobra.Command) error {
	overrides, err := o.policy.resolve()
	if err != nil {
		return err
	}

	rules, err := o.policy.rules()
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

// ListVersionsCmdOptions is what "list versions" was asked to print. It takes
// the schema flags, which are what decide the schemas there are to list.
type ListVersionsCmdOptions struct {
	*GlobalCmdOptions
	schemaFlags

	// internal state
	// store is where the schemas are read from.
	store schema.Store
}

// newListVersionsCmdOptions builds the options "list versions" starts from.
func newListVersionsCmdOptions(global *GlobalCmdOptions) *ListVersionsCmdOptions {
	//nolint:exhaustruct // the flags fill themselves in, and the store is built by Prepare
	return &ListVersionsCmdOptions{GlobalCmdOptions: global}
}

// newListVersionsCommand builds "list versions". --collector-version is
// deliberately absent: this is the command that says which ones exist.
func newListVersionsCommand(opts *ListVersionsCmdOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Print the available schema versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := opts.Prepare(cmd)
			if err != nil {
				return err
			}

			return opts.Run(cmd)
		},
	}

	opts.RegisterFlags(cmd)

	return cmd
}

// RegisterFlags declares the flags the listing takes.
func (o *ListVersionsCmdOptions) RegisterFlags(cmd *cobra.Command) {
	o.registerSettingsFlags(cmd)
	o.registerSchemaFlags(cmd)
}

// Prepare folds the schema keys of the settings file into the flags and builds
// the store the listing reads.
func (o *ListVersionsCmdOptions) Prepare(cmd *cobra.Command) error {
	err := o.GlobalCmdOptions.Prepare(cmd)
	if err != nil {
		return err
	}

	o.applySchemaSettings(o.settings, o.fold(cmd))
	o.store = o.schemaStore(o.fs())

	return nil
}

// Run prints one row per schema version, newest first.
func (o *ListVersionsCmdOptions) Run(cmd *cobra.Command) error {
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
