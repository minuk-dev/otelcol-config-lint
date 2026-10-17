package rule_test

import (
	"math"
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

func TestReadNumbers(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, value string
		known       bool
		num         int64
	}{
		{name: "nanoseconds", value: "5000000000", known: true, num: 5_000_000_000},
		{name: "hexadecimal", value: "0x100", known: true, num: 256},
		{name: "octal", value: "010", known: true, num: 8},
		{name: "underscores", value: "1_000_000", known: true, num: 1_000_000},
		{name: "positive sign", value: "+5", known: true, num: 5},
		{name: "negative", value: "-5", known: true, num: -5},
		{name: "maximum", value: "9223372036854775807", known: true, num: math.MaxInt64},
		{name: "minimum", value: "-9223372036854775808", known: true, num: math.MinInt64},
		{name: "fractional", value: "5.9", known: true, num: 5},
		{name: "negative fractional", value: "-5.9", known: true, num: -5},
		{name: "exponent", value: "1e3", known: true, num: 1000},
		{name: "non-octal leading zero", value: "08", known: true, num: 8},
		{name: "positive overflow", value: "9223372036854775808", known: false, num: 0},
		{name: "negative overflow", value: "-1e20", known: false, num: 0},
		{name: "float upper boundary", value: "9223372036854775808.0", known: false, num: 0},
		{name: "infinity", value: ".inf", known: false, num: 0},
		{name: "nan", value: ".nan", known: false, num: 0},
		{name: "quoted number", value: "'5'", known: false, num: 0},
		{name: "boolean", value: "true", known: false, num: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var doc yaml.Node

			require.NoError(t, yaml.Unmarshal([]byte("value: "+tt.value), &doc))
			node := doc.Content[0].Content[1]

			for name, read := range map[string]func(*yaml.Node) rule.Setting{
				"integer": rule.ReadInt, "duration": rule.ReadDuration,
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					got := read(node)
					assert.Same(t, node, got.Node)
					assert.True(t, got.Present)
					assert.Equal(t, tt.known, got.Known)
					assert.Equal(t, tt.num, got.Num)
				})
			}
		})
	}
}
