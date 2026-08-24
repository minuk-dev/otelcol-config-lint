package schema_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// TestReadTakesTheWholeFile covers what a schema file is allowed to hold. A
// second document is the interesting one: everything after the first would be
// dropped, and nothing about the outcome would say a component went missing.
func TestReadTakesTheWholeFile(t *testing.T) {
	t.Parallel()

	const one = "collectorVersion: v0.157.0\ncomponents:\n  receiver:\n    otlp: {}\n"

	sch, err := schema.Read(strings.NewReader(one))
	require.NoError(t, err)
	assert.Equal(t, "v0.157.0", sch.CollectorVersion)

	// The type is filled in from the key it was written under.
	comp, ok := sch.Lookup(config.KindReceiver, "otlp")
	require.True(t, ok)
	assert.Equal(t, "otlp", comp.Type)

	_, err = schema.Read(strings.NewReader(one + "---\n" + one))
	require.Error(t, err, "a second document should not be dropped in silence")

	// The second document is read strictly too, so a broken one is reported as
	// what it is rather than as an extra document.
	_, err = schema.Read(strings.NewReader(one + "---\nnotAField: true\n"))
	require.Error(t, err)

	_, err = schema.Read(strings.NewReader(""))
	require.Error(t, err, "an empty file is not a schema")
}
