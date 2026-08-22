// Package schemaflags says which schemas to read and which collector binary
// they describe. "run" and "list versions" both take these flags: both answer
// differently depending on the binary meant, and neither can find a schema
// without being told where to look.
package schemaflags

import (
	"github.com/spf13/afero"
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Flags are the two flags themselves. They are a group of their own rather
// than fields of each command so the two cannot drift into spelling the same
// flag differently.
type Flags struct {
	distribution    string
	schemaLocations []string
}

// Register declares --distribution and --schema-location on cmd.
func (f *Flags) Register(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&f.distribution, "distribution", schema.DefaultDistribution,
		"collector distribution to validate against: core, contrib, k8s or otlp")
	flags.StringSliceVar(&f.schemaLocations, "schema-location", nil,
		"where to find schemas: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the published registry)")
}

// ApplySettings folds the keys of the run block these flags mirror.
func (f *Flags) ApplySettings(s *settings.File, fold settings.Fold) {
	fold.Str("distribution", &f.distribution, s.Run.Distribution)
	fold.List("schema-location", &f.schemaLocations, s.Run.SchemaLocations)
}

// Store builds the store the flags describe. The locations are searched
// in the order given, before the published registry.
func (f *Flags) Store(fsys afero.Fs) schema.Store {
	return schema.Store{Locations: f.schemaLocations, Distribution: f.distribution, Fs: fsys}
}
