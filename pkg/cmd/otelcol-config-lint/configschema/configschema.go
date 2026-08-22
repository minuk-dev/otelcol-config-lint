// Package configschema is the command that prints the JSON Schema for the
// settings file.
package configschema

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/settings"
)

// NewCommand builds "config-schema", which prints the JSON Schema for the
// settings file. It takes nothing: it describes the settings file rather than
// reading one. It is a command rather than only a committed file so
// the copy an editor is pointed at can be regenerated from the binary in hand,
// and so the rule names it lists are the ones that binary actually runs.
//
// It is spelled "config-schema" and not "schema" because a schema in this tool
// is otherwise a collector's, which --schema-location and "list versions" are
// about; this one describes the linter's own settings.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "config-schema",
		Short: "Print the JSON Schema for the settings file",
		Long: "Print the JSON Schema for the settings file, for an editor to check one against\n" +
			"as it is written.\n\n" +
			"The same document is committed as " + settings.SchemaFile + " and served from\n" +
			settings.SchemaID + "\n\n" +
			"A settings file can name it directly, which every YAML language server reads:\n\n" +
			"  # yaml-language-server: $schema=" + settings.SchemaID,
		Example: "  otelcol-config-lint config-schema > " + settings.SchemaFile,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc, err := settings.Schema()
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
