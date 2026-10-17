package missingbatch_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/missingbatch"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestAdditionalBatchProcessors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, settings string
		valid          bool
	}{
		{name: "custom type", settings: "additional_processors: [resourcebatch]", valid: true},
		{name: "empty", settings: "additional_processors: []", valid: true},
		{name: "unknown key", settings: "typo: []", valid: false},
		{name: "wrong block", settings: "[]", valid: false},
		{name: "wrong list", settings: "additional_processors: resourcebatch", valid: false},
		{name: "wrong item", settings: "additional_processors: [true]", valid: false},
		{name: "instance name", settings: "additional_processors: [resourcebatch/custom]", valid: false},
		{name: "expansion", settings: "additional_processors: ['${env:BATCH}']", valid: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var document yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tt.settings), &document))

			_, err := rule.Configure([]rule.Rule{missingbatch.New()},
				map[string]yaml.Node{"missing-batch": *document.Content[0]})
			if tt.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}

	const source = `receivers:
  otlp: {}
processors:
  resourcebatch: {}
exporters:
  debug: {}
service:
  pipelines:
    logs:
      receivers: [otlp]
      processors: [resourcebatch]
      exporters: [debug]
`

	var document yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("additional_processors: [resourcebatch]"), &document))

	original := missingbatch.New()
	configured, err := rule.Configure([]rule.Rule{original}, map[string]yaml.Node{"missing-batch": *document.Content[0]})
	require.NoError(t, err)
	found, err := ruletest.Run(configured[0], source)
	require.NoError(t, err)
	require.Empty(t, found)
	found, err = ruletest.Run(original, source)
	require.NoError(t, err)
	require.Len(t, found, 1, "configuration must not mutate the original rule")

	upstream := strings.Replace(source, "exporters: [debug]", "exporters: [forward]", 1) +
		"    logs/out:\n      receivers: [forward]\n      exporters: [debug]\nconnectors:\n  forward: {}\n"
	found, err = ruletest.Run(configured[0], upstream)
	require.NoError(t, err)
	require.Empty(t, found)
	found, err = ruletest.Run(original, upstream)
	require.NoError(t, err)
	require.Len(t, found, 1)
}
