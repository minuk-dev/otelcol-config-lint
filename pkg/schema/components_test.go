package schema_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// The releases the availability fixtures below are written against, newest
// first, the way a registry lists them.
//
//nolint:gochecknoglobals // a fixture the tests in this file share
var fixtureReleases = []string{"v0.157.0", "v0.150.0", "v0.140.0", "v0.130.0", "v0.110.0"}

func TestSpansOfIsOpenAtTheNewestRelease(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		present []string
		want    []schema.Span
	}{{
		name:    "shipped throughout",
		present: fixtureReleases,
		want:    []schema.Span{{From: "v0.110.0", To: ""}},
	}, {
		name:    "added along the way",
		present: []string{"v0.157.0", "v0.150.0"},
		want:    []schema.Span{{From: "v0.150.0", To: ""}},
	}, {
		name:    "dropped along the way",
		present: []string{"v0.110.0", "v0.130.0"},
		want:    []schema.Span{{From: "v0.110.0", To: "v0.130.0"}},
	}, {
		name:    "dropped and brought back",
		present: []string{"v0.110.0", "v0.140.0", "v0.157.0"},
		want: []schema.Span{
			{From: "v0.110.0", To: "v0.110.0"},
			{From: "v0.140.0", To: "v0.140.0"},
			{From: "v0.157.0", To: ""},
		},
	}, {
		name:    "never shipped",
		present: nil,
		want:    nil,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := schema.SpansOf(fixtureReleases, set(tt.present))
			assert.Equal(t, tt.want, got)

			// Whatever the spans came out as, reading them back names the
			// releases they were built from.
			comps := components(got)
			assert.ElementsMatch(t, tt.present,
				comps.Expand(distContrib, fixtureReleases)[config.KindReceiver]["otlp"])
		})
	}
}

// TestExpandIsAskedAgainstTheReleasesServedNow pins that an open span means
// "still shipped", not "shipped for ever": a registry that has dropped its
// oldest releases must not be quoted as still serving them, and one that has
// gained a release since is answered for the releases it has.
func TestExpandIsAskedAgainstTheReleasesServedNow(t *testing.T) {
	t.Parallel()

	comps := components([]schema.Span{{From: "v0.130.0", To: ""}})

	pruned := []string{"v0.157.0", "v0.150.0", "v0.140.0"}
	assert.Equal(t, []string{"v0.140.0", "v0.150.0", "v0.157.0"},
		comps.Expand(distContrib, pruned)[config.KindReceiver]["otlp"])

	assert.Empty(t, comps.Expand("nosuch", fixtureReleases),
		"a distribution the document does not cover has no answer")
	assert.False(t, comps.Has("nosuch"))
	assert.True(t, comps.Has(distContrib))
}

func TestComponentsRoundTrip(t *testing.T) {
	t.Parallel()

	comps := components([]schema.Span{{From: "v0.110.0", To: "v0.130.0"}, {From: "v0.150.0", To: ""}})

	var buf bytes.Buffer

	require.NoError(t, comps.Write(&buf))

	// The open span is what keeps a new release from rewriting the entry of
	// every component it did not change.
	assert.NotContains(t, buf.String(), `"to": "v0.157.0"`)

	read, err := schema.ReadComponents(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	assert.Equal(t, comps, read)

	_, err = schema.ReadComponents(strings.NewReader("{"))
	require.Error(t, err, "a truncated document should not decode")
}

func TestReadComponentsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, schema.ComponentsFile)

	write(t, path, `{"distributions":{"contrib":{"receiver":{"otlp":[{"from":"v0.110.0"}]}}}}`)

	comps, err := schema.ReadComponentsFile(path)
	require.NoError(t, err)
	assert.True(t, comps.Has(distContrib))

	_, err = schema.ReadComponentsFile(filepath.Join(dir, "absent.json"))
	require.Error(t, err)
}

