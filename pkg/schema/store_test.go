package schema_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

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

	versions := store.Versions()
	if len(versions) == 0 {
		t.Fatal("the registry lists no schemas")
	}

	for i := 1; i < len(versions); i++ {
		if schema.Compare(versions[i-1], versions[i]) <= 0 {
			t.Fatalf("versions are not sorted newest first: %v", versions)
		}
	}

	latest, err := store.Load(schema.Latest)
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

	_, err := store.Load("v0.115.0")
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

	cat, err := store.Load("v0.157.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Count() != 1 {
		t.Errorf("want the local schema, got %d components", cat.Count())
	}

	// Versions not present locally still fall through to the next location.
	_, err = store.Load("v0.110.0")
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

	cat, err := store.Load("v0.150.0")
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

	store := schema.Store{Locations: []string{srv.URL + "/{{.Version}}.json"}}

	_, err := store.Load("v0.140.0")
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Load("v0.130.0")
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

		cat, err := store.Load("v0.157.0")
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
	if got := store.Versions(); len(got) != 1 || got[0] != latestVersion {
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

	store := schema.Store{Locations: []string{srv.URL}, Distribution: distCore}

	cat, err := store.Load("v0.157.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Distribution != distCore {
		t.Errorf("unexpected schema: %+v", cat)
	}

	if got := store.Versions(); len(got) != 1 || got[0] != "v0.157.0" {
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
	if got := store.Versions(); len(got) != 1 || got[0] != latestVersion {
		t.Errorf("Versions() = %v, want only the release core has", got)
	}

	if got := (schema.Store{Locations: []string{root}}).Versions(); len(got) != 2 {
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

	_, err := (schema.Store{Locations: []string{root}, Distribution: "k8s"}).Load(latestVersion)
	if err == nil {
		t.Fatal("k8s is not in this registry, so it must not resolve")
	}

	_, err = (schema.Store{Locations: []string{root}}).Load(latestVersion)
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

	_, err := store.Load(latestVersion)
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

	store := schema.Store{Locations: []string{srv.URL + "/schemas/v0.157.0.yaml"}}

	_, err := store.Load("v0.157.0")
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

	cat, err := store.Load("v0.150.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Distribution != distCore {
		t.Errorf("unexpected schema: %+v", cat)
	}
}

func TestAliasesAreMarkedDeprecated(t *testing.T) {
	t.Parallel()

	store := schema.Store{Locations: []string{repoSchemas}}

	cat, err := store.Load(schema.Latest)
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

func write(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
