package rule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// checkRule runs one rule over src in a given deployment environment.
func checkRule(t *testing.T, name, src string, env rule.Environment) diag.Diagnostics {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	require.NoError(t, err, "parse")

	r, ok := rule.Lookup(name)
	require.Truef(t, ok, "rule %q is not registered", name)

	return rule.Run(r, rule.Context{File: f, Schema: testSchema(), Index: rule.NewIndex(f), Env: env}, r.Severity())
}

// checkRuleAgainst runs one rule over src as if the run targeted the named
// collector release, which is what decides the defaults a component takes when
// it writes no setting of its own.
func checkRuleAgainst(t *testing.T, name, release, src string) diag.Diagnostics {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	require.NoError(t, err, "parse")

	r, ok := rule.Lookup(name)
	require.Truef(t, ok, "rule %q is not registered", name)

	sch := testSchema()
	sch.CollectorVersion = release

	return rule.Run(r, rule.Context{File: f, Schema: sch, Index: rule.NewIndex(f)}, r.Severity())
}

// reports whether any finding mentions substr.
func reports(found diag.Diagnostics, substr string) bool {
	for _, d := range found {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}

	return false
}

// limiter wraps memory_limiter settings in a config that is otherwise clean.
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

func TestMemoryLimiterConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     string // a phrase the finding has to carry
	}{
		"nothing set at all": {
			settings: "",
			want:     "'check_interval' must be greater than zero",
		},
		"no limit": {
			settings: "    check_interval: 1s",
			want:     "'limit_mib' or 'limit_percentage' must be greater than zero",
		},
		"check_interval is zero": {
			settings: "    check_interval: 0s\n    limit_mib: 512",
			want:     "'check_interval' must be greater than zero",
		},
		"check_interval written as a bare zero": {
			settings: "    check_interval: 0\n    limit_mib: 512",
			want:     "'check_interval' must be greater than zero",
		},
		"limit set to zero": {
			settings: "    check_interval: 1s\n    limit_mib: 0",
			want:     "'limit_mib' or 'limit_percentage' must be greater than zero",
		},
		"spike at the limit": {
			settings: "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 512",
			want:     "'spike_limit_mib' must be smaller than 'limit_mib'",
		},
		"spike above the limit": {
			settings: "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 600",
			want:     "'spike_limit_mib' must be smaller than 'limit_mib'",
		},
		"spike percentage at the limit": {
			settings: "    check_interval: 1s\n    limit_percentage: 50\n    spike_limit_percentage: 50",
			want:     "'spike_limit_percentage' must be smaller than 'limit_percentage'",
		},
		"a percentage above a hundred": {
			settings: "    check_interval: 1s\n    limit_percentage: 120",
			want:     "less than or equal to hundred",
		},
		"a spike percentage above a hundred": {
			settings: "    check_interval: 1s\n    limit_percentage: 50\n    spike_limit_percentage: 120",
			want:     "less than or equal to hundred",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", tt.settings), rule.Environment{})
			if !reports(found, tt.want) {
				t.Errorf("no finding mentioned %q; got %+v", tt.want, found)
			}

			for _, d := range found {
				if d.Severity != diag.Error {
					continue
				}

				if !strings.Contains(d.Message, "memory_limiter") {
					t.Errorf("a finding should name the instance: %q", d.Message)
				}
			}
		})
	}
}

func TestMemoryLimiterConfigAcceptsAWorkingLimiter(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 128"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("a valid memory_limiter should be quiet, got %+v", found)
	}

	// A percentage limiter is just as valid as a fixed one.
	settings = "    check_interval: 1s\n    limit_percentage: 80\n    spike_limit_percentage: 25"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("a percentage memory_limiter should be quiet, got %+v", found)
	}
}

func TestMemoryLimiterConfigMatchesOnType(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter/aggressive", ""), rule.Environment{})
	if !reports(found, "memory_limiter/aggressive") {
		t.Errorf("a named instance should be checked and named, got %+v", found)
	}
}

func TestMemoryLimiterConfigLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: ${env:INTERVAL}\n    limit_mib: ${env:LIMIT}\n    spike_limit_mib: ${env:SPIKE}"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("values resolved at runtime cannot be checked, got %+v", found)
	}
}

