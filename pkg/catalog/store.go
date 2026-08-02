package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minuk-dev/otelcol-config-lint/catalogs"
)

// Extensions returns the catalog file suffixes, in preference order. The
// readable form comes first, so a location carrying both serves the YAML.
func Extensions() []string { return []string{".yaml", ".yml", ".json"} }

// extensions is the unexported spelling used throughout this package.
func extensions() []string { return Extensions() }

// Latest selects the newest catalog available in a store.
const Latest = "latest"

// Default names the catalogs built into the binary. It can be listed among a
// store's locations to control where the built-ins are consulted.
const Default = "default"

// VersionPlaceholder is substituted with the collector version in a location
// template, e.g. "https://example.com/otel/{{.Version}}.json".
const VersionPlaceholder = "{{.Version}}"

// DistributionPlaceholder is substituted with the selected distribution in a
// location template, e.g. "https://example.com/otel/{{.Distribution}}/{{.Version}}.json".
const DistributionPlaceholder = "{{.Distribution}}"

// defaultFetchTimeout bounds a remote catalog download when the caller has not
// supplied its own client.
const defaultFetchTimeout = 30 * time.Second

// Store resolves collector versions to catalogs. The zero value serves the
// catalogs embedded in the binary.
//
// A location is one of:
//
//   - "default", the catalogs embedded in the binary;
//   - a directory, searched for "<version>.json";
//   - a template containing {{.Version}}, resolved as a local path or, when it
//     starts with http:// or https://, fetched over HTTP.
//
// Locations are consulted in order, so a project can keep its own schema
// directory ahead of the built-ins. When no location is given, only the
// built-ins are used.
type Store struct {
	Locations []string
	// Distribution selects which collector binary to describe. An empty value
	// means AllDistributions, the union of every distribution.
	Distribution string
	// HTTPClient fetches remote locations. A nil client uses a default with a
	// 30 second timeout.
	HTTPClient *http.Client
}

// Versions lists every catalog the store can serve, newest first. Templated and
// remote locations cannot be enumerated, so they never appear here even though
// Load can still resolve them.
func (s Store) Versions() []string {
	seen := map[string]bool{}

	var out []string

	for _, loc := range s.locations() {
		for _, v := range s.versionsAt(loc) {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) > 0 })

	return out
}

// Load returns the catalog for a collector version. The special value "latest"
// (or an empty string) selects the newest catalog the store can enumerate.
func (s Store) Load(version string) (*Catalog, error) {
	if version == "" || version == Latest {
		versions := s.Versions()
		if len(versions) == 0 {
			return nil, fmt.Errorf("%w in %s", errNoCatalogs, strings.Join(s.locations(), ", "))
		}

		version = versions[0]
	}

	version = Normalize(version)

	var tried []string

	for _, loc := range s.locations() {
		c, err := s.loadFrom(loc, version)
		switch {
		case err == nil:
			return c, nil
		case errors.Is(err, os.ErrNotExist), errors.Is(err, errNotFound):
			tried = append(tried, s.resolve(loc, version))
		default:
			return nil, fmt.Errorf("%s: %w", s.resolve(loc, version), err)
		}
	}

	return nil, &UnknownVersionError{Version: version, Available: s.Versions(), Tried: tried}
}

// distribution returns the distribution to serve, defaulting to "all".
func (s Store) distribution() string {
	if s.Distribution == "" {
		return AllDistributions
	}

	return s.Distribution
}

// versionsAt lists the versions one location can serve. A template names a
// single file and cannot be listed, so it contributes nothing.
func (s Store) versionsAt(loc string) []string {
	switch kindOf(loc) {
	case locEmbedded:
		entries, _ := fs.ReadDir(catalogs.FS, AllDistributions)

		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, trimExt(e.Name()))
		}

		return out
	case locRemote:
		idx, err := s.fetchIndex(loc)
		if err != nil {
			return nil
		}

		return idx.Versions(s.distribution())
	case locDir:
		idx, err := ReadIndexFile(filepath.Join(loc, IndexFile))
		if err == nil {
			return idx.Versions(s.distribution())
		}

		return flatVersions(loc)
	default:
		return nil
	}
}

// flatVersions lists "<dir>/<version>.<ext>", the layout used before catalogs
// were split by distribution.
func flatVersions(dir string) []string {
	var out []string

	for _, ext := range extensions() {
		names, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		for _, n := range names {
			out = append(out, trimExt(filepath.Base(n)))
		}
	}

	return out
}

// trimExt strips a catalog file extension, returning "" for anything else.
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
func isRegistryDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, IndexFile))

	return err == nil
}

// locations returns the locations to search, defaulting to the built-ins.
func (s Store) locations() []string {
	if len(s.Locations) == 0 {
		return []string{Default}
	}

	return s.Locations
}

var (
	// errNotFound signals that a location does not have the requested version.
	errNotFound = errors.New("catalog not found")
	// errNoCatalogs signals that no location could be enumerated at all.
	errNoCatalogs = errors.New("no catalogs available")
	// errBadStatus signals a remote location answering with an unusable status.
	errBadStatus = errors.New("unexpected status")
)

