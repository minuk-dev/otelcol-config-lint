package rule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestDynamicReferencesDoNotEstablishUsage(t *testing.T) {
	t.Parallel()

	src := strings.Replace(ruletest.Clean, "extensions: [zpages]", `extensions: ["${env:ID}"]`, 1)
	src = strings.Replace(src, "receivers: [otlp]", `receivers: ["${env:ID}"]`, 1)
	ctx, err := ruletest.Context(src, ruletest.Options{})
	require.NoError(t, err)

	assert.True(t, ctx.Index.HasDynamicRefs(config.KindExtension))
	assert.False(t, ctx.Index.Enabled(config.ParseID("zpages")))
	assert.False(t, ctx.Index.Used(config.KindExtension, config.ParseID("zpages")))
	assert.True(t, ctx.Index.HasDynamicRefs(config.KindReceiver))
	assert.True(t, ctx.Index.HasDynamicRefs(config.KindConnector))
	assert.False(t, ctx.Index.Used(config.KindReceiver, config.ParseID("otlp")))
	assert.False(t, ctx.Index.HasDynamicRefs(config.KindExporter))
	assert.True(t, ctx.Index.Used(config.KindExporter, config.ParseID("otlp")))
}
