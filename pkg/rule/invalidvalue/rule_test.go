package invalidvalue_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidvalue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestDurationValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string // the timeout as written in YAML
		invalid bool
	}{
		{name: "leading decimal point", value: "'.5s'", invalid: false},
		{name: "trailing decimal point", value: "'1.s'", invalid: false},
		{name: "positive sign", value: "'+1s'", invalid: false},
		{name: "negative duration", value: "'-1s'", invalid: false},
		{name: "unitless zero", value: "'0'", invalid: false},
		{name: "Greek mu", value: "'1μs'", invalid: false},
		{name: "micro sign", value: "'1µs'", invalid: false},
		{name: "ASCII microseconds", value: "'1us'", invalid: false},
		{name: "compound duration", value: "'1m30.5s'", invalid: false},
		{name: "maximum duration", value: "'9223372036854775807ns'", invalid: false},
		{name: "minimum duration", value: "'-9223372036854775808ns'", invalid: false},
		{name: "positive overflow", value: "'9223372036854775808ns'", invalid: true},
		{name: "negative overflow", value: "'-9223372036854775809ns'", invalid: true},
		{name: "hours overflow", value: "'9223372036854775808h'", invalid: true},
		{name: "compound overflow", value: "'9223372036854775807ns1ns'", invalid: true},
		{name: "missing unit", value: "'5'", invalid: true},
		{name: "bare number", value: "5", invalid: true},
		{name: "empty string", value: "''", invalid: true},
		{name: "unsupported unit", value: "'1d'", invalid: true},
		{name: "whitespace", value: "' 1s'", invalid: true},
		{name: "list", value: "[1s]", invalid: true},
		{name: "mapping", value: "{value: 1s}", invalid: true},
		{name: "environment expansion", value: "'${env:TIMEOUT}'", invalid: false},
		{name: "short environment expansion", value: "'$TIMEOUT'", invalid: false},
		{name: "embedded expansion", value: "'${env:SECONDS}s'", invalid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "exporters:\n  otlp:\n    endpoint: localhost:4317\n    timeout: " + tt.value + "\n"
			found, err := ruletest.Run(invalidvalue.New(), src)
			require.NoError(t, err)

			if !tt.invalid {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, "invalid-value", found[0].Rule)
			assert.Equal(t, diag.Error, found[0].Severity)
			assert.Equal(t, "exporters.otlp.timeout", found[0].Path)
			assert.Equal(t, 4, found[0].Position.Line)
			assert.Equal(t, `"timeout" must be a duration such as 5s, 200ms or 1m30s`, found[0].Message)
		})
	}
}
