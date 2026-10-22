package schemagen

import "github.com/minuk-dev/otelcol-config-lint/pkg/schema"

func (s *moduleSet) decoder() *schema.Decoder {
	decoder := &schema.Decoder{ConfmapVersion: "", Modified: false}

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
