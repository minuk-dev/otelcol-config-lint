package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"
)

// Extensions returns the schema file suffixes, in preference order. The
// readable form comes first, so a location carrying both serves the YAML.
func Extensions() []string { return []string{".yaml", ".yml", ".json"} }

// extensions is the unexported spelling used throughout this package.
func extensions() []string { return Extensions() }

// Latest selects the newest schema available in a store.
const Latest = "latest"

// Default names the official schema registry. It can be listed among a store's
// locations to control where the published schemas are consulted.
const Default = "default"

// DefaultRegistry is where Default points: the schemas published at
// github.com/minuk-dev/otelcol-config-schemas.
//
// They live in a repository of their own because a registry grows without
// bound -- one file per collector release per distribution -- while a run only
// ever reads one of them. Keeping them beside the linter would charge every
// clone for schemas it will never open.
//
// It tracks main rather than a release tag on purpose. A new collector release
// only needs a schema commit to become lintable, with no linter release; the
// cost is that a schema correction changes what an older binary reports.
const DefaultRegistry = "https://raw.githubusercontent.com/minuk-dev/otelcol-config-schemas/main"

// VersionPlaceholder is substituted with the collector version in a location
// template, e.g. "https://example.com/otel/{{.Version}}.json".
const VersionPlaceholder = "{{.Version}}"

// DistributionPlaceholder is substituted with the selected distribution in a
// location template, e.g. "https://example.com/otel/{{.Distribution}}/{{.Version}}.json".
const DistributionPlaceholder = "{{.Distribution}}"

// defaultFetchTimeout bounds a remote schema download when the caller has not
// supplied its own client.
const defaultFetchTimeout = 30 * time.Second

// maxRemoteBytes bounds what one remote schema or index may deliver, and
// maxRemoteMiB is the same number as the error reports it. A location is
// whatever the caller named, and a decoder materialises everything it is
// handed, so the ceiling is the linter's rather than the endpoint's. The
// largest published schema is a few megabytes; this leaves room for a registry
// an order of magnitude richer and still bounds what a hostile or merely
// broken one can claim.
const (
	maxRemoteMiB   = 32
	maxRemoteBytes = maxRemoteMiB << 20
)

// insecureScheme is the transport a schema location may not use unless the
// caller opted in.
const insecureScheme = "http://"

// secureScheme is the transport a remote schema location is expected to use.
const secureScheme = "https://"

// Store resolves collector versions to schemas. The zero value reads the
// official registry over HTTPS.
//
// A location is one of:
//
//   - "default", the official registry;
//   - a directory, searched for "<version>.json";
//   - a template containing {{.Version}}, resolved as a local path or, when it
//     starts with https://, fetched over the network. A plain http:// location
//     is refused unless AllowInsecure says otherwise.
//
// Locations are consulted in order, so a project can keep its own schema
// directory ahead of the built-ins. When no location is given, only the
// built-ins are used.
type Store struct {
	Locations []string
	// Distribution selects which collector binary to describe. An empty value
	// means DefaultDistribution.
	Distribution string
	// HTTPClient fetches remote locations. A nil client uses a shared default
	// with a 30 second timeout.
	HTTPClient *http.Client
	// AllowInsecure permits a plain http:// location. It is off by default: a
	// schema decides which components exist and which settings are valid, so
	// anyone able to rewrite one in flight decides what the rules report. Turn
	// it on for a registry served over loopback, where there is no flight.
	AllowInsecure bool
	// Fs is the filesystem local locations are read from. A nil Fs means the
	// real one. Remote locations go over HTTPClient and ignore it.
	Fs afero.Fs

	// retryDelay shortens the wait between attempts. It is unexported because
	// only this package's own tests have a reason to set it.
	retryDelay time.Duration
}

