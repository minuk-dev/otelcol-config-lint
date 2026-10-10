package invalidpipelinekey_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidpipelinekey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// pipeline.ID.UnmarshalText uses the same name checks as component.ID, then
// delegates to Signal.UnmarshalText, which accepts profiles as well at v0.157.0:
// https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/pipeline/pipeline.go
// https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/pipeline/internal/globalsignal/signal.go
func TestPipelineIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		invalid bool
	}{
		{name: "unnamed", key: "traces", invalid: false},
		{name: "named", key: "metrics/internal-1", invalid: false},
		{name: "numeric name", key: "logs/2", invalid: false},
		{name: "profiles", key: "profiles", invalid: false},
		{name: "unicode and punctuation", key: "traces/내부/a.b:_-", invalid: false},
		{name: "trim each part", key: " traces / internal ", invalid: false},
		{name: "maximum name", key: "traces/" + strings.Repeat("a", 1024), invalid: false},
		{name: "maximum unicode bytes", key: "traces/" + strings.Repeat("é", 512), invalid: false},
		{name: "empty", key: "", invalid: true},
		{name: "empty signal", key: "/internal", invalid: true},
		{name: "empty name", key: "traces/", invalid: true},
		{name: "blank name", key: "traces/ \t", invalid: true},
		{name: "unknown signal", key: "tracez/internal", invalid: true},
		{name: "uppercase signal", key: "Traces", invalid: true},
		{name: "name too long", key: "traces/" + strings.Repeat("a", 1025), invalid: true},
		{name: "unicode name too many bytes", key: "traces/" + strings.Repeat("é", 513), invalid: true},
		{name: "whitespace", key: "traces/a b", invalid: true},
		{name: "control", key: "traces/a\tb", invalid: true},
		{name: "symbol", key: "traces/a+b", invalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "service:\n  pipelines:\n    ? " + strconv.Quote(tt.key) + "\n    : {}\n"
			found, err := ruletest.Run(invalidpipelinekey.New(), src)
			require.NoError(t, err)

			if !tt.invalid {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, "invalid-pipeline-key", found[0].Rule)
			assert.Equal(t, diag.Error, found[0].Severity)
			assert.Contains(t, found[0].Message, `"`+tt.key+`"`)
			assert.Equal(t, "service.pipelines."+tt.key, found[0].Path)
			assert.Equal(t, diag.Position{File: "test.yaml", Line: 3, Column: 7}, found[0].Position)
		})
	}
}
