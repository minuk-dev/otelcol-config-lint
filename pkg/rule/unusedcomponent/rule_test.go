package unusedcomponent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unusedcomponent"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(unusedcomponent.New(), src)
	require.NoError(t, err, "parse")

	return found
}

// unused-component reports an extension the service block does not list. Once
// a component's settings name it, the two rules would be reporting the same
// line, and undefined-extension-reference is the one that explains it.
func TestSettingsReferenceSilencesUnusedComponent(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
extensions: {file_storage: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`

	assert.Empty(t, check(t, src),
		"an extension named by a component's settings is used")
}
