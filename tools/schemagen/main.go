// Command schemagen builds component catalogs from the upstream collector
// sources.
//
// Every collector component ships a metadata.yaml declaring its type, class and
// per-signal stability. schemagen downloads the core and contrib source
// archives for a release, reads those files, and writes a catalog JSON that the
// linter embeds. Field-level schemas come from the hand-written overlays in
// catalog/overlays, which are merged on top.
//
// Usage:
//
//	go run ./tools/schemagen -version v0.157.0
//	go run ./tools/schemagen -version v0.150.0,v0.157.0 -out catalog/data
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Exit codes: the command either produced catalogs or it did not.
const (
	exitFailure = 1
	exitUsage   = 2
)

// dirPerm is the mode new directories are created with.
const dirPerm = 0o750

// Limits and sentinel errors for the harvest.
const (
	// maxMetadataBytes bounds how much of a metadata.yaml is read.
	maxMetadataBytes = 1 << 20
	// downloadTimeout bounds a single source archive download.
	downloadTimeout = 5 * time.Minute
)

var (
	// errBadStatus reports an upstream archive answering with an unusable status.
	errBadStatus = errors.New("unexpected status")
	// errIncompleteOverlay reports an overlay missing its kind or type.
	errIncompleteOverlay = errors.New("kind and type are required")
)

// source is one upstream repository to harvest components from.
type source struct {
	// name is the distribution label recorded in the catalog.
	name string
	// repo is the GitHub "owner/name" the archive is downloaded from.
	repo string
	// module is the Go module prefix component paths hang off.
	module string
}

// sources lists the upstream repositories a catalog is harvested from.
func sources() []source {
	return []source{
		{
			name:   "core",
			repo:   "open-telemetry/opentelemetry-collector",
			module: "go.opentelemetry.io/collector",
		},
		{
			name:   "contrib",
			repo:   "open-telemetry/opentelemetry-collector-contrib",
			module: "github.com/open-telemetry/opentelemetry-collector-contrib",
		},
	}
}

// logf reports progress. schemagen is a developer command whose output is the
// point, but it goes through one helper so nothing calls fmt.Print* directly.
func logf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...)
}

func main() {
	versions := flag.String("version", "", "comma-separated collector releases, e.g. v0.157.0")
	out := flag.String("out", "catalogs", "directory to write catalogs into")
	overlays := flag.String("overlays", "overlays", "directory of field-schema overlays")
	formats := flag.String("formats", "yaml,json", "catalog formats to write")
	cache := flag.String("cache", filepath.Join(os.TempDir(), "otelcol-config-lint-schemagen"),
		"directory to cache downloaded archives in")
	timeout := flag.Duration("timeout", downloadTimeout, "per-download timeout")

	flag.Parse()

	if *versions == "" {
		_, _ = fmt.Fprintln(os.Stderr, "schemagen: -version is required")

		flag.Usage()
		os.Exit(exitUsage)
	}

	err := run(splitList(*versions), *out, *overlays, *cache, splitList(*formats), *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(exitFailure)
	}
}

func run(versions []string, outDir, overlayDir, cacheDir string, formats []string, timeout time.Duration) error {
	loaded, err := loadOverlays(overlayDir)
	if err != nil {
		return err
	}

	err = os.MkdirAll(outDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	client := &http.Client{
		Timeout:       timeout,
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           nil,
	}

	for _, v := range versions {
		v = catalog.Normalize(v)

		cat, err := build(client, v, cacheDir)
		if err != nil {
			return fmt.Errorf("%s: %w", v, err)
		}

		applyOverlays(cat, loaded)

		err = writeDistributions(outDir, cat, formats)
		if err != nil {
			return err
		}
	}

	return writeIndex(outDir)
}

// writeDistributions splits a merged catalog into one file per distribution,
// under "<out>/<distribution>/<version>.<format>". The union is written too,
// as the "all" distribution.
func writeDistributions(outDir string, cat *catalog.Catalog, formats []string) error {
	for _, dist := range append([]string{catalog.AllDistributions}, distributionsIn(cat)...) {
		sub := filterDistribution(cat, dist)

		dir := filepath.Join(outDir, dist)

		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}

		for _, format := range formats {
			dest := filepath.Join(dir, sub.CollectorVersion+"."+format)

			err := write(dest, sub, catalog.Format(format))
			if err != nil {
				return err
			}
		}

		logf("  %s: %d components\n", dist, sub.Count())
	}

	return nil
}

