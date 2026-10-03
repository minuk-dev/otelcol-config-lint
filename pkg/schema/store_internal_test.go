package schema

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSchemaRedirectTransportPolicy(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name                                string
		toHTTP, allowInsecure, customPolicy bool
	}{
		{name: "refuse HTTP", toHTTP: true},
		{name: "refuse HTTP with caller policy", toHTTP: true, customPolicy: true},
		{name: "allow HTTP opt-in", toHTTP: true, allowInsecure: true},
		{name: "allow HTTP opt-in with caller policy", toHTTP: true, allowInsecure: true, customPolicy: true},
		{name: "allow HTTPS"},
		{name: "allow HTTPS with caller policy", customPolicy: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var httpCalls, policyCalls atomic.Int64

			insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpCalls.Add(1)

				_, _ = w.Write([]byte("components: {}"))
			}))
			defer insecure.Close()

			secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final.yaml" {
					_, _ = w.Write([]byte("components: {}"))

					return
				}

				target := "/final.yaml"
				if tt.toHTTP {
					target = insecure.URL + target
				}

				http.Redirect(w, r, target, http.StatusFound)
			}))
			defer secure.Close()

			client := secure.Client()
			if tt.customPolicy {
				client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
					policyCalls.Add(1)

					return nil
				}
			}

			store := Store{HTTPClient: client, AllowInsecure: tt.allowInsecure, NoCache: true}

			_, err := store.fetch(t.Context(), secure.URL+"/v0.157.0.yaml")
			if tt.toHTTP && !tt.allowInsecure {
				require.ErrorIs(t, err, errInsecureLocation)
				assert.Zero(t, httpCalls.Load(), "refused destination must never be contacted")
				assert.Zero(t, policyCalls.Load())
			} else {
				require.NoError(t, err)

				if tt.toHTTP {
					assert.EqualValues(t, 1, httpCalls.Load())
				}

				if tt.customPolicy {
					assert.EqualValues(t, 1, policyCalls.Load())
				}
			}

			if tt.customPolicy {
				next, err := http.NewRequestWithContext(t.Context(), http.MethodGet, insecure.URL, http.NoBody)
				require.NoError(t, err)
				require.NoError(t, client.CheckRedirect(next, nil), "the caller's policy must remain unchanged")
			} else {
				assert.Nil(t, client.CheckRedirect)
			}
		})
	}
}

func TestSchemaRedirectPreservesCallerRejection(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final.yaml", http.StatusFound)
	}))
	defer srv.Close()

	client := srv.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	store := Store{HTTPClient: client, AllowInsecure: true, NoCache: true}
	_, err := store.fetch(t.Context(), srv.URL+"/v0.157.0.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "302")
}

func TestSchemaRedirectKeepsDefaultLimit(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/loop.yaml", http.StatusFound)
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, NoCache: true}
	_, err := store.fetch(t.Context(), srv.URL+"/loop.yaml")
	require.ErrorIs(t, err, errTooManyRedirects)
	assert.EqualValues(t, 10, calls.Load())
}
