package wrongnodetype_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/wrongnodetype"
)

func TestReferenceListItems(t *testing.T) {
	t.Parallel()

	for _, slot := range []string{"receivers", "processors", "exporters", "extensions"} {
		for name, tt := range map[string]struct {
			items string
			kind  string
			count int
		}{
			"mapping":        {"{bogus: true}", "a mapping", 1},
			"sequence":       {"[otlp]", "a list", 1},
			"mapping alias":  {"&bad {bogus: true}, *bad", "a mapping", 2},
			"sequence alias": {"&bad [otlp], *bad", "a list", 2},
		} {
			t.Run(slot+"/"+name, func(t *testing.T) {
				t.Parallel()

				path := "service.pipelines.traces." + slot
				prefix := "service:\n  pipelines:\n    traces:\n      " + slot + ": [otlp, "

				if slot == "extensions" {
					path = "service.extensions"
					prefix = "service:\n  extensions: [otlp, "
				}

				found, err := ruletest.Run(wrongnodetype.New(), prefix+tt.items+"]\n")
				require.NoError(t, err)
				require.Len(t, found, tt.count)

				lines := strings.Split(prefix, "\n")

				for i, d := range found {
					assert.Equal(t, "wrong-node-type", d.Rule)
					assert.Equal(t, diag.Error, d.Severity)
					assert.Equal(t, fmt.Sprintf("%s[%d]", path, i+1), d.Path)
					assert.Equal(t, "component reference must be a scalar, got "+tt.kind, d.Message)
					assert.Equal(t, diag.Position{
						File: "test.yaml", Line: len(lines), Column: len(lines[len(lines)-1]) + 1,
					}, d.Position, "aliases retain the anchor's position")
				}
			})
		}
	}
}

func TestValidReferenceListItems(t *testing.T) {
	t.Parallel()

	src := `service:
  extensions: [&extension zpages, *extension, zpages/second]
  pipelines:
    traces:
      receivers: [&receiver otlp, otlp/second]
      processors: [&processor batch, batch/second]
      exporters: [&exporter debug, debug/second]
    metrics:
      receivers: [*receiver]
      processors: [*processor]
      exporters: [*exporter]
`
	found, err := ruletest.Run(wrongnodetype.New(), src)
	require.NoError(t, err)
	assert.Empty(t, found)
}
