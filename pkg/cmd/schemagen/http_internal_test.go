package schemagen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPDecoderBookkeeping(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pkg, extra string
		open       bool
	}{
		"migration bookkeeping": {
			coreModuleRoot + "/config/confighttp", "deprecationWarnings []string; _ struct{}", false,
		},
		"older tagged config": {coreModuleRoot + "/config/confighttp", "", false},
		"new custom field": {
			coreModuleRoot + "/config/confighttp", "deprecationWarnings []string; hidden map[string]string", true,
		},
		"changed bookkeeping type": {coreModuleRoot + "/config/confighttp", "deprecationWarnings map[string]string", true},
		"unrelated decoder":        {"example.com/confighttp", "deprecationWarnings []string", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, typ := range []string{"ClientConfig", "ServerConfig"} {
				index := newGoIndex()
				index.add(tt.pkg, []byte(`package confighttp
import "go.opentelemetry.io/collector/confmap"
type `+typ+` struct {
 Endpoint string `+"`mapstructure:\"endpoint\"`"+`
 Keepalive KeepaliveConfig `+"`mapstructure:\"keepalive\"`"+`
 IdleTimeout string `+"`mapstructure:\"idle_timeout\"`"+`
 `+tt.extra+`
}
type KeepaliveConfig struct { Enabled bool `+"`mapstructure:\"enabled\"`"+` }
func (cfg *`+typ+`) Unmarshal(conf *confmap.Conf) error { return nil }
`))
				index.add("example.com/otlphttp", []byte(`package otlphttp
import "`+tt.pkg+`"
type Config struct { confighttp.`+typ+` `+"`mapstructure:\",squash\"`"+` }
`))
				got := index.fields("example.com/otlphttp.Config", nil, 0)
				require.NotNil(t, got)
				assert.Equal(t, tt.open, got.Open)
				assert.ElementsMatch(t, []string{"endpoint", "keepalive", "idle_timeout"}, keysOf(got.Children))
				assert.Contains(t, got.Children["keepalive"].Children, "enabled")
			}
		})
	}
}
