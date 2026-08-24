package schema_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// TestMain keeps the on-disk schema cache out of the developer's own cache
// directory: a test fetching from a local server should not leave anything
// behind outside its own temporary files.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "schema-cache")
	if err != nil {
		panic(err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	_ = os.Setenv("XDG_CACHE_HOME", dir)

	m.Run()
}

func TestCompare(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		a, b string
		want int // sign of the expected result
	}{
		{"v0.157.0", "v0.110.0", 1},
		{"v0.110.0", "v0.157.0", -1},
		{"v0.110.0", "v0.110.0", 0},
		{"0.110.0", "v0.110.0", 0},
		{"v0.110.1", "v0.110.0", 1},
		{"v1.0.0", "v0.999.0", 1},
		{"v0.157.0-rc.1", "v0.157.0", -1},
	} {
		got := schema.Compare(tt.a, tt.b)
		if sign(got) != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ in, want string }{
		{"0.157.0", "v0.157.0"},
		{"v0.157.0", "v0.157.0"},
		{" 0.157.0", "v0.157.0"}, // leading space is trimmed
		{"latest", "latest"},
	} {
		if got := schema.Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTheRepositoryRegistryLoads(t *testing.T) {
	t.Parallel()

	store := schema.Store{Locations: []string{repoSchemas}}

	versions := store.Versions(t.Context())
	if len(versions) == 0 {
		t.Fatal("the registry lists no schemas")
	}

	for i := 1; i < len(versions); i++ {
		if schema.Compare(versions[i-1], versions[i]) <= 0 {
			t.Fatalf("versions are not sorted newest first: %v", versions)
		}
	}

	latest, err := store.Load(t.Context(), schema.Latest)
	if err != nil {
		t.Fatal(err)
	}

	if latest.CollectorVersion != versions[0] {
		t.Errorf("latest resolved to %q, want %q", latest.CollectorVersion, versions[0])
	}

	if latest.Count() < 100 {
		t.Errorf("schema looks too small: %d components", latest.Count())
	}

	if _, ok := latest.Lookup(config.KindReceiver, "otlp"); !ok {
		t.Error("the otlp receiver should exist in every release")
	}
}

func TestUnknownVersionSuggestsTheNearestOlder(t *testing.T) {
	t.Parallel()

	store := schema.Store{Locations: []string{repoSchemas}}

	_, err := store.Load(t.Context(), "v0.115.0")
	if err == nil {
		t.Fatal("want an error for a version with no schema")
	}

	var unknown *schema.UnknownVersionError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *schema.UnknownVersionError, got %T", err)
	}

	near, found := unknown.Nearest()
	if !found {
		t.Fatal("want a nearest version")
	}

	if schema.Compare(near, "v0.115.0") > 0 {
		t.Errorf("nearest %q is newer than the request", near)
	}
}

func TestDirectoryLocationWinsOverEmbedded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "v0.157.0.json"),
		`{"collectorVersion":"v0.157.0","components":{"receiver":{"custom":{"type":"custom","signals":["logs"]}}}}`)

	store := schema.Store{Locations: []string{dir, repoSchemas}}

	cat, err := store.Load(t.Context(), "v0.157.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Count() != 1 {
		t.Errorf("want the local schema, got %d components", cat.Count())
	}

	// Versions not present locally still fall through to the next location.
	_, err = store.Load(t.Context(), "v0.110.0")
	if err != nil {
		t.Errorf("fallthrough to the registry failed: %v", err)
	}
}

func TestTemplateLocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "otel-v0.150.0.json"),
		`{"collectorVersion":"v0.150.0","components":{}}`)

	store := schema.Store{Locations: []string{filepath.Join(dir, "otel-{{.Version}}.json")}}

	cat, err := store.Load(t.Context(), "v0.150.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.CollectorVersion != "v0.150.0" {
		t.Errorf("unexpected schema: %+v", cat)
	}
}

func TestRemoteLocation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0.140.0.json" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte(`{"collectorVersion":"v0.140.0","components":{}}`))
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL + "/{{.Version}}.json"}, AllowInsecure: true}

	_, err := store.Load(t.Context(), "v0.140.0")
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Load(t.Context(), "v0.130.0")
	if err == nil {
		t.Error("a 404 should not resolve")
	}
}

