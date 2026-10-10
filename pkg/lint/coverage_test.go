package lint_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidvalue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestCoverage(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		fields  *schema.Field
		src     string
		missing bool
		status  lint.Status
		skipped []lint.CoverageSkip
	}{
		{name: "complete", fields: &schema.Field{Type: "string"}, src: "localhost:4317",
			status: lint.Valid, skipped: nil, missing: false},
		{name: "missing component", fields: nil, src: "{}", status: lint.Valid, missing: true,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "missing_schema", Block: ""}}},
		{name: "missing fields", fields: nil, src: "{}", status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "missing_schema", Block: ""}}},
		{name: "open fields", fields: &schema.Field{Type: "map", Open: true}, src: "{anything: true}",
			status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "open_schema", Block: ""}}},
		{name: "unconstrained", fields: &schema.Field{}, src: "anything", status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "open_schema", Block: ""}}},
		{name: "open nested map", fields: &schema.Field{Type: "map", Children: map[string]*schema.Field{
			"headers": {Type: "map", Open: true}, "endpoint": {Type: "string"},
		}}, src: "{headers: {token: secret}, endpoint: localhost}", status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom.headers", Reason: "open_schema", Block: ""}}},
		{name: "partial coverage and detected error", fields: &schema.Field{Type: "map", Children: map[string]*schema.Field{
			"headers": {Type: "map", Open: true}, "endpoint": {Type: "int"},
		}}, src: "{headers: {token: secret}, endpoint: wrong}", status: lint.Invalid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom.headers", Reason: "open_schema", Block: ""}}},
		{name: "list missing items", fields: &schema.Field{Type: "list"}, src: "[one, two]",
			status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "missing_schema", Block: ""}}},
		{name: "runtime scalar", fields: &schema.Field{Type: "int"}, src: "${env:PORT}",
			status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{{Path: "receivers.custom", Reason: "runtime_value", Block: ""}}},
		{name: "runtime subtree", fields: &schema.Field{Type: "map"}, src: "${file:secret.yaml}",
			status: lint.Valid, missing: false,
			skipped: []lint.CoverageSkip{
				{Path: "receivers.custom", Reason: "open_schema", Block: ""},
				{Path: "receivers.custom", Reason: "runtime_value", Block: ""},
			}},
		{name: "detected error is not missing coverage", fields: &schema.Field{Type: "int"}, src: "wrong",
			status: lint.Invalid, skipped: nil, missing: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			components := map[string]*schema.Component{"custom": {Fields: tt.fields}}
			if tt.missing {
				components = map[string]*schema.Component{"other": {Fields: &schema.Field{Type: "string"}}}
			}

			linter := lint.New(lint.Options{
				Schema: &schema.Schema{CollectorVersion: "v0.157.0", Distribution: "acme",
					Components: map[config.Kind]map[string]*schema.Component{config.KindReceiver: components}},
				Rules: []rule.Rule{invalidvalue.New()}, MinSeverity: diag.Error, IgnoreMissingSchemas: true,
			})
			result := linter.Lint(t.Context(), "config.yaml", []byte("receivers:\n  custom: "+tt.src+"\n"))
			assert.Equal(t, tt.status, result.Status)
			require.NotNil(t, result.Coverage)
			assert.Equal(t, "v0.157.0", result.Coverage.CollectorVersion)
			assert.Equal(t, "acme", result.Coverage.Distribution)
			assert.Equal(t, []string{"invalid-value"}, result.Coverage.FieldRules)
			assert.Equal(t, tt.skipped, result.Coverage.Skipped)

			wantStatus := "complete"
			if len(tt.skipped) > 0 {
				wantStatus = "partial"
			}

			assert.Equal(t, wantStatus, result.Coverage.Status)
		})
	}
}

func TestCoverageRuntimeValuesInServiceAndAliases(t *testing.T) {
	t.Parallel()

	result := newLinter(t, lint.Options{}).Lint(t.Context(), "config.yaml", []byte(`
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: &endpoint ${env:SECRET_ENDPOINT}
      http:
        endpoint: *endpoint
exporters:
  debug:
service:
  extensions: ${env:EXTENSIONS}
  pipelines:
    traces:
      receivers: ["${env:RECEIVER}"]
      exporters: [debug]
`))
	require.NotNil(t, result.Coverage)

	var paths []string

	for _, skipped := range result.Coverage.Skipped {
		if skipped.Reason == "runtime_value" {
			paths = append(paths, skipped.Path)
		}
	}

	assert.Equal(t, []string{
		"receivers.otlp.protocols.grpc.endpoint", "receivers.otlp.protocols.http.endpoint",
		"service.extensions", "service.pipelines.traces.receivers[0]",
	}, paths)

	encoded, err := json.Marshal(result.Coverage)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SECRET_ENDPOINT")
}

