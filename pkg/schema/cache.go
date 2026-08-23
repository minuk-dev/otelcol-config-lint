package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
)

// cacheDirName is what the cache is called inside the user's cache directory.
const cacheDirName = "otelcol-config-lint"

// cacheEnv is the environment variable naming where caches go, which is where
// this one goes when it is set.
const cacheEnv = "XDG_CACHE_HOME"

// etagSuffix names the file holding an entry's validator, beside the body it
// validates. Two files rather than one envelope: what is cached is exactly
// what was served, so a cached schema can be read with any editor and diffed
// against the registry.
const etagSuffix = ".etag"

// The modes the cache creates its directory and files with. Nothing here is
// secret, but nothing else has a reason to write it either.
const (
	cacheDirPerm  = 0o700
	cacheFilePerm = 0o600
)

// freshness says what a cached copy of a location is worth without asking the
// registry about it.
type freshness int

const (
	// immutable marks a location whose content will not change under the same
	// URL: a schema is generated from one release of one distribution, so the
	// only thing a second fetch can serve is the bytes already on disk. A
	// correction to a published schema is the exception, which is what
	// --no-cache is for.
	immutable freshness = iota
	// revalidated marks a location that changes in place -- the index, which
	// grows a line per release. The cached copy is offered to the registry
	// with If-None-Match, so a run that is up to date pays a 304 instead of
	// the file.
	revalidated
)

// diskCache keeps what a registry served, so that the next run does not ask
// for it again. Entries are keyed by URL, and the body and its validator are
// separate files.
//
// It is best-effort throughout: a cache that cannot be read or written is a
// fetch that goes to the network, never an error. Nothing here is the answer
// the caller asked for.
type diskCache struct {
	fs  afero.Fs
	dir string
}

// cache is where this store keeps what it fetched, or nil when it was told not
// to keep anything or there is nowhere to put it.
func (s Store) cache() *diskCache {
	if s.NoCache {
		return nil
	}

	dir := s.CacheDir
	if dir == "" {
		root, err := cacheRoot()
		if err != nil {
			return nil
		}

		dir = root
	}

	return &diskCache{fs: s.fs(), dir: dir}
}

// cacheRoot is where fetched schemas are kept when the caller named nowhere:
// under $XDG_CACHE_HOME when the environment names one, and under the
// platform's own cache directory otherwise. The variable is honoured on every
// platform, rather than only where os.UserCacheDir reads it, so that one
// setting moves the cache wherever the run happens.
func cacheRoot() (string, error) {
	dir := os.Getenv(cacheEnv)
	if dir == "" {
		var err error

		dir, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate the cache directory: %w", err)
		}
	}

	return filepath.Join(dir, cacheDirName), nil
}

// path is where one URL's body is kept. The name is a digest of the URL: a
// location is whatever the caller named, and its path is not one this package
// should be laying out directories from.
func (c *diskCache) path(url string) string {
	sum := sha256.Sum256([]byte(url))

	return filepath.Join(c.dir, hex.EncodeToString(sum[:]))
}

// load returns what was cached for a URL: the body, the validator to offer the
// registry with it, and whether there was anything at all.
func (c *diskCache) load(url string) ([]byte, string, bool) {
	body, err := afero.ReadFile(c.fs, c.path(url))
	if err != nil {
		return nil, "", false
	}

	// A missing validator only means the entry cannot be revalidated; the body
	// is still the body.
	etag, err := afero.ReadFile(c.fs, c.path(url)+etagSuffix)
	if err != nil {
		return body, "", true
	}

	return body, string(etag), true
}

// save records what a registry served. A failure is ignored: the run has the
// bytes it needed, and a cache that cannot be written is only a slower next
// run.
func (c *diskCache) save(url string, body []byte, etag string) {
	err := c.fs.MkdirAll(c.dir, cacheDirPerm)
	if err != nil {
		return
	}

	err = c.write(c.path(url), body)
	if err != nil {
		return
	}

	if etag == "" {
		// Leave no stale validator beside a fresh body: it would be offered
		// for content it no longer describes.
		_ = c.fs.Remove(c.path(url) + etagSuffix)

		return
	}

	_ = c.write(c.path(url)+etagSuffix, []byte(etag))
}

// write puts a file in place in one step, so that a run reading the cache
// while another writes it sees either the old entry or the new one, and a
// write cut short leaves neither.
func (c *diskCache) write(path string, content []byte) error {
	tmp, err := afero.TempFile(c.fs, c.dir, filepath.Base(path)+"-")
	if err != nil {
		return fmt.Errorf("create a cache entry: %w", err)
	}

	_, err = tmp.Write(content)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = c.fs.Chmod(tmp.Name(), cacheFilePerm)
	}

	if err == nil {
		err = c.fs.Rename(tmp.Name(), path)
	}

	if err != nil {
		_ = c.fs.Remove(tmp.Name())

		return fmt.Errorf("write a cache entry: %w", err)
	}

	return nil
}
