package catalog

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minuk-dev/otel-collector-config-linter/catalogs"
)

// extensions are the catalog file suffixes, in preference order.
var extensions = []string{".yaml", ".yml", ".json"}

// Latest selects the newest catalog available in a store.
const Latest = "latest"

// Default names the catalogs built into the binary. It can be listed among a
// store's locations to control where the built-ins are consulted.
const Default = "default"

// VersionPlaceholder is substituted with the collector version in a location
// template, e.g. "https://example.com/otel/{{.Version}}.json".
const VersionPlaceholder = "{{.Version}}"

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
	// HTTPClient fetches remote locations. A nil client uses a default with a
	// 30 second timeout.
	HTTPClient *http.Client
}

// locations returns the locations to search, defaulting to the built-ins.
func (s Store) locations() []string {
	if len(s.Locations) == 0 {
		return []string{Default}
	}
	return s.Locations
}

// Versions lists every catalog the store can serve, newest first. Templated and
// remote locations cannot be enumerated, so they never appear here even though
// Load can still resolve them.
func (s Store) Versions() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		v := name
		for _, ext := range extensions {
			v = strings.TrimSuffix(v, ext)
		}
		if v != "" && v != name && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, loc := range s.locations() {
		switch kindOf(loc) {
		case locEmbedded:
			entries, _ := fs.ReadDir(catalogs.FS, ".")
			for _, e := range entries {
				add(e.Name())
			}
		case locDir:
			for _, ext := range extensions {
				names, _ := filepath.Glob(filepath.Join(loc, "*"+ext))
				for _, n := range names {
					add(filepath.Base(n))
				}
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
			return nil, fmt.Errorf("no catalogs available in %s", strings.Join(s.locations(), ", "))
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
		case os.IsNotExist(err) || err == errNotFound:
			tried = append(tried, resolve(loc, version))
		default:
			return nil, fmt.Errorf("%s: %w", resolve(loc, version), err)
		}
	}
	return nil, &UnknownVersionError{Version: version, Available: s.Versions(), Tried: tried}
}

// errNotFound signals that a location does not have the requested version.
var errNotFound = fmt.Errorf("catalog not found")

func (s Store) loadFrom(loc, version string) (*Catalog, error) {
	switch kindOf(loc) {
	case locEmbedded:
		for _, ext := range extensions {
			f, err := catalogs.FS.Open(version + ext)
			if err != nil {
				continue
			}
			defer f.Close()
			return Read(f)
		}
		return nil, errNotFound
	case locRemote:
		return s.fetch(resolve(loc, version))
	case locTemplate:
		path := resolve(loc, version)
		if _, err := os.Stat(path); err != nil {
			return nil, errNotFound
		}
		return ReadFile(path)
	default:
		// A directory holds "<version>.yaml" or "<version>.json"; the readable
		// form is preferred when both are present.
		for _, ext := range extensions {
			path := filepath.Join(loc, version+ext)
			if _, err := os.Stat(path); err == nil {
				return ReadFile(path)
			}
		}
		return nil, errNotFound
	}
}

func (s Store) fetch(url string) (*Catalog, error) {
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return Read(resp.Body)
}

type locKind int

const (
	locDir locKind = iota
	locEmbedded
	locTemplate
	locRemote
)

func kindOf(loc string) locKind {
	switch {
	case loc == Default || loc == "embedded":
		return locEmbedded
	case strings.HasPrefix(loc, "http://"), strings.HasPrefix(loc, "https://"):
		return locRemote
	case strings.Contains(loc, VersionPlaceholder):
		return locTemplate
	default:
		return locDir
	}
}

// resolve turns a location into the concrete path or URL for a version.
func resolve(loc, version string) string {
	switch kindOf(loc) {
	case locEmbedded:
		return "built-in " + version
	case locTemplate, locRemote:
		return strings.ReplaceAll(loc, VersionPlaceholder, version)
	default:
		return filepath.Join(loc, version+extensions[0])
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
	msg := fmt.Sprintf("no catalog for collector version %s", e.Version)
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
	for i, part := range strings.SplitN(v, ".", 3) {
		if i >= len(out) {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out[i] = n
	}
	return out
}