// The distributions the registry helper below publishes.
// repoSchemas is the committed schema fixture, in the same layout the
// published registry serves. Tests read it instead of the network.
const repoSchemas = "../../testdata/schemas"

const (
	distCore    = "core"
	distContrib = "contrib"

	// latestVersion is the release the registry helper publishes.
	latestVersion = "v0.157.0"
)

// registry writes a registry root: an index plus one schema per distribution.
// "core" carries otlp alone, so filelog is the component that distinguishes the
// distributions from each other.
func registry(t *testing.T, root string) {
	t.Helper()

	const (
		otlp = `"otlp":{"type":"otlp","signals":["logs"]}`
		both = otlp + `,"filelog":{"type":"filelog","signals":["logs"]}`
	)

	write(t, filepath.Join(root, schema.IndexFile),
		`{"distributions":{"core":["v0.157.0"],"contrib":["v0.157.0"]}}`)

	for dist, comps := range map[string]string{
		distCore:    otlp,
		distContrib: both,
	} {
		err := os.MkdirAll(filepath.Join(root, dist), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		write(t, filepath.Join(root, dist, "v0.157.0.json"),
			`{"collectorVersion":"v0.157.0","distribution":"`+dist+`",`+
				`"components":{"receiver":{`+comps+`}}}`)
	}
}

// TestRegistryDirectorySelectsTheDistribution pins the point of the split: the
// same root serves a different component set per distribution.
func TestRegistryDirectorySelectsTheDistribution(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)

	for dist, want := range map[string]int{"": 2, distCore: 1, distContrib: 2} {
		store := schema.Store{Locations: []string{root}, Distribution: dist}

		cat, err := store.Load(t.Context(), "v0.157.0")
		if err != nil {
			t.Fatalf("distribution %q: %v", dist, err)
		}

		if cat.Count() != want {
			t.Errorf("distribution %q: got %d components, want %d", dist, cat.Count(), want)
		}

		if _, ok := cat.Lookup("receiver", "filelog"); ok && dist == distCore {
			t.Error("filelog must not be in the core distribution")
		}
	}
}

// TestRegistryDirectoryEnumeratesFromTheIndex pins that a registry root is
// listed from its index rather than by globbing, which is what lets a remote
// root be listed at all.
func TestRegistryDirectoryEnumeratesFromTheIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)

	store := schema.Store{Locations: []string{root}}
	if got := store.Versions(t.Context()); len(got) != 1 || got[0] != latestVersion {
		t.Errorf("Versions() = %v, want [%s]", got, latestVersion)
	}
}

func TestRemoteRegistryRoot(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + schema.IndexFile:
			_, _ = w.Write([]byte(`{"distributions":{"contrib":["v0.157.0","v0.150.0"],"core":["v0.157.0"]}}`))
		case "/core/v0.157.0.yaml":
			_, _ = w.Write([]byte("collectorVersion: v0.157.0\ndistribution: core\ncomponents: {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore, AllowInsecure: true}

	cat, err := store.Load(t.Context(), "v0.157.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Distribution != distCore {
		t.Errorf("unexpected schema: %+v", cat)
	}

	if got := store.Versions(t.Context()); len(got) != 1 || got[0] != "v0.157.0" {
		t.Errorf("Versions() = %v, want [v0.157.0] from the index", got)
	}
}

// TestIndexVersionsArePerDistribution pins that a distribution is never told
// about a release it has no schema for. Coverage really does differ: upstream
// had no otlp distribution before v0.120.0, and a version listed but not
// servable poisons "latest" and the nearest-version fallback too.
func TestIndexVersionsArePerDistribution(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)

	write(t, filepath.Join(root, schema.IndexFile),
		`{"distributions":{"contrib":["v0.157.0","v0.150.0"],"core":["v0.157.0"]}}`)

	store := schema.Store{Locations: []string{root}, Distribution: distCore}
	if got := store.Versions(t.Context()); len(got) != 1 || got[0] != latestVersion {
		t.Errorf("Versions() = %v, want only the release core has", got)
	}

	if got := (schema.Store{Locations: []string{root}}).Versions(t.Context()); len(got) != 2 {
		t.Errorf("the default distribution should see both releases, got %v", got)
	}
}

// TestARegistryRefusesADistributionItLacks pins that a location never answers
// with a distribution other than the one asked for. A store searches locations
// in order, so widening here would let a contrib-only component pass a core
// check via a fallback location.
func TestARegistryRefusesADistributionItLacks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry(t, root)

	write(t, filepath.Join(root, schema.IndexFile),
		`{"distributions":{"contrib":["v0.157.0"]}}`)

	_, err := (schema.Store{Locations: []string{root}, Distribution: "k8s"}).Load(t.Context(), latestVersion)
	if err == nil {
		t.Fatal("k8s is not in this registry, so it must not resolve")
	}

	_, err = (schema.Store{Locations: []string{root}}).Load(t.Context(), latestVersion)
	if err != nil {
		t.Errorf("the default distribution should still resolve: %v", err)
	}
}