func (s Store) loadFrom(loc, version string) (*Catalog, error) {
	switch kindOf(loc) {
	case locEmbedded:
		// Only the union is embedded, so any other distribution has to come
		// from a location that actually carries it. Answering with the union
		// would silently widen a narrower request.
		if s.distribution() != AllDistributions {
			return nil, errNotFound
		}

		for _, ext := range extensions() {
			f, err := catalogs.FS.Open(path.Join(AllDistributions, version+ext))
			if err != nil {
				continue
			}

			defer func() { _ = f.Close() }()

			return Read(f)
		}

		return nil, errNotFound
	case locRemote:
		return s.loadFromRegistry(loc, version, s.fetch)
	case locTemplate, locFile:
		return s.loadOne(s.resolve(loc, version))
	default:
		// A directory holding an index is a registry root, laid out by
		// distribution. Without one it is the flat legacy layout.
		if isRegistryDir(loc) {
			return s.loadFromRegistry(loc, version, readLocal)
		}

		return loadFlat(loc, version)
	}
}

// loadOne reads a location that names a single catalog, local or remote.
func (s Store) loadOne(target string) (*Catalog, error) {
	if isRemote(target) {
		return s.fetch(target)
	}

	return readLocal(target)
}

// loadFromRegistry reads "<root>/<distribution>/<version>.<ext>", preferring
// the readable form when a root carries both.
func (s Store) loadFromRegistry(root, version string, read func(string) (*Catalog, error)) (*Catalog, error) {
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

// loadFlat reads "<dir>/<version>.<ext>", the layout used before catalogs were
// split by distribution.
func loadFlat(dir, version string) (*Catalog, error) {
	for _, ext := range extensions() {
		path := filepath.Join(dir, version+ext)

		_, err := os.Stat(path)
		if err == nil {
			return ReadFile(path)
		}
	}

	return nil, errNotFound
}

// readLocal reads a catalog from disk, reporting a missing file as errNotFound
// so a registry root can fall through to the next extension.
func readLocal(path string) (*Catalog, error) {
	_, err := os.Stat(path)
	if err != nil {
		return nil, errNotFound
	}

	return ReadFile(path)
}

// fetchIndex reads a remote registry's index.
func (s Store) fetchIndex(root string) (*Index, error) {
	body, err := s.get(join(root, IndexFile))
	if err != nil {
		return nil, err
	}

	defer func() { _ = body.Close() }()

	return ReadIndex(body)
}

// join appends path segments to a registry root, which is a URL or a local
// directory. Slashes are right for both: filepath.Join would break URLs on
// Windows, and a local path with forward slashes reads fine everywhere.
func join(root string, segments ...string) string {
	return strings.Join(append([]string{strings.TrimSuffix(root, "/")}, segments...), "/")
}

func isRemote(loc string) bool {
	return strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://")
}

// fetch reads a catalog over HTTP. Loading is synchronous and the client
// already carries a timeout, so the request runs on a background context.
func (s Store) fetch(url string) (*Catalog, error) {
	body, err := s.get(url)
	if err != nil {
		return nil, err
	}

	defer func() { _ = body.Close() }()

	return Read(body)
}

// get performs the request behind fetch and fetchIndex. The caller closes the
// body. Loading is synchronous and the client already carries a timeout, so the
// request runs on a background context.
func (s Store) get(url string) (io.ReadCloser, error) {
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       defaultFetchTimeout,
			Transport:     nil,
			CheckRedirect: nil,
			Jar:           nil,
		}
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()

		return nil, errNotFound
	default:
		_ = resp.Body.Close()

		return nil, fmt.Errorf("GET %s: %w %s", url, errBadStatus, resp.Status)
	}
}

type locKind int

const (
	// locDir is a local directory: a registry root when it carries an index,
	// otherwise the flat layout.
	locDir locKind = iota
	// locEmbedded is the catalogs built into the binary.
	locEmbedded
	// locTemplate is a path or URL with placeholders, naming one file.
	locTemplate
	// locFile is a path or URL naming one catalog file outright.
	locFile
	// locRemote is a remote registry root.
	locRemote
)

func kindOf(loc string) locKind {
	switch {
	case loc == Default || loc == "embedded":
		return locEmbedded
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
	case locEmbedded:
		return "built-in " + version
	case locFile:
		return loc
	case locTemplate:
		out := strings.ReplaceAll(loc, VersionPlaceholder, version)

		return strings.ReplaceAll(out, DistributionPlaceholder, s.distribution())
	case locRemote:
		return join(loc, s.distribution(), version+extensions()[0])
	default:
		if isRegistryDir(loc) {
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
	msg := "no catalog for collector version " + e.Version
	if len(e.Available) > 0 {
		msg += " (available: " + strings.Join(e.Available, ", ") + ")"
	}

	return msg
}

// Nearest returns the newest available version that is not newer than the
// requested one, which is the closest safe stand-in for a missing catalog.
func (e *UnknownVersionError) Nearest() (string, bool) {
	for _, v := range e.Available { // Available is sorted newest first.
		if Compare(v, e.Version) <= 0 {
			return v, true
		}
	}

	return "", false
}

// Normalize puts a version into the "vX.Y.Z" form used for catalog file names.
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