// Versions lists every schema the store can serve, newest first. Templated and
// remote locations cannot be enumerated, so they never appear here even though
// Load can still resolve them.
func (s Store) Versions(ctx context.Context) []string {
	seen := map[string]bool{}

	var out []string

	for _, loc := range s.locations() {
		for _, v := range s.versionsAt(ctx, loc) {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) > 0 })

	return out
}

// Distributions lists the distributions the store can serve, sorted. Only a
// registry says what it carries, so other location kinds report none.
func (s Store) Distributions(ctx context.Context) []string {
	for _, loc := range s.locations() {
		idx := s.indexAt(ctx, loc)
		if idx != nil {
			return idx.Names()
		}
	}

	return nil
}

// WithDistribution returns a copy of the store serving another distribution.
func (s Store) WithDistribution(distribution string) Store {
	s.Distribution = distribution

	return s
}

// Load returns the schema for a collector version. The special value "latest"
// (or an empty string) selects the newest schema the store can enumerate.
func (s Store) Load(ctx context.Context, version string) (*Schema, error) {
	err := s.Validate()
	if err != nil {
		return nil, err
	}

	if version == "" || version == Latest {
		versions := s.Versions(ctx)
		if len(versions) == 0 {
			return nil, fmt.Errorf("%w in %s", errNoSchemas, strings.Join(s.locations(), ", "))
		}

		version = versions[0]
	}

	version = Normalize(version)

	var tried []string

	for _, loc := range s.locations() {
		c, err := s.loadFrom(ctx, loc, version)
		switch {
		case err == nil:
			return c, nil
		case errors.Is(err, os.ErrNotExist), errors.Is(err, errNotFound):
			tried = append(tried, s.resolve(loc, version))
		default:
			return nil, fmt.Errorf("%s: %w", s.resolve(loc, version), err)
		}
	}

	return nil, &UnknownVersionError{Version: version, Available: s.Versions(ctx), Tried: tried}
}

// Validate reports the first location the store will refuse, so a command can
// say so while it is still reading its flags rather than at the first fetch.
// A location that is merely unreachable is not one of them: whether a registry
// answers is only known by asking it.
func (s Store) Validate() error {
	if s.AllowInsecure {
		return nil
	}

	for _, loc := range s.locations() {
		if isInsecure(loc) {
			return fmt.Errorf("%w: %s", errInsecureLocation, loc)
		}
	}

	return nil
}

// fs returns the filesystem to read local locations from, which is the real one
// unless the caller named another.
func (s Store) fs() afero.Fs {
	if s.Fs == nil {
		return afero.NewOsFs()
	}

	return s.Fs
}

// distribution returns the distribution to serve.
func (s Store) distribution() string {
	if s.Distribution == "" {
		return DefaultDistribution
	}

	return s.Distribution
}

// indexAt reads a location's index, or nil when it has none.
func (s Store) indexAt(ctx context.Context, loc string) *Index {
	switch kindOf(loc) {
	case locRemote:
		idx, err := s.fetchIndex(ctx, loc)
		if err != nil {
			return nil
		}

		return idx
	case locDir:
		idx, err := readIndexFile(s.fs(), filepath.Join(loc, IndexFile))
		if err != nil {
			return nil
		}

		return idx
	default:
		return nil
	}
}

// versionsAt lists the versions one location can serve. A template names a
// single file and cannot be listed, so it contributes nothing.
func (s Store) versionsAt(ctx context.Context, loc string) []string {
	switch kindOf(loc) {
	case locRemote:
		idx, err := s.fetchIndex(ctx, loc)
		if err != nil {
			return nil
		}

		return idx.Versions(s.distribution())
	case locDir:
		idx, err := readIndexFile(s.fs(), filepath.Join(loc, IndexFile))
		if err == nil {
			return idx.Versions(s.distribution())
		}

		return flatVersions(s.fs(), loc)
	default:
		return nil
	}
}

