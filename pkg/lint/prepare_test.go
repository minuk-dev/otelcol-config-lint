package lint_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestPrepareTargets(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		version      string
		distribution string
		allowNearest bool
		wantVersion  string
		wantFallback bool
		wantUnknown  bool
	}{
		{name: "latest", version: "", distribution: "", allowNearest: false,
			wantVersion: "v0.157.0", wantFallback: false, wantUnknown: false},
		{name: "exact", version: "0.157.0", distribution: "", allowNearest: false,
			wantVersion: "v0.157.0", wantFallback: false, wantUnknown: false},
		{name: "core", version: "v0.157.0", distribution: "core", allowNearest: false,
			wantVersion: "v0.157.0", wantFallback: false, wantUnknown: false},
		{name: "fallback", version: "v0.155.0", distribution: "", allowNearest: true,
			wantVersion: "v0.110.0", wantFallback: true, wantUnknown: false},
		{name: "exact required", version: "v0.155.0", distribution: "", allowNearest: false,
			wantVersion: "", wantFallback: false, wantUnknown: true},
		{name: "nothing older", version: "v0.1.0", distribution: "", allowNearest: true,
			wantVersion: "", wantFallback: false, wantUnknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := repoStore().WithDistribution(tt.distribution)
			linter, target, err := lint.Prepare(t.Context(), lint.PrepareOptions{
				Store: store, CollectorVersion: tt.version, AllowNearestFallback: tt.allowNearest,
			})

			if tt.wantUnknown {
				var unknown *schema.UnknownVersionError
				require.ErrorAs(t, err, &unknown)
				assert.Equal(t, tt.version, unknown.Version)
				assert.Nil(t, linter)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, linter)
			assert.Equal(t, tt.wantVersion, target.CollectorVersion)
			assert.Equal(t, tt.wantFallback, target.Fallback)

			requested := schema.Normalize(tt.version)
			if requested == "" {
				requested = schema.Latest
			}

			assert.Equal(t, requested, target.RequestedVersion)

			distribution := tt.distribution
			if distribution == "" {
				distribution = schema.DefaultDistribution
			}

			assert.Equal(t, distribution, target.Distribution)
		})
	}
}

func TestPreparePolicy(t *testing.T) {
	t.Parallel()

	linter, _, err := lint.Prepare(t.Context(), lint.PrepareOptions{
		Store: repoStore(),
		Rules: ruleset.Selection{
			Default: ruleset.DefaultNone, Enable: []string{"service-required"},
			Severity: []string{"service-required=warning"},
		},
		MinSeverity: diag.Error, FailOn: diag.Warning,
	})
	require.NoError(t, err)

	result := linter.Lint(t.Context(), "request.yaml", []byte("{}"))
	assert.Equal(t, lint.Invalid, result.Status, "hidden findings still determine validity")
	assert.Empty(t, result.Diagnostics)

	result = linter.Lint(t.Context(), "request.yaml", []byte("service:\n  pipelines:\n    traces: {}"))
	assert.Equal(t, lint.Valid, result.Status, "disabled rules must stay silent")
}

func TestPrepareValidatesBeforeReadingSchemas(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		opts    lint.PrepareOptions
		wantErr error
	}{
		{name: "rule", opts: lint.PrepareOptions{Rules: ruleset.Selection{Enable: []string{"typo"}}},
			wantErr: ruleset.ErrUnknownRule},
		{name: "rule severity", opts: lint.PrepareOptions{
			Rules: ruleset.Selection{Severity: []string{"service-required=typo"}},
		}, wantErr: diag.ErrUnknownSeverity},
		{name: "minimum severity", opts: lint.PrepareOptions{MinSeverity: "typo"},
			wantErr: diag.ErrUnknownSeverity},
		{name: "failure severity", opts: lint.PrepareOptions{FailOn: "typo"},
			wantErr: diag.ErrUnknownSeverity},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := &watchedFS{Fs: afero.NewOsFs(), opens: atomic.Int64{}}
			tt.opts.Store = repoStore()
			tt.opts.Store.Fs = fsys

			linter, _, err := lint.Prepare(t.Context(), tt.opts)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, linter)
			assert.Zero(t, fsys.opens.Load())
		})
	}
}

func TestPrepareRejectsEmptySchemaAndReportsAttemptedFallback(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fsys, "v1.0.0.json",
		[]byte(`{"collectorVersion":"v1.0.0","components":{}}`), 0o600))

	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			linter, target, err := lint.Prepare(t.Context(), lint.PrepareOptions{
				Store:            schema.Store{Locations: []string{"."}, Fs: fsys},
				CollectorVersion: version, AllowNearestFallback: true,
			})
			require.ErrorIs(t, err, lint.ErrEmptySchema)
			assert.Nil(t, linter)
			assert.Equal(t, version, target.RequestedVersion)
			assert.Equal(t, "v1.0.0", target.CollectorVersion)
			assert.Equal(t, version != "v1.0.0", target.Fallback)
		})
	}
}

func TestPrepareCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	fsys := &watchedFS{Fs: afero.NewOsFs(), opens: atomic.Int64{}}
	linter, _, err := lint.Prepare(ctx, lint.PrepareOptions{
		Store: schema.Store{Locations: []string{repoSchemas}, Fs: fsys},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, linter)
	assert.Zero(t, fsys.opens.Load())
}

func TestPrepareCancellationDuringSchemaLookup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	linter, _, err := lint.Prepare(ctx, lint.PrepareOptions{
		Store: schema.Store{
			Locations: []string{"."},
			Fs:        &cancelReadFS{Fs: afero.NewMemMapFs(), cancel: cancel, once: sync.Once{}},
		},
		CollectorVersion: "v1.0.0", AllowNearestFallback: true,
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, linter)
}

func TestPrepareUsesInputFs(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fsys, "request.yaml", []byte(good), 0o600))
	linter, _, err := lint.Prepare(t.Context(), lint.PrepareOptions{
		Store: repoStore(), Fs: fsys,
	})
	require.NoError(t, err)
	assert.Equal(t, lint.Valid, linter.LintFile(t.Context(), "request.yaml").Status)
}

func TestPreparedLinterConcurrentRequests(t *testing.T) {
	t.Parallel()

	linter, _, err := lint.Prepare(t.Context(), lint.PrepareOptions{Store: repoStore()})
	require.NoError(t, err)

	for i := range 16 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			src, want := good, lint.Valid
			if i%2 != 0 {
				src, want = "receivers:\n  imaginary: {}\nservice: {}", lint.Invalid
			}

			result := linter.Lint(t.Context(), "request.yaml", []byte(src))
			assert.Equal(t, want, result.Status)
			require.NoError(t, result.Err)
			assert.Equal(t, "request.yaml", result.Path)
		})
	}
}

func ExamplePrepare() {
	engine, target, err := lint.Prepare(context.Background(), lint.PrepareOptions{
		Store:            schema.Store{Locations: []string{"../../testdata/schemas"}},
		CollectorVersion: "v0.157.0",
		Rules: ruleset.Selection{
			Default: ruleset.DefaultNone, Enable: []string{"service-required"},
		},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	result := engine.Lint(context.Background(), "request.yaml", []byte("{}"))

	fmt.Println(target.CollectorVersion, target.Distribution)
	fmt.Println(result.Status, result.Diagnostics[0].Rule)
	// Output:
	// v0.157.0 contrib
	// invalid service-required
}