func TestMemoryLimiterConfigRemarksOnAFarOffInterval(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: 30s\n    limit_mib: 512\n    spike_limit_mib: 128"

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings), rule.Environment{})
	if len(found) != 1 || found[0].Severity != diag.Info {
		t.Fatalf("want one info about the interval, got %+v", found)
	}

	settings = "    check_interval: 2s\n    limit_mib: 512\n    spike_limit_mib: 128"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("an interval near the recommended one is a choice, not a finding: %+v", found)
	}
}

// sized is a memory_limiter with a fixed limit, for the sizing tests.
func sized(limitMiB string) string {
	return limiter("memory_limiter", "    check_interval: 1s\n    limit_mib: "+limitMiB+"\n    spike_limit_mib: 64")
}

func TestMemoryLimiterSizing(t *testing.T) {
	t.Parallel()

	container := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi}

	tests := map[string]struct {
		src  string
		env  rule.Environment
		want diag.Severity
		says string
	}{
		"a limit at the container's": {
			src: sized("512"), env: container,
			want: diag.Error, says: "before the limiter engages",
		},
		"a limit above the container's": {
			src: sized("1024"), env: container,
			want: diag.Error, says: "before the limiter engages",
		},
		"no headroom for the process itself": {
			src: sized("480"), env: container,
			want: diag.Warning, says: "leaving",
		},
		"above the documented ceiling": {
			src: sized("440"), env: container,
			want: diag.Warning, says: "above 80%",
		},
		"a percentage of a container with no limit": {
			src:  limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 80"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 0},
			want: diag.Warning, says: "no memory limit",
		},
		"a percentage above the ceiling": {
			src:  limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 90"),
			env:  container,
			want: diag.Warning, says: "above 80%",
		},
		"more than the pod asked for": {
			src:  sized("400"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 512 * quantity.Mi},
			want: diag.Info, says: "memory request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "memory-limiter-sizing", tt.src, tt.env)
			if !reports(found, tt.says) {
				t.Fatalf("no finding mentioned %q; got %+v", tt.says, found)
			}

			for _, d := range found {
				if strings.Contains(d.Message, tt.says) && d.Severity != tt.want {
					t.Errorf("want severity %q, got %q for %q", tt.want, d.Severity, d.Message)
				}
			}
		})
	}
}

func TestMemoryLimiterSizingIsSilentWithoutAnEnvironment(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]rule.Environment{
		"nothing configured":         {},
		"kubernetes with no numbers": {Kubernetes: true},
		"numbers but not kubernetes": {MemoryLimit: 512 * quantity.Mi},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if found := checkRule(t, "memory-limiter-sizing", sized("4096"), env); len(found) > 0 {
				t.Errorf("nothing should be reported without an environment, got %+v", found)
			}
		})
	}
}

func TestMemoryLimiterSizingFitsTheContainer(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi}
	if found := checkRule(t, "memory-limiter-sizing", sized("400"), env); len(found) > 0 {
		t.Errorf("a limiter that fits its container should be quiet, got %+v", found)
	}
}

func TestMemoryLimiterSizingNamesEveryInstance(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 4096
  memory_limiter/aggressive:
    check_interval: 1s
    limit_mib: 8192
exporters: {debug: }
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, memory_limiter/aggressive]
      exporters: [debug]