// TestDefaultExpandsToTheOfficialRegistry pins that "default" is an alias for a
// URL rather than a location kind of its own, so it composes with the rest.
func TestDefaultExpandsToTheOfficialRegistry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "v0.157.0.json"),
		`{"collectorVersion":"v0.157.0","components":{}}`)

	// The local flat location answers first, so nothing reaches the network.
	store := schema.Store{Locations: []string{dir, schema.Default}}

	_, err := store.Load(t.Context(), latestVersion)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(schema.DefaultRegistry, "https://") {
		t.Errorf("the default registry should be a URL, got %q", schema.DefaultRegistry)
	}
}

// TestRemoteFileLocation pins that a URL naming one schema outright is read as
// that file, not walked as a registry root.
func TestRemoteFileLocation(t *testing.T) {
	t.Parallel()

	var asked []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)

		if r.URL.Path != "/schemas/v0.157.0.yaml" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte("collectorVersion: v0.157.0\ncomponents: {}\n"))
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL + "/schemas/v0.157.0.yaml"}, AllowInsecure: true}

	_, err := store.Load(t.Context(), "v0.157.0")
	if err != nil {
		t.Fatalf("%v (requested %v)", err, asked)
	}

	if len(asked) != 1 {
		t.Errorf("want one request for the named file, got %v", asked)
	}
}

func TestDistributionPlaceholderInATemplate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Join(dir, distCore), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(dir, distCore, "v0.150.0.json"),
		`{"collectorVersion":"v0.150.0","distribution":"core","components":{}}`)

	store := schema.Store{
		Locations:    []string{filepath.Join(dir, "{{.Distribution}}", "{{.Version}}.json")},
		Distribution: distCore,
	}

	cat, err := store.Load(t.Context(), "v0.150.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Distribution != distCore {
		t.Errorf("unexpected schema: %+v", cat)
	}
}

// TestStoreFsReadsAnInMemoryRegistry pins that Fs governs every local read a
// store does: the index it enumerates from and the schema it loads. Nothing
// here is written to disk.
func TestStoreFsReadsAnInMemoryRegistry(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	root := filepath.FromSlash("/registry")

	memWrite(t, fsys, filepath.Join(root, schema.IndexFile),
		`{"distributions":{"core":["v0.150.0"]}}`)
	memWrite(t, fsys, filepath.Join(root, distCore, "v0.150.0.json"),
		`{"collectorVersion":"v0.150.0","distribution":"core","components":{}}`)

	store := schema.Store{Locations: []string{root}, Distribution: distCore, Fs: fsys}

	if got := store.Versions(t.Context()); !slices.Equal(got, []string{"v0.150.0"}) {
		t.Errorf("Versions() = %v, want the in-memory index", got)
	}

	cat, err := store.Load(t.Context(), schema.Latest)
	if err != nil {
		t.Fatal(err)
	}

	if cat.CollectorVersion != "v0.150.0" {
		t.Errorf("unexpected schema: %+v", cat)
	}
}

// TestStoreFsIsNotTheRealFilesystem pins the other half: once an Fs is given,
// a location that exists on disk is no longer reachable.
func TestStoreFsIsNotTheRealFilesystem(t *testing.T) {
	t.Parallel()

	store := schema.Store{Locations: []string{repoSchemas}, Fs: afero.NewMemMapFs()}

	_, err := store.Load(t.Context(), schema.Latest)
	if err == nil {
		t.Error("want an error: the committed schemas are not on the given Fs")
	}
}

