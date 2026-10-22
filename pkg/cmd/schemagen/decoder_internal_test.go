package schemagen

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

			set := &moduleSet{byPath: map[string]resolvedModule{}, incomplete: false}
			for _, mod := range modules {
				set.byPath[mod.Path] = mod
			}

			decoder := set.decoder()
			assert.Equal(t, tt.version, decoder.ConfmapVersion)
			assert.Equal(t, tt.modified, decoder.Modified)
		})
	}
}

func TestDecoderUsesGeneratedCollectorModule(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"generated", "missing", "invalid"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			replacement := filepath.Join(root, "confmap")
			require.NoError(t, os.Mkdir(replacement, dirPerm))
			require.NoError(t, os.WriteFile(filepath.Join(replacement, "go.mod"),
				[]byte("module go.opentelemetry.io/collector/confmap\n\ngo 1.25\n"), filePerm))
			generated := filepath.Join(root, "_build")
			require.NoError(t, os.Mkdir(generated, dirPerm))

			contents := "module example.com/collector\n\ngo 1.25\n\n" +
				"require go.opentelemetry.io/collector/confmap v1.33.0\n" +
				"replace go.opentelemetry.io/collector/confmap => ../confmap\n"
			if state == "invalid" {
				contents = "invalid go.mod"
			}

			if state != "missing" {
				require.NoError(t, os.WriteFile(filepath.Join(generated, "go.mod"), []byte(contents), filePerm))
			}

			var man manifest

			man.dir, man.Dist.OutputPath = root, "_build"
			//nolint:exhaustruct // the decoder reader only uses timeout and progress
			opts := &options{timeout: 20 * time.Second, progress: io.Discard}

			var resolved resolvedModule

			resolved.Path, resolved.Version, resolved.Dir = "go.opentelemetry.io/collector/confmap", "v0.104.0", "cache"
			components := &moduleSet{byPath: map[string]resolvedModule{resolved.Path: resolved}, incomplete: false}
			decoder := opts.decoder(&man, components)
			assert.Equal(t, state != "generated", decoder.Incomplete)
			assert.Equal(t, state == "generated", decoder.Modified)

			if state != "missing" {
				after, err := os.ReadFile(filepath.Join(generated, "go.mod"))
				require.NoError(t, err)
				assert.Equal(t, contents, string(after), "module inspection must be read-only")
			}
		})
	}
}