// flatVersions lists "<dir>/<version>.<ext>", the layout used before schemas
// were split by distribution.
func flatVersions(fsys afero.Fs, dir string) []string {
	var out []string

	for _, ext := range extensions() {
		names, _ := afero.Glob(fsys, filepath.Join(dir, "*"+ext))
		for _, n := range names {
			out = append(out, trimExt(filepath.Base(n)))
		}
	}

	return out
}

// trimExt strips a schema file extension, returning "" for anything else.
func trimExt(name string) string {
	for _, ext := range extensions() {
		if base, found := strings.CutSuffix(name, ext); found {
			return base
		}
	}

	return ""
}

// isRegistryDir reports whether a directory is a registry root: one carrying an
// index, and so laid out by distribution.
func (s Store) isRegistryDir(dir string) bool {
	_, err := s.fs().Stat(filepath.Join(dir, IndexFile))

	return err == nil
}

// locations returns the locations to search, defaulting to the official
// registry. "default" is expanded here, so nothing downstream has to know the
// alias exists.
func (s Store) locations() []string {
	if len(s.Locations) == 0 {
		return []string{DefaultRegistry}
	}

	out := make([]string, 0, len(s.Locations))

	for _, loc := range s.Locations {
		if loc == Default {
			loc = DefaultRegistry
		}

		out = append(out, loc)
	}

	return out
}

var (
	// errNotFound signals that a location does not have the requested version.
	errNotFound = errors.New("schema not found")
	// errNoSchemas signals that no location could be enumerated at all.
	errNoSchemas = errors.New("no schemas available")
	// errBadStatus signals a remote location answering with an unusable status.
	errBadStatus = errors.New("unexpected status")
	// errTooLarge signals a remote location serving more than maxRemoteBytes.
	errTooLarge = fmt.Errorf("schema is larger than the %d MiB limit", maxRemoteMiB)
	// errInsecureLocation signals a plain http:// location without the opt-in
	// that permits one.
	errInsecureLocation = errors.New("refusing to fetch a schema over plain http, " +
		"which anyone on the path can rewrite")
)

func (s Store) loadFrom(ctx context.Context, loc, version string) (*Schema, error) {
	switch kindOf(loc) {
	case locRemote:
		return s.loadFromRegistry(loc, version, func(target string) (*Schema, error) {
			return s.fetch(ctx, target)
		})
	case locTemplate, locFile:
		return s.loadOne(ctx, s.resolve(loc, version))
	default:
		// A directory holding an index is a registry root, laid out by
		// distribution. Without one it is the flat legacy layout.
		if s.isRegistryDir(loc) {
			return s.loadFromRegistry(loc, version, s.readLocal)
		}

		return s.loadFlat(loc, version)
	}
}

// loadOne reads a location that names a single schema, local or remote.
func (s Store) loadOne(ctx context.Context, target string) (*Schema, error) {
	if isRemote(target) {
		return s.fetch(ctx, target)
	}

	return s.readLocal(target)
}

