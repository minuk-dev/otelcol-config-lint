package rule_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// testSchema is a hand-built stand-in for a generated schema, so rule tests
// do not change meaning when the embedded schemas are regenerated.
func testSchema() *schema.Schema {
	return &schema.Schema{
		CollectorVersion: "v9.9.9",
		Components: map[config.Kind]map[string]*schema.Component{
			config.KindReceiver: {
				"otlp": {Type: "otlp", Signals: []config.Signal{"traces", "metrics", "logs"},
					Stability: map[string]schema.Stability{"traces": "stable", "metrics": "stable", "logs": "stable"}},
				"jaeger": {Type: "jaeger", Signals: []config.Signal{"traces"},
					Stability: map[string]schema.Stability{"traces": "beta"}},
				"filelog": {Type: "filelog", Signals: []config.Signal{"logs"},
					Stability: map[string]schema.Stability{"logs": "alpha"}},
			},
			config.KindProcessor: {
				"batch":          {Type: "batch", Signals: []config.Signal{"traces", "metrics", "logs"}},
				"memory_limiter": {Type: "memory_limiter", Signals: []config.Signal{"traces", "metrics", "logs"}},
			},
			config.KindExporter: {
				"debug": {Type: "debug", Signals: []config.Signal{"traces", "metrics", "logs"},
					Fields: &schema.Field{Type: "map", Children: map[string]*schema.Field{
						"verbosity":        {Type: "string", Enum: []string{"basic", "normal", "detailed"}},
						"sampling_initial": {Type: "int"},
					}}},
				"otlp": {Type: "otlp", Signals: []config.Signal{"traces", "metrics", "logs"},
					Fields: &schema.Field{Type: "map", Required: []string{"endpoint"},
						Children: map[string]*schema.Field{
							"endpoint": {Type: "string"},
							"timeout":  {Type: "duration"},
							"headers":  {Type: "map", Open: true},
							"tls": {Type: "map", Children: map[string]*schema.Field{
								"insecure": {Type: "bool"},
							}},
						}}},
				"logging": {Type: "logging", Signals: []config.Signal{"traces"},
					Deprecated: "use the debug exporter instead"},
			},
			config.KindExtension: {
				"zpages": {Type: "zpages", Stability: map[string]schema.Stability{"extension": "beta"}},
			},
			config.KindConnector: {
				"spanmetrics": {Type: "spanmetrics", Pairs: []schema.Pair{{From: "traces", To: "metrics"}}},
			},
		},
	}
}

// check runs every rule over src and returns the names of the rules that fired.
func check(t *testing.T, src string) []string {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	ctx := rule.Context{File: f, Schema: testSchema(), Index: rule.NewIndex(f)}

	var fired []string

	for _, r := range rule.All() {
		if len(rule.Run(r, ctx, r.Severity())) > 0 {
			fired = append(fired, r.Name())
		}
	}

	return fired
}

func fired(rules []string, name string) bool { return slices.Contains(rules, name) }

// clean is a config that every rule should be happy with.
const clean = `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  batch:
exporters:
  otlp:
    endpoint: backend:4317
extensions:
  zpages:
service:
  extensions: [zpages]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp]
`

func TestCleanConfigIsQuiet(t *testing.T) {
	t.Parallel()

	if got := check(t, clean); len(got) > 0 {
		t.Errorf("expected no findings, got %v", got)
	}
}

func TestRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string // the rule that must fire
	}{
		{
			name: "unknown top-level key",
			src:  "recievers:\n  otlp:\n" + clean,
			want: "unknown-top-level-key",
		},
		{
			name: "no service block",
			src:  "receivers:\n  otlp:\n",
			want: "service-required",
		},
		{
			name: "unknown key in service",
			src:  clean + "  telemetrey:\n    logs:\n      level: debug\n",
			want: "unknown-service-key",
		},
		{
			name: "pipeline key is not a signal",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    tracez:
      receivers: [otlp]
      exporters: [debug]
`,
			want: "invalid-pipeline-key",
		},
		{
			name: "pipeline without exporters",
			src: `
receivers: {otlp: }
service:
  pipelines:
    traces:
      receivers: [otlp]
`,
			want: "empty-pipeline",
		},
		{
			name: "unknown key inside a pipeline",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
      connectors: [spanmetrics]
`,
			want: "unknown-pipeline-key",
		},
		{
			name: "duplicate mapping key",
			src: `
receivers:
  otlp:
  otlp:
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "duplicate-key",
		},
		{
			name: "pipeline slot is a mapping",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces:
      receivers:
        otlp: {}
      exporters: [debug]
`,
			want: "wrong-node-type",
		},
		{
			name: "reference to an undeclared component",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp, jaeger], exporters: [debug]}
`,
			want: "undefined-reference",
		},
		{
			name: "declared but never referenced",
			src: `
receivers: {otlp: , jaeger: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "unused-component",
		},
		{
			name: "same component twice in one slot",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp, otlp], exporters: [debug]}
`,
			want: "duplicate-reference",
		},
		{
			name: "connector with no producer",
			src: `
receivers: {otlp: }
exporters: {debug: }
connectors: {spanmetrics: }
service:
  pipelines:
    metrics: {receivers: [spanmetrics], exporters: [debug]}
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "connector-wiring",
		},
		{
			name: "component type not in the schema",
			src: `
receivers: {kafka: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [kafka], exporters: [debug]}
`,
			want: "unknown-component",
		},
		{
			name: "receiver used for the wrong signal",
			src: `
receivers: {jaeger: }
exporters: {debug: }
service:
  pipelines:
    metrics: {receivers: [jaeger], exporters: [debug]}
`,
			want: "signal-support",
		},
		{
			name: "connector wired backwards",
			src: `
receivers: {otlp: }
exporters: {debug: }
connectors: {spanmetrics: }
service:
  pipelines:
    traces: {receivers: [otlp, spanmetrics], exporters: [debug]}
    metrics: {receivers: [otlp], exporters: [debug, spanmetrics]}
`,
			want: "signal-support",
		},
		{
			name: "alpha component",
			src: `
receivers: {filelog: }
exporters: {debug: }
service:
  pipelines:
    logs: {receivers: [filelog], exporters: [debug]}
`,
			want: "component-stability",
		},
		{
			name: "deprecated component",
			src: `
receivers: {otlp: }
exporters: {logging: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [logging]}
`,
			want: "deprecated-component",
		},
		{
			name: "unknown setting",
			src: `
receivers: {otlp: }
exporters:
  debug:
    verbosty: detailed
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "unknown-field",
		},
		{
			name: "missing required setting",
			src: `
receivers: {otlp: }
exporters:
  otlp:
    timeout: 5s
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			want: "required-field",
		},
		{
			name: "value outside the allowed set",
			src: `
receivers: {otlp: }
exporters:
  debug:
    verbosity: chatty
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "invalid-value",
		},
		{
			name: "duration written as a bare number",
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    timeout: 5
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			want: "invalid-value",
		},
		{
			name: "memory_limiter is not first",
			src: `
receivers: {otlp: }
processors: {batch: , memory_limiter: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch, memory_limiter], exporters: [debug]}
`,
			want: "processor-order",
		},
		{
			name: "tls verification disabled",
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    tls:
      insecure: true
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			want: "insecure-tls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := check(t, tt.src)
			if !fired(got, tt.want) {
				t.Errorf("rule %q did not fire; fired: %v", tt.want, got)
			}
		})
	}
}

func TestEnvExpansionSkipsValueChecks(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: ${env:BACKEND}
    timeout: ${env:TIMEOUT}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`
	if got := check(t, src); fired(got, "invalid-value") {
		t.Errorf("expansions must not be type-checked; fired: %v", got)
	}
}

