package lint_test

import (
	"context"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestDistributionIndexRetriesCancelledBuild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localRegistry(t, root, "v0.157.0")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	store := schema.Store{
		Locations: []string{root}, Distribution: "core",
		Fs: &cancelReadFS{Fs: afero.NewOsFs(), cancel: cancel, once: sync.Once{}},
	}
	index := lint.NewDistributionIndex(store, "v0.157.0")
	assert.Empty(t, index.Distributions(ctx, config.KindReceiver, "otlp"))
	require.ErrorIs(t, ctx.Err(), context.Canceled)

	distributions := index.Distributions(t.Context(), config.KindReceiver, "otlp")
	require.Equal(t, []string{"contrib"}, distributions)
	distributions[0] = "changed by caller"

	assert.Equal(t, []string{"contrib"}, index.Distributions(t.Context(), config.KindReceiver, "otlp"))
}