// loadFromRegistry reads "<root>/<distribution>/<version>.<ext>", preferring
// the readable form when a root carries both.
func (s Store) loadFromRegistry(root, version string, read func(string) (*Schema, error)) (*Schema, error) {
	for _, ext := range extensions() {
		c, err := read(join(root, s.distribution(), version+ext))
		if err == nil {
			return c, nil
		}

		if !errors.Is(err, errNotFound) && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	return nil, errNotFound
}

// loadFlat reads "<dir>/<version>.<ext>", the layout used before schemas were
// split by distribution.
func (s Store) loadFlat(dir, version string) (*Schema, error) {
	for _, ext := range extensions() {
		path := filepath.Join(dir, version+ext)

		_, err := s.fs().Stat(path)
		if err == nil {
			return readFile(s.fs(), path)
		}
	}

	return nil, errNotFound
}

// readLocal reads a schema from disk, reporting a missing file as errNotFound
// so a registry root can fall through to the next extension.
func (s Store) readLocal(path string) (*Schema, error) {
	_, err := s.fs().Stat(path)
	if err != nil {
		return nil, errNotFound
	}

	return readFile(s.fs(), path)
}

// fetchIndex reads a remote registry's index.
func (s Store) fetchIndex(ctx context.Context, root string) (*Index, error) {
	body, err := s.get(ctx, join(root, IndexFile))
	if err != nil {
		return nil, err
	}

	return ReadIndex(bytes.NewReader(body))
}

// join appends path segments to a registry root, which is a URL or a local
// directory. Slashes are right for both: filepath.Join would break URLs on
// Windows, and a local path with forward slashes reads fine everywhere.
func join(root string, segments ...string) string {
	return strings.Join(append([]string{strings.TrimSuffix(root, "/")}, segments...), "/")
}

func isRemote(loc string) bool {
	return strings.HasPrefix(loc, insecureScheme) || strings.HasPrefix(loc, secureScheme)
}

// isInsecure reports a location fetched over a transport that anyone on the
// path can rewrite.
func isInsecure(loc string) bool {
	return strings.HasPrefix(loc, insecureScheme)
}

// fetch reads a schema over the network.
func (s Store) fetch(ctx context.Context, url string) (*Schema, error) {
	body, err := s.get(ctx, url)
	if err != nil {
		return nil, err
	}

	return Read(bytes.NewReader(body))
}

// get performs the request behind fetch and fetchIndex, and returns what the
// endpoint served. A location that throttles or fails transiently is asked
// again, up to fetchAttempts times; see retry.go for which statuses earn one
// and how long each wait is.
func (s Store) get(ctx context.Context, url string) ([]byte, error) {
	if !s.AllowInsecure && isInsecure(url) {
		return nil, fmt.Errorf("%w: %s", errInsecureLocation, url)
	}

	for attempt := 0; ; attempt++ {
		body, err := s.attempt(ctx, url)
		if err == nil {
			return body, nil
		}

		var status *statusError

		last := attempt == fetchAttempts-1
		if !errors.As(err, &status) || !status.retryable() || last {
			if attempt > 0 {
				return nil, fmt.Errorf("%w (gave up after %d attempts)", err, attempt+1)
			}

			return nil, err
		}

		err = sleep(ctx, retryDelay(attempt, s.retryBase(), status.after))
		if err != nil {
			return nil, err
		}
	}
}

// attempt makes one request and returns what it served. The body is read here
// rather than handed to a decoder so that maxRemoteBytes is enforced in one
// place, and so that a location serving too much is reported as exactly that
// instead of as a parse failure part-way through a truncated document.
func (s Store) attempt(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}

	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return readBody(url, resp.Body)
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		return nil, &statusError{
			url:    url,
			status: resp.Status,
			code:   resp.StatusCode,
			after:  retryAfter(resp.Header, time.Now()),
		}
	}
}

// retryBase is how long the wait after a first failed attempt is. Only a test
// sets one of its own, so that a retry it is exercising costs milliseconds
// rather than seconds.
func (s Store) retryBase() time.Duration {
	if s.retryDelay > 0 {
		return s.retryDelay
	}

	return retryBaseDelay
}

// readBody reads a response under maxRemoteBytes. One byte past the limit is
// asked for, so a body sitting exactly on it is still served and anything
// longer is refused rather than silently cut short.
func readBody(url string, body io.Reader) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(body, maxRemoteBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}

	if len(buf) > maxRemoteBytes {
		return nil, fmt.Errorf("read %s: %w", url, errTooLarge)
	}

	return buf, nil
}

// client is what the store fetches with.
func (s Store) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}

	return defaultClient()
}

// defaultClient is what a store with no client of its own fetches with. It is
// built once and shared: a client owns a connection pool, so one per request
// means a connection per request, and building the version index walks every
// release a registry carries.
//
//nolint:gochecknoglobals // one client is the point: a package-level one is what makes the pool shared
var defaultClient = sync.OnceValue(func() *http.Client {
	return &http.Client{Timeout: defaultFetchTimeout}
})

