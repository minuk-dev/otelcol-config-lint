package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestScalarDecoding(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, version, distribution string
		decoder                     *schema.Decoder
		weak, zero, known           bool
	}{
		{"old release", "v0.104.0", "core", nil, true, false, true},
		{"strict release", "v0.105.0", "contrib", nil, false, false, true},
		{"null boundary", "v0.127.0", "core", nil, false, true, true},
		{"build metadata", "v0.127.0+custom.1", "core", nil, false, true, true},
		{"custom suffix", "v0.127.0-custom", "core", nil, false, false, false},
		{"development", "dev", "core", nil, false, false, false},
		{"custom distribution without provenance", "v1.0.0", "acme", nil, false, false, false},
		{"resolved old decoder wins", "v1.0.0", "acme",
			&schema.Decoder{ConfmapVersion: "v0.104.0", Modified: false}, true, false, true},
		{"resolved new decoder wins", "v0.101.0-custom", "acme",
			&schema.Decoder{ConfmapVersion: "v1.33.0", Modified: false}, false, true, true},
		{"before null boundary", "v0.127.0", "acme",
			&schema.Decoder{ConfmapVersion: "v1.32.0", Modified: false}, false, false, true},
		{"local replacement", "v0.101.0", "core",
			&schema.Decoder{ConfmapVersion: "v0.101.0", Modified: true}, false, false, false},
		{"unresolved graph", "v0.101.0", "core",
			&schema.Decoder{ConfmapVersion: "", Modified: false}, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &schema.Schema{CollectorVersion: tt.version, Distribution: tt.distribution, Decoder: tt.decoder}
			weak, zero, known := s.ScalarDecoding()
			assert.Equal(t, tt.weak, weak)
			assert.Equal(t, tt.zero, zero)
			assert.Equal(t, tt.known, known)
		})
	}
}
