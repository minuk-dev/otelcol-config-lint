package ruleset_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
)

// check runs every rule over src and returns the names of the rules that fired.
func check(t *testing.T, src string) []string {
	t.Helper()

	ctx, err := ruletest.Context(src, ruletest.Options{})
	require.NoError(t, err, "parse")

	var fired []string

	for _, r := range ruleset.All() {
		if len(rule.Run(r, ctx, r.Severity())) > 0 {
			fired = append(fired, r.Name())
		}
	}

	return fired
}

// checkRule runs one rule, named the way the command line names it, so a test
// about two rules meeting can say which of them reported.
func checkRule(t *testing.T, name, src string) diag.Diagnostics {
	t.Helper()

	return checkRuleIn(t, name, src, rule.Environment{})
}

// checkRuleIn is checkRule for the rules that only report once they know where
// the config is deployed.
func checkRuleIn(t *testing.T, name, src string, env rule.Environment) diag.Diagnostics {
	t.Helper()

	r, ok := ruleset.Lookup(name)
	require.Truef(t, ok, "rule %q is not registered", name)

	found, err := ruletest.RunWith(r, src, ruletest.Options{Env: env})
	require.NoError(t, err, "parse")

	return found
}

func fired(rules []string, name string) bool { return slices.Contains(rules, name) }

// extending wraps extension settings in a config that is otherwise ruletest.Clean. The
// settings are written already indented by four spaces.
func extending(name, settings string) string {
	return `
receivers: {otlp: }
exporters: {debug: }
extensions:
  ` + name + `:
` + settings + `
service:
  extensions: [` + name + `]
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`
}

// limiter wraps memory_limiter settings in a config that is otherwise ruletest.Clean.
// The settings are written already indented by four spaces.
func limiter(name, settings string) string {
	return `
receivers: {otlp: }
processors:
  ` + name + `:
` + settings + `
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [` + name + `], exporters: [debug]}
`
}

// sized wraps a memory_limiter with a hard limit written in MiB.
func sized(limitMiB string) string {
	return limiter("memory_limiter", "    check_interval: 1s\n    limit_mib: "+limitMiB+"\n    spike_limit_mib: 64")
}

func TestCleanConfigIsQuiet(t *testing.T) {
	t.Parallel()

	if got := check(t, ruletest.Clean); len(got) > 0 {
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
			src:  "recievers:\n  otlp:\n" + ruletest.Clean,
			want: "unknown-top-level-key",
		},
		{
			name: "no service block",
			src:  "receivers:\n  otlp:\n",
			want: "service-required",
		},
		{
			name: "unknown key in service",
			src:  ruletest.Clean + "  telemetrey:\n    logs:\n      level: debug\n",
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
			name: "storage extension named by a queue but never declared",
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			want: "undefined-extension-reference",
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
		{
			name: "a queue with nowhere to persist to",
			src: `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
			want: "no-persistent-queue",
		},
		{
			name: "a debug exporter logging every record it is given",
			src: `
receivers: {otlp: }
exporters:
  debug:
    verbosity: detailed
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "debug-exporter-verbosity",
		},
		{
			name: "zpages reachable from off the host",
			src: `
receivers: {otlp: }
exporters: {debug: }
extensions:
  zpages:
    endpoint: 0.0.0.0:55679
service:
  extensions: [zpages]
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "debug-extension-exposed",
		},
		{
			name: "a receiver bound to every interface",
			src: `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			want: "receiver-binds-all-interfaces",
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

// config.Ref.Path is the path from the root of the document, so a rule
// reporting a reference has nothing to prepend to it. Every rule that reports
// one is here, since the mistake is the kind that spreads by being copied from
// the rule next door.
func TestServiceReferencePathsAreWrittenOnce(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rule string
		src  string
		path string
	}{
		"an extension the service block enables but nobody declares": {
			rule: "undefined-reference",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  extensions: [zpages]
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			path: "service.extensions[0]",
		},
		"a receiver a pipeline names but nobody declares": {
			rule: "undefined-reference",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp, jaeger], exporters: [debug]}
`,
			path: "service.pipelines.traces.receivers[1]",
		},
		"a receiver listed twice in one slot": {
			rule: "duplicate-reference",
			src: `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp, otlp], exporters: [debug]}
`,
			path: "service.pipelines.traces.receivers[1]",
		},
		"a receiver that does not carry the pipeline's signal": {
			rule: "signal-support",
			src: `
receivers: {jaeger: }
exporters: {debug: }
service:
  pipelines:
    metrics: {receivers: [jaeger], exporters: [debug]}
`,
			path: "service.pipelines.metrics.receivers[0]",
		},
		"a memory_limiter that is not the first processor": {
			rule: "processor-order",
			src: `
receivers: {otlp: }
processors: {batch: , memory_limiter: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch, memory_limiter], exporters: [debug]}
`,
			path: "service.pipelines.traces.processors[1]",
		},
		// processor-order's other clause, which reports a different processor
		// in the same slot and so is a second place the prefix could come back.
		"a batch that runs before another processor": {
			rule: "processor-order",
			src: `
