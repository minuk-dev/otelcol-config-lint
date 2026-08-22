package otelcolconfiglint

import (
	"github.com/spf13/afero"
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// schemaFlags say which schemas to read and which collector binary they
// describe. "run" and "list versions" both take them: both answer differently
// depending on the binary meant, and neither can find a schema without being
// told where to look.
//
// They are a group of their own rather than fields of each command so the two
// cannot drift into spelling the same flag differently.
type schemaFlags struct {
	distribution    string
	schemaLocations []string
}

// registerSchemaFlags declares --distribution and --schema-location on cmd.
func (f *schemaFlags) registerSchemaFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&f.distribution, "distribution", schema.DefaultDistribution,
		"collector distribution to validate against: core, contrib, k8s or otlp")
	flags.StringSliceVar(&f.schemaLocations, "schema-location", nil,
		"where to find schemas: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the published registry)")
}

// applySchemaSettings folds the keys of the run block these flags mirror.
func (f *schemaFlags) applySchemaSettings(s *settings, fold settingsFold) {
	fold.str("distribution", &f.distribution, s.Run.Distribution)
	fold.list("schema-location", &f.schemaLocations, s.Run.SchemaLocations)
}

// schemaStore builds the store the flags describe. The locations are searched
// in the order given, before the published registry.
func (f *schemaFlags) schemaStore(fsys afero.Fs) schema.Store {
	return schema.Store{Locations: f.schemaLocations, Distribution: f.distribution, Fs: fsys}
}
