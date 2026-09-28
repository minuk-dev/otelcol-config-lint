package schema

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFailedIndexReadDoesNotPoisonLaterLookup(t *testing.T) {
	t.Setenv(cacheEnv, t.TempDir())

	var calls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte(`{"distributions":{"contrib":["v0.157.0"]}}`))
	}))
	defer srv.Close()

	store := Store{Locations: []string{srv.URL}, AllowInsecure: true}
	if got := store.Versions(t.Context()); len(got) != 0 {
		t.Fatalf("first lookup = %v, want no versions", got)
	}

	if got := store.Versions(t.Context()); len(got) != 1 || got[0] != "v0.157.0" {
		t.Fatalf("second lookup = %v, want recovered registry", got)
	}

	if got := calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

// TestStoresShareOneDefaultClient pins that a store without a client of its own
// does not build one per call. A client owns a connection pool, so a fresh one
// per request is a fresh connection per request, and building the version index
// fetches one schema per release a registry carries. It is written inside the
// package because that client is unexported, which is what lets it be shared.
func TestStoresShareOneDefaultClient(t *testing.T) {
	t.Parallel()

	first, second := Store{}, Store{Distribution: "core"}

	assert.Same(t, first.client(), second.client(), "stores with no client of their own should share one")
	assert.Equal(t, defaultFetchTimeout, first.client().Timeout, "the shared client should carry the fetch timeout")

	own := Store{HTTPClient: &http.Client{}}
	assert.NotSame(t, defaultClient(), own.client(), "a store with a client of its own should fetch with it")
}