receivers: {otlp: }
processors: {batch: , batch/second: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch, batch/second], exporters: [debug]}
`,
			path: "service.pipelines.traces.processors[0]",
		},
		// The third clause, which reports the processor in the middle of the
		// chain rather than at either end of it.
		"an enrichment processor behind a sampler": {
			rule: "processor-order",
			src: `
receivers: {otlp: }
processors: {tail_sampling: , k8sattributes: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [tail_sampling, k8sattributes], exporters: [debug]}
`,
			path: "service.pipelines.traces.processors[1]",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, tt.rule, tt.src)
			require.NotEmpty(t, found)
			assert.Equal(t, tt.path, found[0].Path)
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

	r, ok := ruleset.Lookup("unknown-field")
	if !ok {
		t.Fatal("unknown-field rule is not registered")
	}

	found, err := ruletest.RunWith(r, src, ruletest.Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) != 1 || found[0].Severity != diag.Error {
		t.Fatalf("want one error, got %+v", found)
	}
}

func TestDisabledRuleProducesNothing(t *testing.T) {
	t.Parallel()

	ctx, err := ruletest.Context("receivers:\n  otlp:\n", ruletest.Options{})
	if err != nil {
		t.Fatal(err)
	}

	r, _ := ruleset.Lookup("service-required")
	if found := rule.Run(r, ctx, diag.Off); len(found) != 0 {
		t.Errorf("a rule set to off must not report: %+v", found)
	}
}

func TestEveryRuleIsDocumented(t *testing.T) {
	t.Parallel()

	for _, r := range ruleset.All() {
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

			found := checkRule(t, tt.rule, tt.src)
			require.Len(t, found, 1)
			assert.Equal(t, tt.message, found[0].Message)
			assert.Equal(t, tt.hint, found[0].Hint)
		})
	}
}

// TestExtensionNobodyStartsIsNotReported pins the other half of the rule's
// premise: service.extensions is what instantiates an extension, so a
// declaration left out of it binds no port for anyone to reach.
func TestExtensionNobodyStartsIsNotReported(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters: {debug: }
extensions:
  pprof:
    endpoint: 0.0.0.0:1777
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`

	assert.Empty(t, checkRule(t, "debug-extension-exposed", src))
	// The declaration is not thereby unremarked: another rule owns it.
	assert.NotEmpty(t, checkRule(t, "unused-component", src))
}

// The debugging extensions are reported once, by the rule that says what they
// hand out; the health checks are not reported at all, since the kubelet
// reaches a liveness probe from off the container's loopback interface.
func TestReceiverBindsAllInterfacesLeavesExtensionsToTheirOwnRules(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"pprof", "zpages"} {
		src := extending(typ, "    endpoint: 0.0.0.0:1777")
		assert.Empty(t, checkRule(t, "receiver-binds-all-interfaces", src),
			"%s is debug-extension-exposed's to report", typ)
		assert.NotEmpty(t, checkRule(t, "debug-extension-exposed", src),
			"%s should still be reported by the rule that owns it", typ)
	}

	for _, typ := range []string{"health_check", "healthcheckv2"} {
		assert.Empty(t, checkRule(t, "receiver-binds-all-interfaces",
			extending(typ, "    endpoint: 0.0.0.0:13133")),
			"%s bound to every interface is a correct deployment", typ)
	}
}

// TestFindingsCiteUpstream pins that a rule reporting what the collector
// requires says where upstream says so, rather than asking to be believed.
func TestFindingsCiteUpstream(t *testing.T) {
	t.Parallel()

	const memoryLimiterDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/memorylimiterprocessor/README.md"

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", ""))
	if len(found) == 0 {
		t.Fatal("an empty memory_limiter should be reported")
	}

	for _, d := range found {
		if d.Docs != memoryLimiterDocs {
			t.Errorf("finding %q links to %q, want the processor's README", d.Message, d.Docs)
		}
	}

	env := rule.Environment{Kubernetes: true, MemoryRequest: 128 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	for _, d := range checkRuleIn(t, "memory-limiter-sizing", sized("512"), env) {
		if d.Docs == "" {
			t.Errorf("finding %q cites nothing", d.Message)
		}
	}
}

// TestNoBuiltInRuleTakesSettingsYet records where rules.settings stands: the
// schema is there for the rules that will want it, and no rule in the set reads
// a block. When the first one does, this test is the reminder to document it.
func TestNoBuiltInRuleTakesSettingsYet(t *testing.T) {
	t.Parallel()

	for _, r := range ruleset.All() {
		if _, ok := r.(rule.Configurable); ok {
			t.Errorf("%s now takes settings -- document its block under rules.settings", r.Name())
		}
	}
}