// distributionsIn returns every distribution any component in the catalog
// ships in, sorted.
func distributionsIn(cat *catalog.Catalog) []string {
	seen := map[string]bool{}

	for _, byType := range cat.Components {
		for _, comp := range byType {
			for _, d := range comp.Distributions {
				seen[d] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}

	sort.Strings(out)

	return out
}

// filterDistribution copies the catalog down to the components one
// distribution ships. "all" keeps everything.
func filterDistribution(cat *catalog.Catalog, dist string) *catalog.Catalog {
	out := *cat
	out.Distribution = dist
	out.Components = map[config.Kind]map[string]*catalog.Component{}

	for kind, byType := range cat.Components {
		for typ, comp := range byType {
			if dist != catalog.AllDistributions && !slices.Contains(comp.Distributions, dist) {
				continue
			}

			if out.Components[kind] == nil {
				out.Components[kind] = map[string]*catalog.Component{}
			}

			out.Components[kind][typ] = comp
		}
	}

	return &out
}

// writeIndex records what the registry can serve. It is rebuilt by listing the
// output directory, not from the versions generated in this run, so
// regenerating one release leaves the others listed. Versions are recorded per
// distribution because coverage differs: upstream had no otlp distribution
// before v0.120.0.
func writeIndex(outDir string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", outDir, err)
	}

	idx := &catalog.Index{Distributions: map[string][]string{}}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		versions := versionsIn(filepath.Join(outDir, e.Name()))
		if len(versions) == 0 {
			// Not a distribution, just a directory that happens to sit here.
			continue
		}

		idx.Distributions[e.Name()] = versions
	}

	dest := filepath.Join(outDir, catalog.IndexFile)

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	err = idx.Write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return err
	}

	logf("wrote %s (%d distributions)\n", dest, len(idx.Distributions))

	return nil
}

// versionsIn lists the releases a distribution directory holds, in any of the
// formats a catalog may be written in.
func versionsIn(dir string) []string {
	seen := map[string]bool{}

	var out []string

	for _, ext := range catalog.Extensions() {
		names, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		for _, n := range names {
			v := strings.TrimSuffix(filepath.Base(n), ext)
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	return out
}

// write serialises a catalog to disk, replacing whatever was there before.
func write(dest string, cat *catalog.Catalog, format catalog.Format) error {
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	err = cat.Write(f, format)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}

func build(client *http.Client, version, cacheDir string) (*catalog.Catalog, error) {
	cat := &catalog.Catalog{
		CollectorVersion: version,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Sources:          map[string]string{},
		Components:       map[config.Kind]map[string]*catalog.Component{},
	}
	for _, src := range sources() {
		archive, err := fetch(client, src, version, cacheDir)
		if err != nil {
			return nil, err
		}

		n, err := harvest(cat, src, archive)
		if err != nil {
			return nil, err
		}

		cat.Distributions = append(cat.Distributions, src.name)
		cat.Sources[src.name] = src.module
		logf("  %s: %d components\n", src.name, n)
	}

	return cat, nil
}

// fetch downloads a source archive, caching it so regenerating several versions
// does not re-download what is already on disk.
func fetch(client *http.Client, src source, version, cacheDir string) (string, error) {
	mkErr := os.MkdirAll(cacheDir, dirPerm)
	if mkErr != nil {
		return "", fmt.Errorf("create cache directory: %w", mkErr)
	}

	dest := filepath.Join(cacheDir, strings.ReplaceAll(src.repo, "/", "_")+"-"+version+".tar.gz")

	info, err := os.Stat(dest)
	if err == nil && info.Size() > 0 {
		return dest, nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/tags/%s", src.repo, version)
	logf("  downloading %s\n", url)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %w %s", url, errBadStatus, resp.Status)
	}

	tmp := dest + ".part"

	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", tmp, err)
	}

	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = os.Remove(tmp)

		return "", fmt.Errorf("download %s: %w", url, err)
	}

	err = os.Rename(tmp, dest)
	if err != nil {
		return "", fmt.Errorf("finish download of %s: %w", url, err)
	}

	return dest, nil
}

