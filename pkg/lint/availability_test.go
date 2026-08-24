package lint_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// walkLimit is the bound the index puts on reading schemas one release at a
// time, kept here so the assertions below say what they are testing. It is
// lint's own constant; a test naming another number is a test of nothing.
const walkLimit = 12

// TestVersionIndexAsksThePublishedAvailability covers what the whole document
// is for: one unknown component used to download every schema in the registry,
// which for the published one is a hundred megabytes to produce a single hint.
func TestVersionIndexAsksThePublishedAvailability(t *testing.T) {
	t.Parallel()

	srv, asked := availabilityServer(t, map[string]string{
		schema.IndexFile: `{"distributions":{"contrib":["v0.157.0","v0.150.0","v0.110.0"]},` +
			`"extensions":{"contrib":".json"}}`,
		schema.ComponentsFile: `{"distributions":{"contrib":{"receiver":{` +
			`"otlp":[{"from":"v0.110.0"}],"logging":[{"from":"v0.110.0","to":"v0.150.0"}]}}}}`,
	})
	defer srv.Close()

	index := lint.NewVersionIndex(remoteStore(srv.URL))

	assert.Equal(t, []string{"v0.110.0", "v0.150.0"},
		index.Versions(t.Context(), config.KindReceiver, "logging"),
		"the hint should name the releases the component was shipped in")
	assert.Equal(t, []string{"v0.110.0", "v0.150.0", "v0.157.0"},
		index.Versions(t.Context(), config.KindReceiver, "otlp"))
	assert.Empty(t, index.Versions(t.Context(), config.KindReceiver, "nosuch"))

	assert.Empty(t, asked(t, "/contrib/"),
		"a published availability index should answer without reading a schema")
}

// TestVersionIndexFallsBackToTheSchemas covers a registry published before the
// availability index existed, and the checkout of one: the answer is still
// there, worked out from the schemas themselves.
func TestVersionIndexFallsBackToTheSchemas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localRegistry(t, root, "v0.157.0", "v0.110.0")

	index := lint.NewVersionIndex(schema.Store{Locations: []string{root}})

	// filelog is written into the older release alone, so the walk has to say
	// where a dropped component did exist.
	assert.Equal(t, []string{"v0.110.0"},
		index.Versions(t.Context(), config.KindReceiver, "filelog"))
	assert.Equal(t, []string{"v0.110.0", "v0.157.0"},
		index.Versions(t.Context(), config.KindReceiver, "otlp"))
}

// TestVersionIndexBoundsTheWalkItPaysFor pins the cost of that fallback
// against a remote registry, where every release read is a multi-megabyte
// download: the newest releases are what a hint is usually about, and the rest
// are not worth the requests.
func TestVersionIndexBoundsTheWalkItPaysFor(t *testing.T) {
	t.Parallel()

	const releases = walkLimit + 8

	files := map[string]string{schema.ComponentsFile: ""} // published by no registry of this age

	versions := make([]string, 0, releases)

	for i := range releases {
		v := fmt.Sprintf("v0.%d.0", 157-i)
		versions = append(versions, v)
		files["contrib/"+v+".json"] = `{"collectorVersion":"` + v + `","distribution":"contrib",` +
			`"components":{"receiver":{"otlp":{"type":"otlp"}}}}`
	}

	listed, err := json.Marshal(versions)
	require.NoError(t, err)

	files[schema.IndexFile] = `{"distributions":{"contrib":` + string(listed) + `},` +
		`"extensions":{"contrib":".json"}}`

	srv, asked := availabilityServer(t, files)
	defer srv.Close()

	index := lint.NewVersionIndex(remoteStore(srv.URL))

	got := index.Versions(t.Context(), config.KindReceiver, "otlp")
	assert.Len(t, got, walkLimit, "the walk read more releases than it is allowed to")
	assert.Equal(t, versions[0], got[len(got)-1], "the newest release should be among them")
	assert.NotContains(t, got, versions[len(versions)-1],
		"the oldest releases are what the bound gives up")
	assert.Len(t, asked(t, "/contrib/"), walkLimit)
}

// TestVersionIndexDropsAWalkItCouldNotFinish covers the failure the hint used
// to hide: a throttled registry drops releases from the walk, and whichever
// one it dropped is the one the hint would have named. Nothing is a better
// answer than a plausible wrong one.
func TestVersionIndexDropsAWalkItCouldNotFinish(t *testing.T) {
	t.Parallel()

	versions := []string{"v0.157.0", "v0.150.0", "v0.140.0", "v0.110.0"}
	files := map[string]string{
		schema.IndexFile: `{"distributions":{"contrib":["v0.157.0","v0.150.0","v0.140.0","v0.110.0"]},` +
			`"extensions":{"contrib":".json"}}`,
		schema.ComponentsFile: "",
	}

	// The two oldest releases answer; the newer ones are throttled, which is
	// the shape a rate limit takes when a walk asks for a registry at once.
	for _, v := range versions[2:] {
		files["contrib/"+v+".json"] = `{"collectorVersion":"` + v + `","distribution":"contrib",` +
			`"components":{"receiver":{"otlp":{"type":"otlp"}}}}`
	}

	srv, _ := availabilityServer(t, files)
	defer srv.Close()

	index := lint.NewVersionIndex(remoteStore(srv.URL))

	assert.Empty(t, index.Versions(t.Context(), config.KindReceiver, "otlp"),
		"half a registry cannot say where a component was added")
}

// remoteStore reads a registry served by one of the test servers below. The
// cache is off: what a test served should not outlive it.
func remoteStore(url string) schema.Store {
	return schema.Store{Locations: []string{url}, AllowInsecure: true, NoCache: true}
}

// availabilityServer serves a registry from the files it is given, recording
// what was asked for. A file with empty content is one the registry does not
// publish; every other path is answered as throttled, which is what a registry
// under a rate limit does.
func availabilityServer(t *testing.T, files map[string]string) (*httptest.Server, func(*testing.T, string) []string) {
	t.Helper()

	var (
		lock  sync.Mutex
		asked []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()

		asked = append(asked, r.URL.Path)

		lock.Unlock()

		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]

		switch {
		case ok && body != "":
			_, _ = w.Write([]byte(body))
		case ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			// 403 is what GitHub answers a rate-limited reader with, and is not
			// retried, so a test spends no time waiting on it.
			w.WriteHeader(http.StatusForbidden)
		}
	}))

	return srv, func(t *testing.T, prefix string) []string {
		t.Helper()

		lock.Lock()
		defer lock.Unlock()

		var out []string

		for _, path := range asked {
			if strings.HasPrefix(path, prefix) {
				out = append(out, path)
			}
		}

		return out
	}
}

// localRegistry writes a registry directory holding one contrib schema per
// release. The oldest carries filelog as well, so a walk has a component that
// was dropped along the way to report on.
func localRegistry(t *testing.T, root string, versions ...string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "contrib"), 0o750))

	listed, err := json.Marshal(versions)
	require.NoError(t, err)

	writeAt(t, filepath.Join(root, schema.IndexFile),
		`{"distributions":{"contrib":`+string(listed)+`}}`)

	for i, v := range versions {
		components := `"otlp":{"type":"otlp"}`
		if i == len(versions)-1 {
			components += `,"filelog":{"type":"filelog"}`
		}

		writeAt(t, filepath.Join(root, "contrib", v+".json"),
			`{"collectorVersion":"`+v+`","distribution":"contrib",`+
				`"components":{"receiver":{`+components+`}}}`)
	}
}

func writeAt(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
