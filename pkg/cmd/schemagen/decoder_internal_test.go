package schemagen

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecoderProvenance(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, modules, version string
		modified               bool
	}{
		{"resolved module", `[
  {
    "Path": "go.opentelemetry.io/collector/confmap",
    "Version": "v1.33.0",
    "Dir": "cache"
  }
]`, "v1.33.0", false},
		{"official version replacement", `[
  {
    "Path": "go.opentelemetry.io/collector/confmap",
    "Version": "v0.101.0",
    "Dir": "cache",
    "Replace": {
      "Path": "go.opentelemetry.io/collector/confmap",
      "Version": "v1.33.0",
      "Dir": "cache"
    }
  }
]`, "v1.33.0", false},
		{"local replacement", `[
  {
    "Path": "go.opentelemetry.io/collector/confmap",
    "Version": "v0.101.0",
    "Dir": "local",
    "Replace": {
      "Path": "../confmap",
      "Dir": "local"
    }
  }
]`, "", true},
		{"fork replacement", `[
  {
    "Path": "go.opentelemetry.io/collector/confmap",
    "Version": "v0.101.0",
    "Dir": "cache",
    "Replace": {
      "Path": "example.com/confmap",
      "Version": "v1.33.0",
      "Dir": "cache"
    }
  }
]`, "v1.33.0", true},
		{"mapstructure replacement", `[
  {
    "Path": "go.opentelemetry.io/collector/confmap",
    "Version": "v1.33.0",
    "Dir": "cache"
  },
  {
    "Path": "github.com/go-viper/mapstructure/v2",
    "Dir": "local",
    "Replace": {
      "Path": "../mapstructure"
    }
  }
]`, "v1.33.0", true},
		{"missing decoder", `[]`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var modules []resolvedModule
			require.NoError(t, json.Unmarshal([]byte(tt.modules), &modules))

			set := &moduleSet{byPath: map[string]resolvedModule{}}
			for _, mod := range modules {
				set.byPath[mod.Path] = mod
			}

			decoder := set.decoder()
			assert.Equal(t, tt.version, decoder.ConfmapVersion)
			assert.Equal(t, tt.modified, decoder.Modified)
		})
	}
}
