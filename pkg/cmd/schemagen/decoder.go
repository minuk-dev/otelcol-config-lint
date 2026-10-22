package schemagen

import (
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func (s *moduleSet) decoder() *schema.Decoder {
	decoder := &schema.Decoder{ConfmapVersion: "", Modified: false, Incomplete: s.incomplete}

	for _, path := range []string{
		"go.opentelemetry.io/collector/confmap",
		"github.com/mitchellh/mapstructure", "github.com/go-viper/mapstructure/v2",
	} {
		mod, ok := s.lookup(path)
		if !ok {
			continue
		}

		if mod.Replace != nil {
			decoder.Modified = decoder.Modified || mod.Replace.Path != path || mod.Replace.Version == ""
			mod = *mod.Replace
		}

		if path == "go.opentelemetry.io/collector/confmap" {
			decoder.ConfmapVersion = mod.Version
		}
	}

	return decoder
}

// decoder prefers the generated Collector module: components alone omit runtime/provider dependencies.
func (o *options) decoder(man *manifest, components *moduleSet) *schema.Decoder {
	partial := components.decoder()

	partial.Incomplete = true
	if man.Dist.OutputPath == "" {
		return partial
	}

	dir := man.replaceBase()
	exists, err := afero.Exists(o.fs(), filepath.Join(dir, "go.mod"))
	if err != nil || !exists {
		return partial
	}

	out, err := o.goCommand(dir, "list", "-mod=readonly", "-m", "-e", "-json", "all")
	if err != nil {
		o.logf("  collector decoder graph: %v\n", err)

		return partial
	}

	modules, err := o.readModuleSet(out)
	if err != nil {
		o.logf("  collector decoder graph: %v\n", err)

		return partial
	}

	return modules.decoder()
}
