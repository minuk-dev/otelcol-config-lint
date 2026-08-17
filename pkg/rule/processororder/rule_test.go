package processororder_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/processororder"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(processororder.New(), src)
	require.NoError(t, err, "parse")

	return found
}

// ordering wraps a processor list in a config that is otherwise clean, with
// every processor in the list declared so the pipeline references nothing that
// does not exist.
func ordering(processors ...string) string {
	var declared strings.Builder
	for _, p := range processors {
		declared.WriteString("  " + p + ":\n")
	}

	return `
receivers: {otlp: }
processors:
` + declared.String() + `exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [` + strings.Join(processors, ", ") + `], exporters: [debug]}
`
}

// A sampler decides on what it can see, so an enrichment processor behind one
// is a policy matching attributes that are not there yet. None of these lists
// carry a memory_limiter or a batch, so what the rule reports is the middle of
// the chain and nothing else.
func TestProcessorOrderEnrichmentAfterSampling(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		processors []string
		want       int
	}{
		"k8s attributes added after the sampling decision": {
			processors: []string{"tail_sampling", "k8sattributes"},
			want:       1,
		},
		// The same two processors under the names upstream renamed them to,
		// which is what a config written against a recent release carries.
		"the k8s attributes processor under its new name": {
			processors: []string{"tail_sampling", "k8s_attributes"},
			want:       1,
		},
		"the resource detection processor under its new name": {
			processors: []string{"probabilistic_sampler", "resource_detection"},
			want:       1,
		},
		"the same two the way round that works": {
			processors: []string{"k8sattributes", "tail_sampling"},
			want:       0,
		},
		"cloud and host attributes added after the decision": {
			processors: []string{"probabilistic_sampler", "resourcedetection"},
			want:       1,
		},
		"attributes the config writes itself, added after the decision": {
			processors: []string{"tail_sampling", "resource"},
			want:       1,
		},
		// attributes is left out of the group on purpose: stripping fields
		// from what a sampler kept is the right order to do it in.
		"an attributes processor after the decision": {
			processors: []string{"tail_sampling", "attributes"},
			want:       0,
		},
		// filter belongs between enrichment and sampling in the documented
		// order, but a filter that drops early is a filter doing its job.
		"a filter after the decision": {
			processors: []string{"tail_sampling", "filter"},
			want:       0,
		},
		"two enrichment processors behind one sampler": {
			processors: []string{"tail_sampling", "k8sattributes", "resource"},
			want:       2,
		},
		"a pipeline with no sampler in it at all": {
			processors: []string{"resource", "k8sattributes"},
			want:       0,
		},
		// Matching on the type is what covers the named instances, which is
		// how a second sampling policy set is usually written.
		"named instances of both": {
			processors: []string{"tail_sampling/errors", "k8sattributes/pods"},
			want:       1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, ordering(tt.processors...))
			assert.Len(t, found, tt.want)

			for _, d := range found {
				assert.Equal(t, diag.Warning, d.Severity)
			}
		})
	}
}

// The finding has to name both processors, since neither one on its own says
// what is wrong, and it reports at the rule's own severity rather than at the
// batch clause's info.
func TestProcessorOrderEnrichmentReadsAsASentence(t *testing.T) {
	t.Parallel()

	found := check(t, ordering("memory_limiter", "tail_sampling", "k8sattributes/pods"))
	require.Len(t, found, 1)

	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Equal(t, `"k8sattributes/pods" runs after "tail_sampling" in pipeline "traces", `+
		"so sampling policies cannot match the attributes it adds", found[0].Message)
	assert.Equal(t, `move "k8sattributes/pods" ahead of "tail_sampling" in `+
		"service.pipelines.traces.processors so the decision is made against enriched telemetry",
		found[0].Hint)
	assert.Equal(t, "service.pipelines.traces.processors[2]", found[0].Path)
	assert.Contains(t, found[0].Docs, "processor/tailsamplingprocessor/README.md")
}

// Getting ahead of the first sampler clears the ones behind it, so that is the
// one the finding names -- and the page it cites is the one that sampler's
// decision is documented on.
func TestProcessorOrderNamesTheFirstSampler(t *testing.T) {
	t.Parallel()

	found := check(t, ordering("probabilistic_sampler", "tail_sampling", "resourcedetection"))
	require.Len(t, found, 1)

	assert.Contains(t, found[0].Message, `runs after "probabilistic_sampler"`)
	assert.Contains(t, found[0].Docs, "processor/probabilisticsamplerprocessor/README.md")
}
