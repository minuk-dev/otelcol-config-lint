package rule_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

func TestSettingReaders(t *testing.T) {
	t.Parallel()

	for name, read := range map[string]func(*yaml.Node) rule.Setting{
		"integer": rule.ReadInt, "duration": rule.ReadDuration,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range []struct {
				name    string
				value   string
				present bool
				known   bool
			}{
				{name: "null", value: "null", present: true, known: true},
				{name: "null shorthand", value: "~", present: true, known: true},
				{name: "empty YAML value", value: "", present: true, known: true},
				{name: "quoted null is not null", value: "'null'", present: true, known: false},
				{name: "quoted empty string is not null", value: "''", present: true, known: false},
				{name: "runtime expansion", value: "${env:VALUE}", present: true, known: false},
				{name: "invalid value", value: "invalid", present: true, known: false},
				{name: "explicit zero", value: "0", present: true, known: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					var doc yaml.Node

					require.NoError(t, yaml.Unmarshal([]byte("value: "+tt.value+"\n"), &doc))
					node := doc.Content[0].Content[1]
					got := read(node)
					assert.Same(t, node, got.Node, "retain the diagnostic location")
					assert.Equal(t, tt.present, got.Present)
					assert.Equal(t, tt.known, got.Known)
					assert.Zero(t, got.Num)
					assert.Equal(t, tt.present && !tt.known, got.Unknown())
				})
			}

			assert.Equal(t, rule.Absent(), read(nil), "a missing node also uses the default")
		})
	}
}