// metadata is the subset of an upstream metadata.yaml the catalog needs.
type metadata struct {
	Type string `yaml:"type"`
	// DeprecatedType is the legacy name a renamed component is still
	// registered under, which existing configs keep using.
	DeprecatedType string `yaml:"deprecated_type"` //nolint:tagliatelle // upstream metadata.yaml is snake_case
	Parent         string `yaml:"parent"`
	Status         struct {
		Class string `yaml:"class"`
		// Stability maps a level to the signals at that level, which is the
		// inverse of how the catalog stores it.
		Stability     map[string][]string `yaml:"stability"`
		Distributions []string            `yaml:"distributions"`
		Deprecation   map[string]struct {
			Migration string `yaml:"migration"`
			Date      string `yaml:"date"`
		} `yaml:"deprecation"`
	} `yaml:"status"`
}

// harvest reads every metadata.yaml in an archive and adds the components it
// finds to the catalog. Components already present keep their first definition,
// so core wins over contrib for the handful of types shipped by both.
func harvest(cat *catalog.Catalog, src source, archive string) (int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", archive, err)
	}

	defer func() { _ = f.Close() }()

	unzipped, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", archive, err)
	}

	defer func() { _ = unzipped.Close() }()

	count := 0
	archiveReader := tar.NewReader(unzipped)

	for {
		hdr, err := archiveReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return count, fmt.Errorf("read %s: %w", archive, err)
		}

		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "metadata.yaml" {
			continue
		}

		raw, err := io.ReadAll(io.LimitReader(archiveReader, maxMetadataBytes))
		if err != nil {
			return count, fmt.Errorf("read %s from %s: %w", hdr.Name, archive, err)
		}

		var meta metadata

		// A few metadata files are templates rather than component metadata.
		if yaml.Unmarshal(raw, &meta) != nil {
			continue
		}

		count += add(cat, meta, src, hdr.Name)
	}

	return count, nil
}

// entriesWithAlias is how many catalog entries a renamed component produces:
// its current name and the legacy one.
const entriesWithAlias = 2

// add records a component and, when it was renamed upstream, its legacy name.
// It returns how many catalog entries it created.
func add(cat *catalog.Catalog, meta metadata, src source, tarPath string) int {
	comp, kind, ok := convert(meta, src, tarPath)
	if !ok {
		return 0
	}

	if cat.Components[kind] == nil {
		cat.Components[kind] = map[string]*catalog.Component{}
	}

	if _, dup := cat.Components[kind][comp.Type]; dup {
		return 0
	}

	cat.Components[kind][comp.Type] = comp

	// A renamed component stays configurable under its old name, so the
	// catalog carries both and marks the legacy one deprecated.
	if comp.Alias == "" {
		return 1
	}

	if _, dup := cat.Components[kind][comp.Alias]; dup {
		return 1
	}

	alias := *comp
	alias.Type = comp.Alias
	alias.Alias = ""
	alias.AliasOf = comp.Type
	alias.Deprecated = "renamed to " + strconv.Quote(comp.Type) +
		" upstream; the old name still resolves for now"
	cat.Components[kind][comp.Alias] = &alias

	return entriesWithAlias
}

// classKind maps an upstream status.class to a linter component kind, and
// reports whether the class is a configurable component at all. Classes outside
// this set (cmd, pkg, scraper, ...) are not.
func classKind(class string) (config.Kind, bool) {
	for _, k := range config.Kinds() {
		if string(k) == class {
			return k, true
		}
	}

	return "", false
}

// configurable resolves the component kind for an upstream metadata file and
// reports whether the file describes something a config can declare at all.
func configurable(meta metadata, tarPath string) (config.Kind, bool) {
	kind, ok := classKind(meta.Status.Class)
	if !ok || meta.Type == "" {
		return "", false
	}

	// A parent means this is a sub-component such as a hostmetrics scraper,
	// which is configured inside its parent rather than declared on its own.
	if meta.Parent != "" || !declarable(tarPath) {
		return "", false
	}

	return kind, true
}

