package schema

import (
	"net/http"
	"testing"
)

// TestStoresShareOneDefaultClient pins that a store without a client of its own
// does not build one per call. A client owns a connection pool, so a fresh one
// per request is a fresh connection per request, and building the version index
// fetches one schema per release a registry carries. It is written inside the
// package because that client is unexported, which is what lets it be shared.
func TestStoresShareOneDefaultClient(t *testing.T) {
	t.Parallel()

	first, second := Store{}, Store{Distribution: "core"}

	if first.client() != second.client() {
		t.Error("stores with no client of their own should share the default one")
	}

	if first.client().Timeout != defaultFetchTimeout {
		t.Errorf("the default client should carry the fetch timeout, got %v", first.client().Timeout)
	}

	own := Store{HTTPClient: &http.Client{}}
	if own.client() == defaultClient() {
		t.Error("a store with a client of its own should fetch with it")
	}
}