type locKind int

const (
	// locDir is a local directory: a registry root when it carries an index,
	// otherwise the flat layout.
	locDir locKind = iota
	// locTemplate is a path or URL with placeholders, naming one file.
	locTemplate
	// locFile is a path or URL naming one schema file outright.
	locFile
	// locRemote is a remote registry root.
	locRemote
)

func kindOf(loc string) locKind {
	switch {
	case strings.Contains(loc, VersionPlaceholder), strings.Contains(loc, DistributionPlaceholder):
		return locTemplate
	case trimExt(path.Base(loc)) != "":
		return locFile
	case isRemote(loc):
		return locRemote
	default:
		return locDir
	}
}

// resolve turns a location into the concrete path or URL a version is read
// from, which is what error messages name.
func (s Store) resolve(loc, version string) string {
	switch kindOf(loc) {
	case locFile:
		return loc
	case locTemplate:
		out := strings.ReplaceAll(loc, VersionPlaceholder, version)

		return strings.ReplaceAll(out, DistributionPlaceholder, s.distribution())
	case locRemote:
		return join(loc, s.distribution(), version+extensions()[0])
	default:
		if s.isRegistryDir(loc) {
			return filepath.Join(loc, s.distribution(), version+extensions()[0])
		}

		return filepath.Join(loc, version+extensions()[0])
	}
}

// UnknownVersionError reports that no location had the requested version.
type UnknownVersionError struct {
	Version string
	// Available lists the versions the store could enumerate.
	Available []string
	// Tried lists the concrete paths and URLs that were checked.
	Tried []string
}

func (e *UnknownVersionError) Error() string {
	msg := "no schema for collector version " + e.Version

	switch {
	case len(e.Available) > 0:
		msg += " (available: " + strings.Join(e.Available, ", ") + ")"
	case len(e.Tried) > 0:
		// Nothing could be enumerated either, so naming the version alone
		// would not say whether the registry is wrong or simply unreachable.
		msg += " (tried: " + strings.Join(e.Tried, ", ") + ")"
	}

	return msg
}

// Nearest returns the newest available version that is not newer than the
// requested one, which is the closest safe stand-in for a missing schema.
func (e *UnknownVersionError) Nearest() (string, bool) {
	for _, v := range e.Available { // Available is sorted newest first.
		if Compare(v, e.Version) <= 0 {
			return v, true
		}
	}

	return "", false
}

// Normalize puts a version into the "vX.Y.Z" form used for schema file names.
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == Latest {
		return v
	}

	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	return v
}

// Compare orders two collector versions. It returns a negative number when a is
// older than b, zero when they are equal, and a positive number otherwise. A
// pre-release sorts before the release it leads up to, and unparsable segments
// compare as zero so malformed versions sort as oldest.
func Compare(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] - pb[i]
		}
	}

	ra, rb := prerelease(a), prerelease(b)
	switch {
	case ra == rb:
		return 0
	case ra == "":
		return 1
	case rb == "":
		return -1
	default:
		return strings.Compare(ra, rb)
	}
}

// prerelease returns the suffix after the first "-", e.g. "rc.1".
func prerelease(v string) string {
	_, rest, _ := strings.Cut(Normalize(v), "-")

	return rest
}

func parseVersion(v string) [3]int {
	var out [3]int

	v = strings.TrimPrefix(Normalize(v), "v")
	v, _, _ = strings.Cut(v, "-") // drop any pre-release suffix

	parts := strings.SplitN(v, ".", len(out))
	for i := range out {
		if i >= len(parts) {
			break
		}

		n, err := strconv.Atoi(parts[i])
		if err != nil {
			continue // an unparsable segment counts as zero
		}

		out[i] = n
	}

	return out
}
