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

	podNetwork, hostNetwork := false, true

	tests := []struct {
		name     string
		env      rule.Environment
		severity diag.Severity
		message  string
	}{
		{"ordinary pod", rule.Environment{Kubernetes: true, HostNetwork: &podNetwork}, diag.Info, "pod network namespace"},
		{
			"host network", rule.Environment{Kubernetes: true, HostNetwork: &hostNetwork},
			diag.Warning, "node network namespace",
		},
		{"unknown host network", rule.Environment{Kubernetes: true}, diag.Warning, "hostNetwork is unknown"},
		{"outside Kubernetes", rule.Environment{}, diag.Warning, "host has"},
	}
	for name, source := range map[string]string{
		"receiver":  receiving("      grpc: {endpoint: '0.0.0.0:4317'}"),
		"extension": extending("custom", "    server: {endpoint: '[::]:8000'}"),
	} {
		for _, tt := range tests {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				found, err := ruletest.RunWith(receiverbindsallinterfaces.New(), source,
					ruletest.Options{Env: tt.env})
				require.NoError(t, err)
				require.Len(t, found, 1)
				assert.Equal(t, tt.severity, found[0].Severity)
				assert.Contains(t, found[0].Message, tt.message)

				if tt.env.Kubernetes {
					assert.Contains(t, found[0].Hint, "hostNetwork")
					assert.NotContains(t, found[0].Hint, "MY_POD_IP")
				}
			})
		}
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
