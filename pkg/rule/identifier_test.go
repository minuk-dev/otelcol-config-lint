package rule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// Contract: component.ID.UnmarshalText and pipeline.ID.UnmarshalText at v0.157.0:
// https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/component/identifiable.go
// https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/pipeline/pipeline.go
// Both trim each part, split only once and limit names by bytes, not runes.
func TestIdentifierError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		invalid bool
	}{
		{name: "unnamed", id: "otlp", invalid: false},
		{name: "named", id: "otlp/internal-1", invalid: false},
		{name: "uppercase and underscore type", id: "Otlp_1/2", invalid: false},
		{name: "unicode name", id: "otlp/내부", invalid: false},
		{name: "punctuation and slash in name", id: "otlp/a/b:._-", invalid: false},
		{name: "trim each part", id: " otlp / internal ", invalid: false},
		{name: "maximum type", id: strings.Repeat("a", 63), invalid: false},
		{name: "maximum name", id: "otlp/" + strings.Repeat("a", 1024), invalid: false},
		{name: "maximum unicode name bytes", id: "otlp/" + strings.Repeat("é", 512), invalid: false},
		{name: "empty", id: "", invalid: true},
		{name: "blank", id: " ", invalid: true},
		{name: "empty type", id: "/internal", invalid: true},
		{name: "empty name", id: "otlp/", invalid: true},
		{name: "blank name", id: "otlp/ \t", invalid: true},
		{name: "digit first", id: "1otlp", invalid: true},
		{name: "underscore first", id: "_otlp", invalid: true},
		{name: "hyphen in type", id: "otlp-http", invalid: true},
		{name: "unicode type", id: "수신기", invalid: true},
		{name: "type too long", id: strings.Repeat("a", 64), invalid: true},
		{name: "name too long", id: "otlp/" + strings.Repeat("a", 1025), invalid: true},
		{name: "unicode name too many bytes", id: "otlp/" + strings.Repeat("é", 513), invalid: true},
		{name: "space", id: "otlp/a b", invalid: true},
		{name: "unicode separator", id: "otlp/a\u00a0b", invalid: true},
		{name: "control", id: "otlp/a\tb", invalid: true},
		{name: "unicode format", id: "otlp/a\u200bb", invalid: true},
		{name: "symbol", id: "otlp/a+b", invalid: true},
		{name: "emoji", id: "otlp/😀", invalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.invalid, rule.IdentifierError(tt.id) != "", "%q", tt.id)
		})
	}
}