func memWrite(t *testing.T, fsys afero.Fs, path, content string) {
	t.Helper()

	err := afero.WriteFile(fsys, path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAliasesAreMarkedDeprecated(t *testing.T) {
	t.Parallel()

	store := schema.Store{Locations: []string{repoSchemas}}

	cat, err := store.Load(t.Context(), schema.Latest)
	if err != nil {
		t.Fatal(err)
	}

	for kind, byType := range cat.Components {
		for typ, comp := range byType {
			if comp.AliasOf == "" {
				continue
			}

			if comp.Deprecated == "" {
				t.Errorf("%s %q is an alias of %q but is not marked deprecated", kind, typ, comp.AliasOf)
			}

			if _, ok := cat.Lookup(kind, comp.AliasOf); !ok {
				t.Errorf("%s %q points at missing canonical type %q", kind, typ, comp.AliasOf)
			}
		}
	}
}

// TestAPlainHTTPLocationIsRefused pins the default: a schema decides which
// components exist, so it may not arrive over a transport anyone on the path
// can rewrite.
func TestAPlainHTTPLocationIsRefused(t *testing.T) {
	t.Parallel()

	var asked int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++

		_, _ = w.Write([]byte(`{"collectorVersion":"v0.157.0","components":{}}`))
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL + "/{{.Version}}.json"}}

	_, err := store.Load(t.Context(), "v0.157.0")
	require.Error(t, err, "an http:// location should be refused")
	assert.Contains(t, err.Error(), "http", "the error should say what it refused")
	assert.Zero(t, asked, "a refused location should not be fetched")
	require.Error(t, store.Validate(), "Validate should refuse it while a command is still reading flags")

	// The opt-in is what a registry on localhost is served under.
	allowed := store
	allowed.AllowInsecure = true

	require.NoError(t, allowed.Validate(), "the opt-in should permit http")

	_, err = allowed.Load(t.Context(), "v0.157.0")
	require.NoError(t, err)
}

// TestARemoteSchemaIsCappedInSize pins that a location cannot decide how much
// memory the linter spends. The cap is reported as itself, not as a parse
// failure part-way through a truncated document.
func TestARemoteSchemaIsCappedInSize(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "collectorVersion: v0.157.0\ncomponents: {}\n# ")

		// More than the store will accept, streamed rather than allocated.
		_, _ = io.CopyN(w, filler{}, 64<<20)
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL + "/{{.Version}}.yaml"}, AllowInsecure: true}

	_, err := store.Load(t.Context(), "v0.157.0")
	require.Error(t, err, "a body over the limit should not be decoded")
	assert.Contains(t, err.Error(), "larger than",
		"the error should name the limit rather than the parse it prevented")
}

// filler is an endless body, standing in for a registry that streams.
type filler struct{}

func (filler) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}

	return len(p), nil
}

// TestFetchStopsWhenTheContextIsCancelled pins that an interrupt reaches a
// request that is already in flight, rather than waiting out the timeout.
func TestFetchStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL + "/{{.Version}}.json"}, AllowInsecure: true}

	_, err := store.Load(ctx, "v0.157.0")
	require.ErrorIs(t, err, context.Canceled, "a cancelled run should end the fetch it started")
}

func write(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRemoteRegistryFetchesTheExtensionTheIndexNames pins the request the
// index saves. A registry publishing JSON used to cost three requests and two
// 404s for one schema, against a rate limit that counts every one of them.
func TestRemoteRegistryFetchesTheExtensionTheIndexNames(t *testing.T) {
	t.Parallel()

	asked, srv := registryServer(t,
		`{"distributions":{"core":["v0.157.0"]},"extensions":{"core":".json"}}`)
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore, AllowInsecure: true}

	_, err := store.Load(t.Context(), latestVersion)
	require.NoError(t, err)

	assert.Equal(t, []string{"/" + schema.IndexFile, "/core/v0.157.0.json"}, *asked,
		"the named extension should be fetched outright")
}

