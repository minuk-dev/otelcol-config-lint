package rule_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

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