// TestAvailabilityFromADirectory covers the registry a checkout serves: the
// document sits beside the index and is read for the store's distribution.
func TestAvailabilityFromADirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)
	write(t, filepath.Join(root, schema.ComponentsFile),
		`{"distributions":{`+
			`"contrib":{"receiver":{"otlp":[{"from":"v0.157.0"}],"logging":[{"from":"v0.110.0","to":"v0.150.0"}]}},`+
			`"core":{"receiver":{"otlp":[{"from":"v0.157.0"}]}}}}`)

	// The index this fixture writes lists v0.157.0 alone, so a span that ended
	// before it names nothing the registry can still serve.
	store := schema.Store{Locations: []string{root}}

	avail := store.Availability(t.Context())
	require.NotNil(t, avail)
	assert.Equal(t, []string{latestVersion}, avail[config.KindReceiver]["otlp"])
	assert.Empty(t, avail[config.KindReceiver]["logging"])

	// A distribution the document does not cover is no answer at all, so the
	// caller can tell it apart from "nothing was ever shipped".
	assert.Nil(t, store.WithDistribution("k8s").Availability(t.Context()),
		"an uncovered distribution should not answer")
}

// TestAvailabilityIsOneFetch is the point of the whole document: a registry
// that publishes one is asked once, instead of once per release.
func TestAvailabilityIsOneFetch(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		switch r.URL.Path {
		case "/" + schema.IndexFile:
			_, _ = w.Write([]byte(`{"distributions":{"contrib":["v0.157.0","v0.150.0","v0.110.0"]}}`))
		case "/" + schema.ComponentsFile:
			_, _ = w.Write([]byte(`{"distributions":{"contrib":{"receiver":{` +
				`"otlp":[{"from":"v0.110.0"}],"logging":[{"from":"v0.110.0","to":"v0.150.0"}]}}}}`))
		default:
			t.Errorf("unexpected request for %s: a published index answers without reading a schema", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	store := schema.Store{
		Locations:     []string{srv.URL},
		AllowInsecure: true,
		NoCache:       true,
	}

	avail := store.Availability(t.Context())
	require.NotNil(t, avail)
	assert.Equal(t, []string{"v0.110.0", "v0.150.0", "v0.157.0"}, avail[config.KindReceiver]["otlp"])
	assert.Equal(t, []string{"v0.110.0", "v0.150.0"}, avail[config.KindReceiver]["logging"],
		"a closed span should stop where the component was dropped")

	// The index and the availability document, once each; asking again is
	// answered from what the process already read.
	assert.Equal(t, int64(2), requests.Load())

	store.Availability(t.Context())
	assert.Equal(t, int64(2), requests.Load(), "a second question should not be a second fetch")
}

// TestAvailabilityWithoutADocument covers every registry published before this
// file existed: there is no answer, rather than an empty one.
func TestAvailabilityWithoutADocument(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)

	assert.Nil(t, schema.Store{Locations: []string{root}}.Availability(t.Context()))
	assert.Nil(t, schema.Store{Locations: []string{repoSchemas + "/contrib/v0.157.0.json"}}.
		Availability(t.Context()), "a location naming one file publishes nothing")
}

func TestRemoteReportsWhatCostsARequest(t *testing.T) {
	t.Parallel()

	assert.True(t, schema.Store{}.Remote(), "the default location is the published registry")
	assert.True(t, schema.Store{Locations: []string{schema.Default}}.Remote())
	assert.True(t, schema.Store{Locations: []string{repoSchemas, schema.Default}}.Remote(),
		"a store that falls through to the network reads over it")
	assert.False(t, schema.Store{Locations: []string{repoSchemas}}.Remote())
}

// components wraps one component's spans in a document, which is what most of
// the assertions above are about.
func components(spans []schema.Span) *schema.Components {
	return &schema.Components{Distributions: map[string]schema.ComponentSpans{
		distContrib: {config.KindReceiver: {"otlp": spans}},
	}}
}

// set turns a list of releases into what SpansOf takes.
func set(versions []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range versions {
		out[v] = true
	}

	return out
}