`
	env := rule.Environment{Kubernetes: true, MemoryRequest: 0, MemoryLimit: 512 * quantity.Mi}

	found := checkRule(t, "memory-limiter-sizing", src, env)
	if !reports(found, "\"memory_limiter\"") || !reports(found, "\"memory_limiter/aggressive\"") {
		t.Errorf("both instances share the one container limit and both should be named: %+v", found)
	}
}

// TestMemoryLimiterSizingDoesNotInventANumber pins that a limit_mib too large
// to hold as a byte count leaves the hard limit unknown. Multiplying it out
// wraps, and a wrapped product lands back in a plausible range: the limiter
// below would otherwise be reported as enforcing exactly the container's 512Mi,
// a figure that appears nowhere in the config.
func TestMemoryLimiterSizingDoesNotInventANumber(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 0, MemoryLimit: 512 * quantity.Mi}

	// 2^44 MiB is 2^64 bytes plus the 512Mi that the wrap leaves behind.
	assert.Empty(t, checkRule(t, "memory-limiter-sizing", sized("17592186044928"), env),
		"a limit that does not fit in a byte count cannot be sized")

	// The same for a percentage large enough to overflow the multiplication.
	src := limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 99999999999999")
	assert.Empty(t, checkRule(t, "memory-limiter-sizing", src, env),
		"a percentage that does not fit cannot be sized")
}

func TestMemoryLimiterSizingLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	src := limiter("memory_limiter", "    check_interval: 1s\n    limit_mib: ${env:LIMIT}")

	if found := checkRule(t, "memory-limiter-sizing", src, env); len(found) > 0 {
		t.Errorf("a limit resolved at runtime cannot be sized, got %+v", found)
	}
}

// batcher wraps batch processor settings in a config that is otherwise clean.
// The settings are written already indented by four spaces.
func batcher(name, settings string) string {
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

func TestBatchSizeBounds(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     string // a phrase the finding has to carry
	}{
		"a cap below the default size": {
			settings: "    send_batch_max_size: 1000",
			want:     "send_batch_max_size must be greater or equal to send_batch_size",
		},
		"a cap below the size written next to it": {
			settings: "    send_batch_size: 4096\n    send_batch_max_size: 1000",
			want:     "sets send_batch_max_size to 1000 and send_batch_size to 4096",
		},
		"a negative timeout": {
			settings: "    timeout: -5s",
			want:     "timeout must be greater or equal to 0",
		},
		"a key listed twice": {
			settings: "    metadata_keys: [tenant, tenant]",
			want:     `duplicate entry in metadata_keys: "tenant" (case-insensitive)`,
		},
		"a key listed twice in another case": {
			settings: "    metadata_keys: [tenant, Tenant]",
			want:     `duplicate entry in metadata_keys: "tenant" (case-insensitive)`,
		},
		"a negative cap": {
			settings: "    send_batch_size: 100\n    send_batch_max_size: -1",
			want:     "which the field cannot hold",
		},
		"a size larger than a uint32": {
			settings: "    send_batch_size: 5000000000\n    send_batch_max_size: 20000",
			want:     "which the field cannot hold",
		},
		"a size the field cannot hold next to one resolved at runtime": {
			settings: "    send_batch_size: ${env:SIZE}\n    send_batch_max_size: -1",
			want:     "which the field cannot hold",
		},
		"a cap written in octal": {
			settings: "    send_batch_max_size: 010000",
			want:     "sets send_batch_max_size to 4096",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "batch-size-bounds", batcher("batch", tt.settings), rule.Environment{})
			assert.Truef(t, reports(found, tt.want), "no finding mentioned %q; got %+v", tt.want, found)

			for _, d := range found {
				assert.Containsf(t, d.Message, "batch", "a finding should name the instance: %q", d.Message)
				assert.Containsf(t, d.Docs, "processor/batchprocessor/README.md",
					"finding %q links to %q, want the processor's README", d.Message, d.Docs)
			}
		})
	}
}

// TestBatchSizeBoundsSaysTheDefaultIsADefault pins that the one number in the
// message the reader will not find in their file is named as a default, not
// quoted back at them as if they had written it.
func TestBatchSizeBoundsSaysTheDefaultIsADefault(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "batch-size-bounds", batcher("batch", "    send_batch_max_size: 1000"), rule.Environment{})
	assert.Truef(t, reports(found, "below the default send_batch_size of 8192"),
		"the message should say 8192 is the default, got %+v", found)
}

// TestBatchSizeBoundsDiagnosesTheRealConstraint pins that a size the uint32
// field cannot hold is reported as what it is. Such a value fails to decode, so
// the collector never reaches the bounds check, and comparing the two numbers
// would hint at a fix that still does not load.
func TestBatchSizeBoundsDiagnosesTheRealConstraint(t *testing.T) {
	t.Parallel()

	settings := "    send_batch_size: 5000000000\n    send_batch_max_size: 20000"

	found := checkRule(t, "batch-size-bounds", batcher("batch", settings), rule.Environment{})
	require.Lenf(t, found, 1, "want one finding about the size that does not fit, got %+v", found)
	assert.NotContains(t, found[0].Message, "must be greater or equal to send_batch_size")
	assert.Contains(t, found[0].Hint, "between 0 and 4294967295")
}

// TestBatchSizeBoundsLeavesAMergedConfigAlone pins that a merge key stops the
// default from being filled in. The document is read as written, so a
// send_batch_size the merge supplies looks absent here, while the collector
// resolves it before either number is read.
func TestBatchSizeBoundsLeavesAMergedConfigAlone(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
processors:
  batch/defaults: &defaults
    send_batch_size: 500
  batch:
    <<: *defaults
    send_batch_max_size: 1000
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch], exporters: [debug]}
`
	assert.Empty(t, checkRule(t, "batch-size-bounds", src, rule.Environment{}),
		"a size the merge key supplies is not the default")
}