func TestOpenMapAcceptsAnyKey(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    headers:
      x-tenant: acme
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`
	if got := check(t, src); fired(got, "unknown-field") {
		t.Errorf("open maps must accept any key; fired: %v", got)
	}
}

func TestStrictPromotesUnknownFieldToError(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  debug:
    verbosty: detailed
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`

	f, err := config.Parse("test.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	r, ok := rule.Lookup("unknown-field")
	if !ok {
		t.Fatal("unknown-field rule is not registered")
	}

	ctx := rule.Context{File: f, Schema: testSchema(), Index: rule.NewIndex(f), Strict: true}

	found := rule.Run(r, ctx, r.Severity())
	if len(found) != 1 || found[0].Severity != diag.Error {
		t.Fatalf("want one error, got %+v", found)
	}
}

func TestDisabledRuleProducesNothing(t *testing.T) {
	t.Parallel()

	f, err := config.Parse("test.yaml", []byte("receivers:\n  otlp:\n"))
	if err != nil {
		t.Fatal(err)
	}

	r, _ := rule.Lookup("service-required")

	ctx := rule.Context{File: f, Schema: testSchema(), Index: rule.NewIndex(f)}
	if found := rule.Run(r, ctx, diag.Off); len(found) != 0 {
		t.Errorf("a rule set to off must not report: %+v", found)
	}
}

func TestEveryRuleIsDocumented(t *testing.T) {
	t.Parallel()

	for _, r := range rule.All() {
		if r.Description() == "" {
			t.Errorf("rule %q has no description", r.Name())
		}

		if strings.ToLower(r.Name()) != r.Name() {
			t.Errorf("rule %q should be lowercase kebab-case", r.Name())
		}

		switch r.Severity() {
		case diag.Error, diag.Warning, diag.Info:
		default:
			t.Errorf("rule %q has an unusable default severity %q", r.Name(), r.Severity())
		}
	}
}

// A finding is a sentence a reader acts on, so the wiring rules are pinned on
// what they actually say, not only on having fired.
func TestWiringFindingsReadAsSentences(t *testing.T) {
	t.Parallel()

	const removeOrReference = "remove it, or reference it so it actually runs"

	tests := map[string]struct {
		rule    string
		src     string
		message string
		hint    string
	}{
		"an extension nothing enables": {
			rule: "unused-component",
			src: `
receivers: {otlp: }
exporters: {debug: }
extensions: {zpages: }
service:
  extensions: []
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			message: `extension "zpages" is declared but not listed in service.extensions`,
			hint:    removeOrReference,
		},
		"a processor no pipeline lists": {
			rule: "unused-component",
			src: `
receivers: {otlp: }
processors: {batch: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			message: `processor "batch" is declared but referenced by no pipeline`,
			hint:    removeOrReference,
		},
		"a processor listed as an extension": {
			rule: "undefined-reference",
			src: `
receivers: {otlp: }
processors: {batch: }
exporters: {debug: }
service:
  extensions: [batch]
  pipelines:
    traces: {receivers: [otlp], processors: [batch], exporters: [debug]}
`,
			message: `service.extensions references "batch" which is not declared under extensions`,
			hint:    `"batch" is declared under processors; it cannot be used as an extension`,
		},
		"an extension listed as an exporter": {
			rule: "undefined-reference",
			src: `
receivers: {otlp: }
extensions: {zpages: }
service:
  extensions: [zpages]
  pipelines:
    traces: {receivers: [otlp], exporters: [zpages]}
`,
			message: `pipeline "traces" references exporter "zpages" which is not declared under exporters`,
			hint:    `"zpages" is declared under extensions; it cannot be used as an exporter`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, tt.rule, tt.src, rule.Environment{})
			require.Len(t, found, 1)
			assert.Equal(t, tt.message, found[0].Message)
			assert.Equal(t, tt.hint, found[0].Hint)
		})
	}
}