func convert(meta metadata, src source, tarPath string) (*catalog.Component, config.Kind, bool) {
	kind, ok := configurable(meta, tarPath)
	if !ok {
		return nil, "", false
	}

	comp := &catalog.Component{
		Type:          meta.Type,
		Alias:         meta.DeprecatedType,
		Stability:     map[string]catalog.Stability{},
		Distributions: meta.Status.Distributions,
		Module:        modulePath(src, tarPath),
	}
	if len(comp.Distributions) == 0 {
		comp.Distributions = []string{src.name}
	}

	signals := map[config.Signal]bool{}

	for level, entries := range meta.Status.Stability {
		for _, entry := range entries {
			comp.Stability[entry] = catalog.Stability(level)
			switch {
			case entry == "extension":
				// Extensions have no signals.
			case strings.Contains(entry, "_to_"):
				from, to, _ := strings.Cut(entry, "_to_")
				comp.Pairs = append(comp.Pairs, catalog.Pair{
					From: config.Signal(from), To: config.Signal(to),
				})
			default:
				signals[config.Signal(entry)] = true
			}
		}
	}

	for _, s := range config.Signals() {
		if signals[s] {
			comp.Signals = append(comp.Signals, s)
		}
	}

	sort.Slice(comp.Pairs, func(i, j int) bool {
		if comp.Pairs[i].From != comp.Pairs[j].From {
			return comp.Pairs[i].From < comp.Pairs[j].From
		}

		return comp.Pairs[i].To < comp.Pairs[j].To
	})

	for _, dep := range meta.Status.Deprecation {
		if dep.Migration != "" {
			comp.Deprecated = dep.Migration

			break
		}
	}

	return comp, kind, true
}

// declarable reports whether an in-archive metadata path belongs to a component
// that can be declared in a config. A real component sits directly at
// "<kind>/<name>", never under "cmd" — mdatagen ships sample components to
// exercise its own code generation — and never under "internal", which holds a
// component's sub-parts, such as the resourcedetection providers that are
// configured inside their parent's "detectors" list. Upstream leaves the
// metadata "parent" field unset for both, so the path is what tells them apart.
func declarable(tarPath string) bool {
	dir := path.Dir(tarPath)

	_, rest, found := strings.Cut(dir, "/") // drop the archive root
	if !found {
		return false
	}

	for seg := range strings.SplitSeq(rest, "/") {
		if seg == "cmd" || seg == "internal" {
			return false
		}
	}

	return true
}

// modulePath turns an in-archive path such as
// "opentelemetry-collector-contrib-0.157.0/receiver/filelogreceiver/metadata.yaml"
// into the component's Go module path.
func modulePath(src source, tarPath string) string {
	dir := path.Dir(tarPath)
	if _, rest, found := strings.Cut(dir, "/"); found { // drop the archive root
		dir = rest
	} else {
		return src.module
	}

	return src.module + "/" + dir
}

// overlay adds a field schema to a component the upstream metadata cannot
// describe.
type overlay struct {
	Kind       config.Kind    `yaml:"kind"`
	Type       string         `yaml:"type"`
	MinVersion string         `yaml:"minVersion"`
	MaxVersion string         `yaml:"maxVersion"`
	Fields     *catalog.Field `yaml:"fields"`
}

func loadOverlays(dir string) ([]overlay, error) {
	var out []overlay

	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // overlays are optional
			}

			return err
		}

		if d.IsDir() || (filepath.Ext(p) != ".yaml" && filepath.Ext(p) != ".yml") {
			return nil
		}

		raw, err := os.ReadFile(p) //nolint:gosec // the overlay directory is a build input, not user data
		if err != nil {
			return fmt.Errorf("read overlay %s: %w", p, err)
		}

		var o overlay

		err = yaml.Unmarshal(raw, &o)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		if o.Type == "" || o.Kind == "" {
			return fmt.Errorf("%s: %w", p, errIncompleteOverlay)
		}

		out = append(out, o)

		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load overlays: %w", err)
	}

	logf("loaded %d field overlay(s)\n", len(out))

	return out, nil
}

func applyOverlays(cat *catalog.Catalog, overlays []overlay) {
	for _, o := range overlays {
		if o.MinVersion != "" && catalog.Compare(cat.CollectorVersion, o.MinVersion) < 0 {
			continue
		}

		if o.MaxVersion != "" && catalog.Compare(cat.CollectorVersion, o.MaxVersion) > 0 {
			continue
		}

		comp, ok := cat.Lookup(o.Kind, o.Type)
		if !ok {
			continue // the component does not exist in this release
		}

		comp.Fields = o.Fields
	}
}

func splitList(s string) []string {
	var out []string

	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}
