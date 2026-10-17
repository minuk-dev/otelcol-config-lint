package componentstability_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/componentstability"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestAlphaReportingPolicy(t *testing.T) {
	t.Parallel()

	base := componentstability.New()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("report_alpha: false"), &node))

	configurable, ok := base.(rule.Configurable)
	require.True(t, ok)

	configured, err := configurable.WithSettings(node.Content[0])
	require.NoError(t, err)

	fixture := `extensions:
  alpha:
  dev:
service:
  extensions: [alpha, dev]
`
	opts := ruletest.Options{Schema: &schema.Schema{Components: map[config.Kind]map[string]*schema.Component{
		config.KindExtension: {
			"alpha": {Type: "alpha", Stability: map[string]schema.Stability{"extension": schema.Alpha}},
			"dev":   {Type: "dev", Stability: map[string]schema.Stability{"extension": schema.Development}},
		},
	}}}
	found, err := ruletest.RunWith(configured, fixture, opts)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Contains(t, found[0].Message, "development")

	original, err := ruletest.RunWith(base, fixture, opts)
	require.NoError(t, err)
	assert.Len(t, original, 2, "configuring a rule does not mutate its default instance")
}

func TestAlphaSettingsRejectInvalidValues(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"report_alpha: 'false'", "report_alpha: null", "report_alhpa: false", "[]"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()

			var node yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(source), &node))

			configurable, ok := componentstability.New().(rule.Configurable)
			require.True(t, ok)

			_, err := configurable.WithSettings(node.Content[0])
			require.Error(t, err)
		})
	}
}
