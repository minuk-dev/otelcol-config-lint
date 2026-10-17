package insecuretls_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/insecuretls"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestConfiguredCAWithSkippedVerification(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, settings string
		count          int
		specific       bool
	}{
		{name: "configured CA", settings: "ca_file: ca.pem, insecure_skip_verify: true", count: 1, specific: true},
		{name: "no CA", settings: "insecure_skip_verify: true", count: 1, specific: false},
		{name: "empty CA", settings: "ca_file: '', insecure_skip_verify: true", count: 1, specific: false},
		{name: "dynamic CA", settings: "ca_file: '${env:CA}', insecure_skip_verify: true", count: 1, specific: false},
		{name: "numeric CA", settings: "ca_file: 12, insecure_skip_verify: true", count: 1, specific: false},
		{name: "verification enabled", settings: "ca_file: ca.pem, insecure_skip_verify: false", count: 0, specific: false},
		{name: "insecure transport", settings: "ca_file: ca.pem, insecure: true", count: 1, specific: false},
		{name: "merged CA", settings: "'<<': {ca_file: ca.pem}, insecure_skip_verify: true", count: 1, specific: false},
		{name: "YAML merged CA", settings: "<<: {ca_file: ca.pem}, insecure_skip_verify: true", count: 1, specific: true},
		{name: "unrelated CA", settings: "ca_file: ca.pem, nested: {insecure_skip_verify: true}", count: 1, specific: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "exporters:\n  otlp:\n    nested:\n      tls: {" + tt.settings + "}\n"
			found, err := ruletest.Run(insecuretls.New(), source)
			require.NoError(t, err)
			require.Len(t, found, tt.count)

			if tt.count == 0 {
				return
			}

			if tt.specific {
				assert.Contains(t, found[0].Message, "despite a configured ca_file")
				assert.Contains(t, found[0].Hint, "set it to false")
				assert.NotContains(t, found[0].Hint, "supply ca_file")
			} else {
				assert.NotContains(t, found[0].Message, "despite a configured ca_file")
			}
		})
	}
}
