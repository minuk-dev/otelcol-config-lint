package rule_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

func TestExpansions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, value, masked string
		dynamic             bool
	}{
		{name: "literal", value: "5s", masked: "5s", dynamic: false},
		{name: "provider", value: "${env:TIMEOUT}", masked: "?", dynamic: true},
		{name: "short", value: "$TIMEOUT", masked: "?", dynamic: true},
		{name: "escaped provider", value: "$${env:TIMEOUT}", masked: "$${env:TIMEOUT}", dynamic: false},
		{name: "escaped short", value: "$$TIMEOUT", masked: "$$TIMEOUT", dynamic: false},
		{name: "odd dollars", value: "$$${env:TIMEOUT}", masked: "$$?", dynamic: true},
		{name: "even dollars", value: "$$$${env:TIMEOUT}", masked: "$$$${env:TIMEOUT}", dynamic: false},
		{name: "odd short", value: "$$$TIMEOUT", masked: "$$?", dynamic: true},
		{name: "even short", value: "$$$$TIMEOUT", masked: "$$$$TIMEOUT", dynamic: false},
		{name: "mixed", value: "$${env:HOST}:${env:PORT}", masked: "$${env:HOST}:?", dynamic: true},
		{name: "multiple", value: "${env:HOST}:$PORT", masked: "?:?", dynamic: true},
		{name: "bare dollars", value: "$$$", masked: "$$$", dynamic: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.dynamic, rule.HasExpansion(tt.value))
			assert.Equal(t, tt.masked, rule.MaskExpansions(tt.value, "?"))
		})
	}
}

func TestScalarChild(t *testing.T) {
	t.Parallel()

	var doc yaml.Node

	err := yaml.Unmarshal([]byte(`name: first
name: second
empty: ''
expanded: ${env:NAME}
list: [item]
`), &doc)
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name, key, want string
		found           bool
	}{
		{name: "first duplicate wins", key: "name", want: "first", found: true},
		{name: "missing", key: "missing", want: "", found: false},
		{name: "empty", key: "empty", want: "", found: false},
		{name: "expansion", key: "expanded", want: "", found: false},
		{name: "non scalar", key: "list", want: "", found: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			node, found := rule.ScalarChild(doc.Content[0], testCase.key)
			if found != testCase.found || (found && node.Value != testCase.want) {
				t.Errorf("ScalarChild(%q) = (%v, %v), want (%q, %v)",
					testCase.key, node, found, testCase.want, testCase.found)
			}
		})
	}
}
