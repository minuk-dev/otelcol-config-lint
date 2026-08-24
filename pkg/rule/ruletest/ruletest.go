// Package ruletest builds the fixtures a rule's tests are written against.
//
// A rule package tests its own rule, so every one of them needs the same two
// things: a config parsed into the shape a rule reads, and a schema that does
// not change meaning when the embedded schemas are regenerated. Both live here
// rather than being copied into thirty-odd packages.
//
// It imports nothing a test brings with it -- no testing, no assertion library
// -- so a failure is reported where the test can say what it expected.
package ruletest

import (
	"fmt"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Options are the parts of a rule.Context a test may want to set. The zero
// value is the ordinary case: the schema below, no environment, not strict.
type Options struct {
	// Env describes where the config is deployed, for the rules that need one.
	Env rule.Environment
	// Strict mirrors the linter's --strict flag.
	Strict bool
	// Schema overrides the stand-in schema. A nil Schema uses Schema().
	Schema *schema.Schema
}

// Run parses src and checks it with one rule, at that rule's own severity.
func Run(r rule.Rule, src string) (diag.Diagnostics, error) {
	return RunWith(r, src, Options{})
}

// RunWith parses src and checks it with one rule, in the context the options
// describe.
func RunWith(r rule.Rule, src string, opts Options) (diag.Diagnostics, error) {
	ctx, err := Context(src, opts)
	if err != nil {
		return nil, err
	}

	return rule.Run(r, ctx, r.Severity()), nil
}

// Context parses src into the context a rule is run against, so a test that
// needs to reach into the file or the index can do so.
func Context(src string, opts Options) (rule.Context, error) {
	f, err := config.Parse("test.yaml", []byte(src))
	if err != nil {
		return rule.Context{}, fmt.Errorf("parsing the fixture: %w", err)
	}

	sch := opts.Schema
	if sch == nil {
		sch = Schema()
	}

	return rule.Context{
		File:   f,
		Schema: sch,
		Index:  rule.NewIndex(f, sch),
		Strict: opts.Strict,
		Env:    opts.Env,
	}, nil
}

// Reports whether any finding mentions substr.
func Reports(found diag.Diagnostics, substr string) bool {
	for _, d := range found {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}

	return false
}

// Clean is a config every rule should be happy with. A rule's own tests start
// from it, so a finding it produces is about the line the test added.
const Clean = `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
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

// Schema is a hand-built stand-in for a generated schema, so rule tests do not
// change meaning when the embedded schemas are regenerated.
func Schema() *schema.Schema {
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
				// otlphttp is the exporter with a queue, so the rules that read
				// one have a type whose schema says it has it. The debug and
				// otlp entries above deliberately do not, which is what a
				// schema that describes an exporter without a queue looks like.
				"otlphttp": {Type: "otlphttp", Signals: []config.Signal{"traces", "metrics", "logs"},
					Fields: &schema.Field{Type: "map", Children: map[string]*schema.Field{
						"endpoint": {Type: "string"},
						"sending_queue": {Type: "map", Open: true, Children: map[string]*schema.Field{
							"enabled":    {Type: "bool"},
							"queue_size": {Type: "int"},
							"storage":    {Type: "string"},
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
