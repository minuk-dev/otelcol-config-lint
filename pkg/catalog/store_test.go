package catalog_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
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
		got := catalog.Compare(tt.a, tt.b)
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
		if got := catalog.Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmbeddedCatalogsLoad(t *testing.T) {
	t.Parallel()

	var store catalog.Store

	versions := store.Versions()
	if len(versions) == 0 {
		t.Fatal("no catalogs are embedded")
	}

	for i := 1; i < len(versions); i++ {
		if catalog.Compare(versions[i-1], versions[i]) <= 0 {
			t.Fatalf("versions are not sorted newest first: %v", versions)
		}
	}

	latest, err := store.Load(catalog.Latest)
	if err != nil {
		t.Fatal(err)
	}

	if latest.CollectorVersion != versions[0] {
		t.Errorf("latest resolved to %q, want %q", latest.CollectorVersion, versions[0])
	}

	if latest.Count() < 100 {
		t.Errorf("catalog looks too small: %d components", latest.Count())
	}

	if _, ok := latest.Lookup(config.KindReceiver, "otlp"); !ok {
		t.Error("the otlp receiver should exist in every release")
	}
}

func TestUnknownVersionSuggestsTheNearestOlder(t *testing.T) {
	t.Parallel()

	var store catalog.Store

	_, err := store.Load("v0.115.0")
	if err == nil {
		t.Fatal("want an error for a version with no catalog")
	}

	var unknown *catalog.UnknownVersionError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *catalog.UnknownVersionError, got %T", err)
	}

	near, found := unknown.Nearest()
	if !found {
		t.Fatal("want a nearest version")
	}

	if catalog.Compare(near, "v0.115.0") > 0 {
		t.Errorf("nearest %q is newer than the request", near)
	}
}

func TestDirectoryLocationWinsOverEmbedded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "v0.157.0.json"),
		`{"collectorVersion":"v0.157.0","components":{"receiver":{"custom":{"type":"custom","signals":["logs"]}}}}`)

	store := catalog.Store{Locations: []string{dir, catalog.Default}}

	cat, err := store.Load("v0.157.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Count() != 1 {
		t.Errorf("want the local catalog, got %d components", cat.Count())
	}

	// Versions not present locally still fall through to the built-ins.
	_, err = store.Load("v0.110.0")
	if err != nil {
		t.Errorf("fallthrough to the embedded catalogs failed: %v", err)
	}
}

func TestTemplateLocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "otel-v0.150.0.json"),
		`{"collectorVersion":"v0.150.0","components":{}}`)

	store := catalog.Store{Locations: []string{filepath.Join(dir, "otel-{{.Version}}.json")}}

	cat, err := store.Load("v0.150.0")
	if err != nil {
		t.Fatal(err)
	}

	if cat.CollectorVersion != "v0.150.0" {
		t.Errorf("unexpected catalog: %+v", cat)
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

	store := catalog.Store{Locations: []string{srv.URL + "/{{.Version}}.json"}}

	_, err := store.Load("v0.140.0")
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Load("v0.130.0")
	if err == nil {
		t.Error("a 404 should not resolve")
	}
}

func TestAliasesAreMarkedDeprecated(t *testing.T) {
	t.Parallel()

	var store catalog.Store

	cat, err := store.Load(catalog.Latest)
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