// TestRemoteRegistryProbesAnIndexThatNamesNothing pins the fallback: an index
// written before the field existed, or one whose releases do not agree on a
// form, still resolves -- by trying each extension, the way it always did.
func TestRemoteRegistryProbesAnIndexThatNamesNothing(t *testing.T) {
	t.Parallel()

	// Published as JSON alone, so the probe has to walk past two misses.
	asked, srv := registryServer(t, `{"distributions":{"core":["v0.157.0"]}}`, ".json")
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore, AllowInsecure: true}

	_, err := store.Load(t.Context(), latestVersion)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"/" + schema.IndexFile, "/core/v0.157.0.yaml", "/core/v0.157.0.yml", "/core/v0.157.0.json",
	}, *asked, "every extension should be probed, in preference order")
}

// TestRemoteRegistryRefusesAnExtensionItDoesNotRead pins that the index cannot
// aim a fetch wherever it likes. The value becomes part of a URL and the index
// comes from the registry, so anything outside the set this package reads is
// ignored rather than requested.
func TestRemoteRegistryRefusesAnExtensionItDoesNotRead(t *testing.T) {
	t.Parallel()

	asked, srv := registryServer(t,
		`{"distributions":{"core":["v0.157.0"]},"extensions":{"core":"/../../etc/passwd"}}`)
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore, AllowInsecure: true}

	_, err := store.Load(t.Context(), latestVersion)
	require.NoError(t, err)

	for _, path := range *asked {
		assert.NotContains(t, path, "passwd", "the index should not decide what is fetched")
	}
}

// TestRemoteRegistryReadsItsIndexOnce pins that resolving several versions
// does not re-fetch the file that says which versions there are. Against a
// rate limit that counts requests, an index fetched per lookup would spend
// more than the extension it names saves.
func TestRemoteRegistryReadsItsIndexOnce(t *testing.T) {
	t.Parallel()

	asked, srv := registryServer(t,
		`{"distributions":{"core":["v0.157.0"]},"extensions":{"core":".json"}}`)
	defer srv.Close()

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore, AllowInsecure: true}

	for range 3 {
		_, err := store.Load(t.Context(), schema.Latest)
		require.NoError(t, err)
	}

	indexes := 0

	for _, path := range *asked {
		if path == "/"+schema.IndexFile {
			indexes++
		}
	}

	assert.Equal(t, 1, indexes, "the index should be read once per registry, not once per lookup")
}

// registryServer serves a registry holding one core schema under the given
// index, published in the named forms (every form when none are named), and
// records the paths it was asked for.
func registryServer(t *testing.T, index string, served ...string) (*[]string, *httptest.Server) {
	t.Helper()

	if len(served) == 0 {
		served = schema.Extensions()
	}

	var (
		lock  sync.Mutex
		asked []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()

		asked = append(asked, r.URL.Path)

		lock.Unlock()

		if r.URL.Path == "/"+schema.IndexFile {
			_, _ = w.Write([]byte(index))

			return
		}

		for _, ext := range served {
			if r.URL.Path != "/core/"+latestVersion+ext {
				continue
			}

			if ext == ".json" {
				_, _ = w.Write([]byte(`{"collectorVersion":"v0.157.0","distribution":"core","components":{}}`))
			} else {
				_, _ = w.Write([]byte("collectorVersion: v0.157.0\ndistribution: core\ncomponents: {}\n"))
			}

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	return &asked, srv
}

// TestARegistryIsReadOnceAcrossRuns pins what the cache is for: the schema for
// a release will never change, so a second run reads it from disk. Against a
// registry that throttles, re-downloading the same immutable file on every
// invocation is the worst pattern there is.
func TestARegistryIsReadOnceAcrossRuns(t *testing.T) {
	t.Parallel()

	asked, srv := registryServer(t, `{"distributions":{"core":["v0.157.0"]}}`)
	defer srv.Close()

	// One cache directory, two stores: the second is the next run.
	dir := t.TempDir()

	for range 2 {
		store := schema.Store{
			Locations:     []string{srv.URL},
			Distribution:  distCore,
			AllowInsecure: true,
			CacheDir:      dir,
		}

		_, err := store.Load(t.Context(), latestVersion)
		require.NoError(t, err)
	}

	schemas := 0

	for _, path := range *asked {
		if strings.HasPrefix(path, "/core/") {
			schemas++
		}
	}

	assert.Equal(t, 1, schemas, "the second run should have read the schema from the cache")
}