// TestBatchSizeBoundsReadsTheBaseTheCollectorReads pins that a value is
// resolved the way confmap's own decoder resolves it. yaml.v3 reads a leading
// zero as octal and 0x as hex, so reading base 10 here would quote back a
// number the collector never sees -- and pass a config that will not start.
func TestBatchSizeBoundsReadsTheBaseTheCollectorReads(t *testing.T) {
	t.Parallel()

	// 010000 is 4096, below the 8192 default, however much it looks like ten
	// thousand.
	found := checkRule(t, "batch-size-bounds", batcher("batch", "    send_batch_max_size: 010000"),
		rule.Environment{})
	require.Lenf(t, found, 1, "an octal cap below the default should be reported, got %+v", found)
	assert.Contains(t, found[0].Message, "sets send_batch_max_size to 4096")

	// 0x400 is 1024, which base 10 cannot read at all; the collector can.
	found = checkRule(t, "batch-size-bounds", batcher("batch", "    send_batch_max_size: 0x400"), rule.Environment{})
	require.Lenf(t, found, 1, "a hex cap below the default should be reported, got %+v", found)
	assert.Contains(t, found[0].Message, "sets send_batch_max_size to 1024")
}

func TestBatchSizeBoundsAcceptsAWorkingBatch(t *testing.T) {
	t.Parallel()

	for name, settings := range map[string]string{
		"nothing set at all":               "",
		"a cap above the default size":     "    send_batch_max_size: 10000",
		"a cap above the size set":         "    send_batch_size: 1000\n    send_batch_max_size: 2000",
		"a cap equal to the size set":      "    send_batch_size: 1000\n    send_batch_max_size: 1000",
		"no cap at all":                    "    send_batch_size: 10000\n    send_batch_max_size: 0",
		"a size of zero, which is off":     "    send_batch_size: 0\n    send_batch_max_size: 1000",
		"a timeout of zero, which is fine": "    timeout: 0s",
		"keys that differ":                 "    metadata_keys: [tenant, region]",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, checkRule(t, "batch-size-bounds", batcher("batch", settings), rule.Environment{}),
				"a valid batch processor should be quiet")
		})
	}
}

func TestBatchSizeBoundsMatchesOnType(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "batch-size-bounds", batcher("batch/traces", "    send_batch_max_size: 1000"),
		rule.Environment{})
	assert.Truef(t, reports(found, "batch/traces"), "a named instance should be checked and named, got %+v", found)
}

func TestBatchSizeBoundsLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	for name, settings := range map[string]string{
		"the cap is resolved at runtime":  "    send_batch_max_size: ${env:MAX}",
		"the size is resolved at runtime": "    send_batch_size: ${env:SIZE}\n    send_batch_max_size: 1000",
		"a key is resolved at runtime":    `    metadata_keys: ["${env:KEY}", "${env:KEY}"]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, checkRule(t, "batch-size-bounds", batcher("batch", settings), rule.Environment{}),
				"values resolved at runtime cannot be checked")
		})
	}
}

// TestBatchSizeBoundsPointsAtTheDuplicate pins that a duplicate key is reported
// against the entry that repeats, not against the processor as a whole.
func TestBatchSizeBoundsPointsAtTheDuplicate(t *testing.T) {
	t.Parallel()

	src := batcher("batch", "    metadata_keys: [tenant, region, Tenant]")

	found := checkRule(t, "batch-size-bounds", src, rule.Environment{})
	require.Lenf(t, found, 1, "want one finding about the repeated key, got %+v", found)
	assert.Equal(t, "processors.batch.metadata_keys[2]", found[0].Path)
}

// TestFindingsCiteUpstream pins that a rule reporting what the collector
// requires says where upstream says so, rather than asking to be believed.
func TestFindingsCiteUpstream(t *testing.T) {
	t.Parallel()

	const memoryLimiterDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/memorylimiterprocessor/README.md"

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", ""), rule.Environment{})
	if len(found) == 0 {
		t.Fatal("an empty memory_limiter should be reported")
	}

	for _, d := range found {
		if d.Docs != memoryLimiterDocs {
			t.Errorf("finding %q links to %q, want the processor's README", d.Message, d.Docs)
		}
	}

	env := rule.Environment{Kubernetes: true, MemoryRequest: 128 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	for _, d := range checkRule(t, "memory-limiter-sizing", sized("512"), env) {
		if d.Docs == "" {
			t.Errorf("finding %q cites nothing", d.Message)
		}
	}
}
