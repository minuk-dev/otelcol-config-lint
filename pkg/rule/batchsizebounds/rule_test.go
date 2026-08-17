package batchsizebounds_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/batchsizebounds"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(batchsizebounds.New(), src)
	require.NoError(t, err, "parse")

	return found
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

			found := check(t, batcher("batch", tt.settings))
			assert.Truef(t, ruletest.Reports(found, tt.want), "no finding mentioned %q; got %+v", tt.want, found)

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

	found := check(t, batcher("batch", "    send_batch_max_size: 1000"))
	assert.Truef(t, ruletest.Reports(found, "below the default send_batch_size of 8192"),
		"the message should say 8192 is the default, got %+v", found)
}

// TestBatchSizeBoundsDiagnosesTheRealConstraint pins that a size the uint32
// field cannot hold is reported as what it is. Such a value fails to decode, so
// the collector never reaches the bounds check, and comparing the two numbers
// would hint at a fix that still does not load.
func TestBatchSizeBoundsDiagnosesTheRealConstraint(t *testing.T) {
	t.Parallel()

	settings := "    send_batch_size: 5000000000\n    send_batch_max_size: 20000"

	found := check(t, batcher("batch", settings))
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
	assert.Empty(t, check(t, src),
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
	found := check(t, batcher("batch", "    send_batch_max_size: 010000"))
	require.Lenf(t, found, 1, "an octal cap below the default should be reported, got %+v", found)
	assert.Contains(t, found[0].Message, "sets send_batch_max_size to 4096")

	// 0x400 is 1024, which base 10 cannot read at all; the collector can.
	found = check(t, batcher("batch", "    send_batch_max_size: 0x400"))
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

			assert.Empty(t, check(t, batcher("batch", settings)),
				"a valid batch processor should be quiet")
		})
	}
}

func TestBatchSizeBoundsMatchesOnType(t *testing.T) {
	t.Parallel()

	found := check(t, batcher("batch/traces", "    send_batch_max_size: 1000"))
	assert.Truef(t, ruletest.Reports(found, "batch/traces"),
		"a named instance should be checked and named, got %+v", found)
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

			assert.Empty(t, check(t, batcher("batch", settings)),
				"values resolved at runtime cannot be checked")
		})
	}
}

// TestBatchSizeBoundsPointsAtTheDuplicate pins that a duplicate key is reported
// against the entry that repeats, not against the processor as a whole.
func TestBatchSizeBoundsPointsAtTheDuplicate(t *testing.T) {
	t.Parallel()

	src := batcher("batch", "    metadata_keys: [tenant, region, Tenant]")

	found := check(t, src)
	require.Lenf(t, found, 1, "want one finding about the repeated key, got %+v", found)
	assert.Equal(t, "processors.batch.metadata_keys[2]", found[0].Path)
}
