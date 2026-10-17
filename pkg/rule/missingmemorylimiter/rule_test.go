package missingmemorylimiter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/missingmemorylimiter"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestConnectorIngressProtection(t *testing.T) {
	t.Parallel()

	const source = `receivers:
  otlp: {}
processors:
  memory_limiter: {check_interval: 1s, limit_mib: 128}
connectors:
  forward/a: {}
  forward/b: {}
exporters:
  debug: {}
service:
  pipelines:
    logs/in:
      receivers: [otlp]
      processors: [memory_limiter]
      exporters: [forward/a]
    logs/middle:
      receivers: [forward/a]
      exporters: [forward/b]
    logs/out:
      receivers: [forward/b]
      exporters: [debug]
`

	for _, tt := range []struct {
		name, from, to string
		want           int
	}{
		{name: "protected multi-hop", from: "not-present", to: "", want: 0},
		{name: "no limiter", from: "processors: [memory_limiter]", to: "processors: []", want: 3},
		{name: "undeclared limiter", from: "  memory_limiter: {check_interval: 1s, limit_mib: 128}", to: "", want: 2},
		{name: "routing", from: "forward", to: "routing", want: 0},
		{name: "unknown connector", from: "forward", to: "custom", want: 2},
		{name: "mixed external receiver", from: "receivers: [forward/b]", to: "receivers: [forward/b, otlp]", want: 1},
		{name: "missing producer", from: "exporters: [forward/a]", to: "exporters: [debug]", want: 2},
		{name: "dynamic producer", from: "exporters: [forward/a]", to: "exporters: ['${env:DEST}']", want: 2},
		{name: "dynamic receiver", from: "receivers: [forward/b]", to: "receivers: ['${env:INPUT}']", want: 1},
		{name: "cycle containing limiter", from: "receivers: [otlp]", to: "receivers: [forward/b]", want: 2},
		{name: "signal conversion", from: "logs/out:", to: "metrics/out:", want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := ruletest.Run(missingmemorylimiter.New(), strings.ReplaceAll(source, tt.from, tt.to))
			require.NoError(t, err)
			require.Len(t, found, tt.want)
		})
	}

	t.Run("unprotected fan-in", func(t *testing.T) {
		t.Parallel()

		input := source + "    logs/extra:\n      receivers: [otlp]\n      exporters: [forward/a]\n"
		found, err := ruletest.Run(missingmemorylimiter.New(), input)
		require.NoError(t, err)
		require.Len(t, found, 3)
	})
}
