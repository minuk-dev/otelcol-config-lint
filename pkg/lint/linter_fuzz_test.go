package lint_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/settings"
)

// Keep mutations bounded while admitting the committed core schema fixture.
const maxFuzzBytes = 128 * 1024

func addFixtureSeeds(f *testing.F, patterns ...string) {
	f.Helper()

	for _, pattern := range patterns {
		paths, err := filepath.Glob("../../testdata/" + pattern)
		require.NoError(f, err)
		require.NotEmpty(f, paths, "seed pattern %s", pattern)

		for _, path := range paths {
			src, err := os.ReadFile(path)
			require.NoError(f, err)
			require.LessOrEqual(f, len(src), maxFuzzBytes, "seed %s", path)
			f.Add(src)
		}
	}
}

func addConfigSeeds(f *testing.F) {
	f.Helper()
	addFixtureSeeds(f, "valid/*.yaml", "invalid/*.yaml", "aliases/*.yaml",
		"rules/undefined-reference.yaml", "rules/wrong-node-type.yaml", "rules/duplicate-key.yaml",
		"rules/invalid-component-id.yaml", "rules/connector-wiring.yaml", "rules/invalid-value.yaml")

	for _, src := range []string{
		"", "null", "[]", "receivers: {}\n---\n", "service: [broken",
		"service: {pipelines: {traces: {receivers: [null, {}, [], 1], exporters: [debug]}}}",
		"processors:\n  batch: &loop {nested: *loop}\n",
		"processors:\n  batch/base: &base {timeout: 5s}\n  batch: {<<: *base}\n",
		"exporters: {debug: null}\nservice: {pipelines: null}\n",
		"service:\r  pipelines: null\r", "service:\u0085  pipelines: null\u0085",
	} {
		f.Add([]byte(src))
	}
}

func FuzzConfigParse(f *testing.F) {
	addConfigSeeds(f)

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > maxFuzzBytes {
			t.Skip()
		}

		first, firstErr := config.Parse("fuzz.yaml", src)
		second, secondErr := config.Parse("fuzz.yaml", src)
		require.Equal(t, firstErr == nil, secondErr == nil)

		if firstErr != nil {
			require.EqualError(t, secondErr, firstErr.Error())

			var syn *config.SyntaxError

			require.ErrorAs(t, firstErr, &syn)
			assert.Equal(t, "fuzz.yaml", syn.Path)

			return
		}

		require.NotNil(t, first)
		require.NotNil(t, first.Service)
		assert.Equal(t, first, second)
	})
}

func FuzzSettingsParse(f *testing.F) {
	addFixtureSeeds(f, "settings/valid/*.yaml", "settings/invalid/*.yaml", "rules/*.settings.yaml")

	for _, src := range []string{
		"", "null", "[]", "version: '1'\n---\n", "output: [broken",
		"run: null\nrules: {settings: {batch-size-bounds: null}}\n",
		"rules: {enable: &rules [unknown-field], disable: *rules}\n",
		"output: &loop {format: *loop}\n",
	} {
		f.Add([]byte(src))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > maxFuzzBytes {
			t.Skip()
		}

		first, firstErr := settings.Parse(src)
		second, secondErr := settings.Parse(src)
		require.Equal(t, firstErr == nil, secondErr == nil)

		if firstErr == nil {
			require.NotNil(t, first)
			assert.Equal(t, first, second)
		}
	})
}

func FuzzSchemaRead(f *testing.F) {
	addFixtureSeeds(f, "schemas/core/v0.157.0.json")

	for _, src := range []string{
		"", "null", "{}", "[]", "components: {}\n---\n", "components: [broken",
		"components: {receiver: {otlp: null}}",
		"components: {receiver: {otlp: {fields: {type: map, children: {protocols: null}}}}}",
		"components: {receiver: {otlp: &base {}, other: *base}}",
		"components: {receiver: {otlp: {fields: &loop {children: {nested: *loop}}}}}",
	} {
		f.Add([]byte(src))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > maxFuzzBytes {
			t.Skip()
		}

		first, firstErr := schema.Read(bytes.NewReader(src))
		second, secondErr := schema.Read(bytes.NewReader(src))
		require.Equal(t, firstErr == nil, secondErr == nil)

		// Invalid maps may report a different first invalid component or field.
		if firstErr == nil {
			require.NotNil(t, first)
			assert.Equal(t, first, second)
		}
	})
}

func FuzzLint(f *testing.F) {
	addConfigSeeds(f)

	cat, err := schema.ReadFile(repoSchemas + "/core/v0.157.0.json")
	require.NoError(f, err)

	l := lint.New(lint.Options{Schema: cat, Strict: true})

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > maxFuzzBytes {
			t.Skip()
		}

		first := l.Lint(t.Context(), "fuzz.yaml", src)
		second := l.Lint(t.Context(), "fuzz.yaml", src)
		assert.Equal(t, first.Status, second.Status)
		assert.Equal(t, first.Message(), second.Message())
		assert.Equal(t, first.Diagnostics, second.Diagnostics)
		assert.Contains(t, []lint.Status{lint.Valid, lint.Invalid}, first.Status)

		// YAML accepts CR, NEL and Unicode line separators as well as LF.
		normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u0085", "\n",
			"\u2028", "\n", "\u2029", "\n").Replace(string(src))
		lineCount := strings.Count(normalized, "\n") + 1

		for _, d := range first.Diagnostics {
			assert.Equal(t, "fuzz.yaml", d.Position.File)
			require.GreaterOrEqual(t, d.Position.Line, 0)
			require.GreaterOrEqual(t, d.Position.Column, 0)

			if d.Position.Line > 0 {
				require.LessOrEqual(t, d.Position.Line, lineCount)

				// yaml.v3 syntax errors can carry a line without a column.
				if d.Rule != "yaml-syntax" {
					assert.Positive(t, d.Position.Column)
				}
			}
		}
	})
}
