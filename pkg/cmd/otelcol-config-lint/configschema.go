package otelcolconfiglint

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ConfigSchemaCmdOptions is what "config-schema" was asked to do. It has no
// flags of its own and reads no settings file: it describes the file rather
// than reading one.
type ConfigSchemaCmdOptions struct {
	*GlobalCmdOptions
}

// newConfigSchemaCmdOptions builds the options "config-schema" starts from.
func newConfigSchemaCmdOptions(global *GlobalCmdOptions) *ConfigSchemaCmdOptions {
	return &ConfigSchemaCmdOptions{GlobalCmdOptions: global}
}

// newConfigSchemaCommand builds "config-schema", which prints the JSON Schema
// for the settings file. It is a command rather than only a committed file so
// the copy an editor is pointed at can be regenerated from the binary in hand,
// and so the rule names it lists are the ones that binary actually runs.
//
// It is spelled "config-schema" and not "schema" because a schema in this tool
// is otherwise a collector's, which --schema-location and "list versions" are
// about; this one describes the linter's own settings.
func newConfigSchemaCommand(_ *ConfigSchemaCmdOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "config-schema",
		Short: "Print the JSON Schema for the settings file",
		Long: "Print the JSON Schema for the settings file, for an editor to check one against\n" +
			"as it is written.\n\n" +
			"The same document is committed as " + SettingsSchemaFile + " and served from\n" +
			SettingsSchemaID + "\n\n" +
			"A settings file can name it directly, which every YAML language server reads:\n\n" +
			"  # yaml-language-server: $schema=" + SettingsSchemaID,
		Example: "  otelcol-config-lint config-schema > " + SettingsSchemaFile,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc, err := SettingsSchema()
			if err != nil {
				return err
			}

			_, err = cmd.OutOrStdout().Write(doc)
			if err != nil {
				return fmt.Errorf("write schema: %w", err)
			}

			return nil
		},
	}
}
