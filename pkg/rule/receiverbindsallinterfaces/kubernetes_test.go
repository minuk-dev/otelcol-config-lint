package receiverbindsallinterfaces_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/receiverbindsallinterfaces"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestKubernetesWildcardListeners(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]string{
		"receiver":  receiving("      grpc: {endpoint: '0.0.0.0:4317'}"),
		"extension": extending("custom", "    server: {endpoint: '[::]:8000'}"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found, err := ruletest.RunWith(receiverbindsallinterfaces.New(), source,
				ruletest.Options{Env: rule.Environment{Kubernetes: true}})
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, diag.Info, found[0].Severity)
			assert.Contains(t, found[0].Message, "pod network namespace")
			assert.Contains(t, found[0].Hint, "hostNetwork")
			assert.NotContains(t, found[0].Hint, "MY_POD_IP")
			unknown := check(t, source)
			require.Len(t, unknown, 1)
			assert.Equal(t, diag.Warning, unknown[0].Severity)
		})
	}
}

func TestTelemetryReaderDoesNotBecomeReceiverListener(t *testing.T) {
	t.Parallel()

	source := `service:
  telemetry:
    metrics:
      readers:
        - pull:
            exporter:
              prometheus:
                host: 0.0.0.0
                port: 8888
`
	found, err := ruletest.RunWith(receiverbindsallinterfaces.New(), source,
		ruletest.Options{Env: rule.Environment{Kubernetes: true}})
	require.NoError(t, err)
	assert.Empty(t, found)
}