func TestCoverageNotChecked(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		opts lint.Options
	}{
		{name: "structural only", opts: lint.Options{}},
		{name: "no field rules", opts: lint.Options{Rules: []rule.Rule{}}},
		{name: "field rule off", opts: lint.Options{Rules: []rule.Rule{invalidvalue.New()},
			Severities: map[string]diag.Severity{"invalid-value": diag.Off}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			linter := lint.New(tt.opts)
			if tt.name != "structural only" {
				linter = newLinter(t, tt.opts)
			}

			result := linter.Lint(t.Context(), "config.yaml", []byte(good))
			require.NotNil(t, result.Coverage)
			assert.Equal(t, "not_checked", result.Coverage.Status)

			if tt.name == "structural only" {
				assert.Empty(t, result.Coverage.CollectorVersion)
				assert.Equal(t, []lint.CoverageSkip{{Path: "", Reason: "missing_schema", Block: ""}}, result.Coverage.Skipped)
			} else {
				assert.Empty(t, result.Coverage.FieldRules)
			}
		})
	}
}

func TestCoverageEmbeddedBlocks(t *testing.T) {
	t.Parallel()

	src := `apiVersion: v1
kind: ConfigMap
metadata:
  name: collector
data:
  first.yaml: |
    exporters:
      debug: ${env:DEBUG}
  second.yaml: |
    exporters:
      debug: ${env:DEBUG}
`
	result := newLinter(t, lint.Options{Embedded: true}).Lint(t.Context(), "cm.yaml", []byte(src))
	require.NotNil(t, result.Coverage)
	assert.Equal(t, "partial", result.Coverage.Status)
	require.Len(t, result.Coverage.Skipped, 2)
	assert.Equal(t, "collector/first.yaml", result.Coverage.Skipped[0].Block)
	assert.Equal(t, "collector/second.yaml", result.Coverage.Skipped[1].Block)
}

func TestCoverageBoundedWalk(t *testing.T) {
	t.Parallel()

	var src strings.Builder

	src.WriteString("service:\n")

	for depth := 1; depth < rule.MaxSettingsDepth+2; depth++ {
		src.WriteString(strings.Repeat("  ", depth) + "nested:\n")
	}

	result := newLinter(t, lint.Options{}).Lint(t.Context(), "config.yaml", []byte(src.String()))
	require.NotNil(t, result.Coverage)
	require.Len(t, result.Coverage.Skipped, 1)
	assert.Equal(t, "unresolved_structure", result.Coverage.Skipped[0].Reason)
}

func TestCoverageReports(t *testing.T) {
	t.Parallel()

	linter := newLinter(t, lint.Options{Rules: []rule.Rule{invalidvalue.New()}})
	complete := linter.Lint(t.Context(), "complete.yaml", []byte("exporters:\n  debug:\n"))
	partial := linter.Lint(t.Context(), "partial.yaml", []byte("exporters:\n  debug: ${env:DEBUG}\n"))
	require.Equal(t, "complete", complete.Coverage.Status)
	require.Equal(t, "partial", partial.Coverage.Status)

	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			formatter, err := lint.NewFormatter(format, &buf, lint.FormatterOptions{Summary: true})
			require.NoError(t, err)

			var summary lint.Summary

			for _, result := range []lint.Result{complete, partial} {
				require.NoError(t, formatter.Result(result))
				summary.Add(result)
			}

			require.NoError(t, formatter.Finish(summary))
			assert.Equal(t, 1, summary.Incomplete)
			assert.False(t, summary.Failed())

			if format == "text" {
				assert.Contains(t, buf.String(), "partial.yaml: validation coverage partial")
				assert.Contains(t, buf.String(), "1 runtime value")
				assert.Contains(t, buf.String(), "Collector v0.157.0, distribution contrib")
				assert.NotContains(t, buf.String(), "complete.yaml")
				assert.NotContains(t, buf.String(), "${env:DEBUG}")
				assert.Contains(t, buf.String(), "coverage incomplete for 1 file(s)")

				return
			}

			var report struct {
				Files   []lint.Result `json:"files"`
				Summary lint.Summary  `json:"summary"`
			}

			require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
			require.Len(t, report.Files, 2, "complete and partial results remain visible without --verbose")
			assert.Equal(t, complete.Coverage, report.Files[0].Coverage)
			assert.Equal(t, partial.Coverage, report.Files[1].Coverage)
			assert.Equal(t, 1, report.Summary.Incomplete)
		})
	}
}
