package schema

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheServesAnImmutableLocationWithoutAsking pins the request the cache
// saves. A published schema describes one release of one distribution, so the
// only thing a second fetch could serve is what is already on disk -- and
// asking anyway, every run, against a registry that throttles, is the pattern
// the cache exists to end.
func TestCacheServesAnImmutableLocationWithoutAsking(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		_, _ = w.Write([]byte("served"))
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, CacheDir: t.TempDir()}

	first, err := store.get(t.Context(), srv.URL, immutable)
	require.NoError(t, err)

	second, err := store.get(t.Context(), srv.URL, immutable)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
	assert.Equal(t, int32(1), requests.Load(), "the second read should have come from the cache")
}

// TestCacheRevalidatesAnIndex pins that what does change under its own URL is
// asked about rather than assumed. The index grows a line per release, so it
// is offered back with its validator: a run that is up to date pays a 304
// instead of the file.
func TestCacheRevalidatesAnIndex(t *testing.T) {
	t.Parallel()

	var offered atomic.Value

	offered.Store("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offered.Store(r.Header.Get("If-None-Match"))

		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("the index"))
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, CacheDir: t.TempDir()}

	_, err := store.get(t.Context(), srv.URL, revalidated)
	require.NoError(t, err)
	assert.Empty(t, offered.Load(), "nothing was cached yet, so nothing should have been offered")

	body, err := store.get(t.Context(), srv.URL, revalidated)
	require.NoError(t, err)
	assert.Equal(t, `"v1"`, offered.Load(), "the cached copy should have been offered for revalidation")
	assert.Equal(t, "the index", string(body), "a 304 should serve what the cache holds")
}

// TestCacheServesTheNewBodyWhenTheValidatorIsStale pins the other half of
// revalidation: an index that has moved on is served, stored, and offered
// under its new validator.
func TestCacheServesTheNewBodyWhenTheValidatorIsStale(t *testing.T) {
	t.Parallel()

	var current atomic.Value

	current.Store(`"v1"`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag, _ := current.Load().(string)
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", tag)
		_, _ = w.Write([]byte("index " + tag))
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, CacheDir: t.TempDir()}

	_, err := store.get(t.Context(), srv.URL, revalidated)
	require.NoError(t, err)

	current.Store(`"v2"`)

	body, err := store.get(t.Context(), srv.URL, revalidated)
	require.NoError(t, err)
	assert.Equal(t, `index "v2"`, string(body), "a moved-on location should serve its new body")

	body, err = store.get(t.Context(), srv.URL, revalidated)
	require.NoError(t, err)
	assert.Equal(t, `index "v2"`, string(body), "the new validator should have replaced the old one")
}

// TestNoCacheAsksEveryTime pins what --no-cache is for: a schema corrected
// under a version already read once is only picked up by a run that does not
// trust what it kept.
func TestNoCacheAsksEveryTime(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		_, _ = w.Write([]byte("served"))
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, CacheDir: t.TempDir(), NoCache: true}

	for range 2 {
		_, err := store.get(t.Context(), srv.URL, immutable)
		require.NoError(t, err)
	}

	assert.Equal(t, int32(2), requests.Load(), "--no-cache should keep nothing and read nothing")
	assert.Nil(t, store.cache(), "--no-cache should leave no cache at all")

	entries, err := os.ReadDir(store.CacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "--no-cache should have written nothing")
}

// TestAnUnwritableCacheIsStillAFetch pins that the cache never decides whether
// a run works. It holds what was served; it is not the answer.
func TestAnUnwritableCacheIsStillAFetch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("served"))
	}))
	defer srv.Close()

	store := Store{
		AllowInsecure: true,
		CacheDir:      t.TempDir(),
		Fs:            afero.NewReadOnlyFs(afero.NewMemMapFs()),
	}

	body, err := store.get(t.Context(), srv.URL, immutable)
	require.NoError(t, err)
	assert.Equal(t, "served", string(body))
}

// TestCacheRootHonoursXDG pins where the cache goes when the caller names
// nowhere. XDG_CACHE_HOME is read on every platform, rather than only where
// os.UserCacheDir happens to read it, so one setting moves the cache wherever
// the run happens.
func TestCacheRootHonoursXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(cacheEnv, dir)

	root, err := cacheRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, cacheDirName), root)
}
