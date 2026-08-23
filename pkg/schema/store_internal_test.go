package schema

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
