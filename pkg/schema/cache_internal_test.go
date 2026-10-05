package schema

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

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

// Separate cache instances share only the filesystem, as separate CLI runs
// do. Pause the newer writer after its first replacement, let an older writer
// finish, then resume: separate body/ETag writes used to leave old/new here.
func TestCacheInterleavedWritesKeepBodyAndValidatorTogether(t *testing.T) {
	t.Parallel()

	var notModified atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"new"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", `"new"`)
		_, _ = w.Write([]byte("new index"))
	}))
	defer srv.Close()

	fsys := afero.NewOsFs()
	dir := t.TempDir()
	// Keep network I/O outside the bubble; only the filesystem interleaving
	// needs deterministic goroutine synchronization.
	synctest.Test(t, func(t *testing.T) {
		paused := &pauseAfterRenameFs{
			Fs: fsys, renamed: false, resume: make(chan struct{}), once: sync.Once{},
		}
		defer close(paused.resume)

		newer := &diskCache{fs: paused, dir: dir}
		older := &diskCache{fs: fsys, dir: dir}

		go newer.save(srv.URL, []byte("new index"), `"new"`)

		synctest.Wait()
		require.True(t, paused.renamed, "the writer must reach the first replacement")
		older.save(srv.URL, []byte("old index"), `"old"`)

		paused.resume <- struct{}{}
		// Test waits for the resumed writer to finish before returning.
	})

	store := Store{AllowInsecure: true, CacheDir: dir, Fs: fsys, HTTPClient: srv.Client()}
	for range 3 {
		body, err := store.get(t.Context(), srv.URL, revalidated)
		require.NoError(t, err)
		assert.Equal(t, "new index", string(body), "a validator must describe the body returned on 304")
	}

	assert.GreaterOrEqual(t, notModified.Load(), int32(2), "exercise repeated 304 revalidations")
}

type pauseAfterRenameFs struct {
	afero.Fs

	renamed bool
	resume  chan struct{}
	once    sync.Once
}

func (fsys *pauseAfterRenameFs) Rename(oldname, newname string) error {
	err := fsys.Fs.Rename(oldname, newname)
	if err == nil {
		fsys.once.Do(func() {
			fsys.renamed = true

			<-fsys.resume
		})
	}

	return err
}

func TestCacheIgnoresLegacyBodyAndValidator(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		keep freshness
	}{
		{name: "immutable", keep: immutable},
		{name: "revalidated", keep: revalidated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Empty(t, r.Header.Get("If-None-Match"), "legacy validators cannot be trusted")
				w.Header().Set("ETag", `"new"`)
				_, _ = w.Write([]byte("new index"))
			}))
			defer srv.Close()

			store := Store{AllowInsecure: true, CacheDir: t.TempDir(), HTTPClient: srv.Client()}
			cache := store.cache()
			legacyPath := strings.TrimSuffix(cache.path(srv.URL), ".json")
			require.NoError(t, afero.WriteFile(cache.fs, legacyPath, []byte("old index"), cacheFilePerm))
			require.NoError(t, afero.WriteFile(cache.fs, legacyPath+".etag", []byte(`"new"`), cacheFilePerm))

			body, err := store.get(t.Context(), srv.URL, tt.keep)
			require.NoError(t, err)
			assert.Equal(t, "new index", string(body))
		})
	}
}

func TestCacheEntryRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		body []byte
		etag string
	}{
		{name: "with validator", body: []byte("index"), etag: `W/"new"`},
		{name: "without validator", body: []byte("no ETag"), etag: ""},
		{name: "empty body", body: []byte{}, etag: `"empty"`},
		{name: "binary body", body: []byte{0, 0xff}, etag: `"binary"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cache := &diskCache{fs: afero.NewMemMapFs(), dir: t.TempDir()}
			cache.save("https://example.com/index", []byte("previous"), `"old"`)
			cache.save("https://example.com/index", tt.body, tt.etag)

			body, etag, ok := cache.load("https://example.com/index")
			require.True(t, ok)
			assert.Equal(t, tt.body, body)
			assert.Equal(t, tt.etag, etag)
		})
	}
}

func TestCacheInvalidEntryIsAMiss(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		content string
	}{
		{name: "truncated", content: `{"body":"aW5kZXg=","etag":`},
		{name: "missing body", content: `{"etag":"new"}`},
		{name: "null body", content: `{"body":null,"etag":"new"}`},
		{name: "invalid base64", content: `{"body":"!","etag":"new"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cache := &diskCache{fs: afero.NewOsFs(), dir: t.TempDir()}
			url := "https://example.com/index"
			require.NoError(t, afero.WriteFile(cache.fs, cache.path(url), []byte(tt.content), cacheFilePerm))

			body, etag, ok := cache.load(url)
			assert.False(t, ok)
			assert.Nil(t, body)
			assert.Empty(t, etag)
		})
	}
}

func TestCacheFailedReplacementKeepsPreviousEntry(t *testing.T) {
	t.Parallel()

	fsys := afero.NewOsFs()
	cache := &diskCache{fs: fsys, dir: t.TempDir()}
	url := "https://example.com/index"
	cache.save(url, []byte("old index"), `"old"`)
	cache.fs = failRenameFs{Fs: fsys}
	cache.save(url, []byte("new index"), `"new"`)

	body, etag, ok := cache.load(url)
	require.True(t, ok)
	assert.Equal(t, "old index", string(body))
	assert.Equal(t, `"old"`, etag)

	entries, err := afero.ReadDir(fsys, cache.dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "failed replacements must remove temporary files")
}

type failRenameFs struct {
	afero.Fs
}

func (fsys failRenameFs) Rename(_, _ string) error {
	return os.ErrPermission
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
