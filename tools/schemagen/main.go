// Command schemagen builds component schemas from the upstream collector
// sources.
//
// Every collector component ships a metadata.yaml declaring its type, class and
// per-signal stability. schemagen downloads the core and contrib source
// archives for a release, reads those files, and writes a schema JSON that the
// linter embeds. Field-level schemas come from the hand-written overlays in
// schema/overlays, which are merged on top.
//
// Usage:
//
//	go run ./tools/schemagen -version v0.157.0
//	go run ./tools/schemagen -version v0.150.0,v0.157.0 -out schema/data
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

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Exit codes: the command either produced schemas or it did not.
const (
	exitFailure = 1
	exitUsage   = 2
)

// dirPerm is the mode new directories are created with.
const dirPerm = 0o750

// Limits and sentinel errors for the harvest.
const (
	// maxSchemaBytes bounds one config schema read out of an archive.
	maxSchemaBytes = 4 << 20
	// maxSourceBytes bounds one Go source file read out of an archive.
	maxSourceBytes = 2 << 20
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
	// name is the distribution label recorded in the schema.
	name string
	// repo is the GitHub "owner/name" the archive is downloaded from.
	repo string
	// module is the Go module prefix component paths hang off.
	module string
}

// sources lists the upstream repositories a schema is harvested from.
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
	out := flag.String("out", "schemas", "registry directory to write schemas into")
	overlays := flag.String("overlays", "overlays", "directory of field-schema overlays")
	formats := flag.String("formats", "yaml,json", "schema formats to write")
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

	var skipped []string

	for _, v := range versions {
		v = schema.Normalize(v)

		// A release is skipped rather than fatal: generating the full tag
		// history means asking for versions one repository tagged and the
		// other did not, and one gap should not discard the rest of the run.
		cat, err := build(client, v, cacheDir)
		if err != nil {
			logf("  %s: skipped (%v)\n", v, err)
			skipped = append(skipped, v)

			continue
		}

		applyOverlays(cat, loaded)

		err = writeDistributions(outDir, cat, formats)
		if err != nil {
			return err
		}
	}

	if len(skipped) > 0 {
		logf("skipped %d of %d releases: %s\n", len(skipped), len(versions), strings.Join(skipped, ", "))
	}

	return writeIndex(outDir)
}

// writeDistributions splits a merged schema into one file per distribution,
// under "<out>/<distribution>/<version>.<format>". No union is written: it
// would be exactly the distributions put back together, and no collector ships
// it, so checking against one could only hide a component the binary lacks.
func writeDistributions(outDir string, cat *schema.Schema, formats []string) error {
	for _, dist := range distributionsIn(cat) {
		sub := filterDistribution(cat, dist)

		// An empty schema would report every component as unknown, which is
		// worse than having no schema for the release at all.
		if sub.Count() == 0 {
			continue
		}

		dir := filepath.Join(outDir, dist)

		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}

		for _, format := range formats {
			dest := filepath.Join(dir, sub.CollectorVersion+"."+format)

			err := write(dest, sub, schema.Format(format))
			if err != nil {
				return err
			}
		}

		logf("  %s: %d components\n", dist, sub.Count())
	}

	return nil
}

// distributionsIn returns every distribution any component in the schema
// ships in, sorted.
func distributionsIn(cat *schema.Schema) []string {
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

// filterDistribution copies the schema down to the components one
// distribution ships.
func filterDistribution(cat *schema.Schema, dist string) *schema.Schema {
	out := *cat
	out.Distribution = dist
	out.Components = map[config.Kind]map[string]*schema.Component{}

	for kind, byType := range cat.Components {
		for typ, comp := range byType {
			if !slices.Contains(comp.Distributions, dist) {
				continue
			}

			if out.Components[kind] == nil {
				out.Components[kind] = map[string]*schema.Component{}
			}

			// The written component drops the distribution list: the
			// directory it lands in already says which binary ships it. The
			// copy matters because the merged catalogue is filtered once per
			// distribution and still needs the list.
			written := *comp
			written.Distributions = nil
			out.Components[kind][typ] = &written
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

	idx := &schema.Index{Distributions: map[string][]string{}}

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

	dest := filepath.Join(outDir, schema.IndexFile)

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
// formats a schema may be written in.
func versionsIn(dir string) []string {
	seen := map[string]bool{}

	var out []string

	for _, ext := range schema.Extensions() {
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

// write serialises a schema to disk, replacing whatever was there before.
func write(dest string, cat *schema.Schema, format schema.Format) error {
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

func build(client *http.Client, version, cacheDir string) (*schema.Schema, error) {
	// References cross from contrib into core, so every schema has to be in
	// hand before any of them is resolved. The same is true of the Go types a
	// config struct embeds.
	set := newSchemaSet()
	index := newGoIndex()

	cat := &schema.Schema{
		CollectorVersion: version,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Sources:          map[string]string{},
		Components:       map[config.Kind]map[string]*schema.Component{},
	}
	for _, src := range sources() {
		archive, err := fetch(client, src, version, cacheDir)
		if err != nil {
			return nil, err
		}

		n, err := harvest(cat, set, index, src, archive)
		if err != nil {
			return nil, err
		}

		cat.Sources[src.name] = src.module
		logf("  %s: %d components\n", src.name, n)
	}

	// The sources decide the shape; the published schemas only add to it.
	fromSource := attachSourceFields(cat, index)
	published := attachFields(cat, set)

	logf("  %d components from source, %d enriched by %d published schemas\n",
		fromSource, published, set.count())

	return cat, nil
}

// harvestEntry takes one archive entry: a component's metadata, its published
// schema, or a Go source file. It returns how many catalogue entries the
// metadata produced.
func harvestEntry(
	cat *schema.Schema, set *schemaSet, index *goIndex,
	src source, r io.Reader, hdr *tar.Header,
) (int, error) {
	if hdr.Typeflag != tar.TypeReg {
		return 0, nil
	}

	switch {
	case path.Base(hdr.Name) == configSchemaFile:
		return 0, readConfigSchema(r, set, src, hdr.Name)
	case isConfigSource(hdr.Name):
		return 0, readConfigSource(r, index, src, hdr.Name)
	case path.Base(hdr.Name) != "metadata.yaml":
		return 0, nil
	}

	raw, err := io.ReadAll(io.LimitReader(r, maxMetadataBytes))
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", hdr.Name, err)
	}

	var meta metadata

	// A few metadata files are templates rather than component metadata.
	err = yaml.Unmarshal(raw, &meta)
	if err != nil {
		//nolint:nilerr // an unparsable metadata file is skipped, not fatal
		return 0, nil
	}

	return add(cat, meta, src, hdr.Name), nil
}

// readConfigSchema records one config schema out of the archive.
func readConfigSchema(r io.Reader, set *schemaSet, src source, tarPath string) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxSchemaBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", tarPath, err)
	}

	set.add(src, archiveDir(tarPath), raw)

	return nil
}

// readConfigSource records the type declarations in one Go file.
func readConfigSource(r io.Reader, index *goIndex, src source, tarPath string) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxSourceBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", tarPath, err)
	}

	index.add(src.module+"/"+archiveDir(tarPath), raw)

	return nil
}

// archiveDir is the component directory inside an archive, with the archive
// root stripped: "opentelemetry-collector-contrib-0.157.0/receiver/x/y.yaml"
// becomes "receiver/x".
func archiveDir(tarPath string) string {
	dir := path.Dir(tarPath)
	if _, rest, found := strings.Cut(dir, "/"); found {
		return rest
	}

	return ""
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

// metadata is the subset of an upstream metadata.yaml the schema needs.
type metadata struct {
	Type string `yaml:"type"`
	// DeprecatedType is the legacy name a renamed component is still
	// registered under, which existing configs keep using.
	DeprecatedType string `yaml:"deprecated_type"` //nolint:tagliatelle // upstream metadata.yaml is snake_case
	Parent         string `yaml:"parent"`
	Status         struct {
		Class string `yaml:"class"`
		// Stability maps a level to the signals at that level, which is the
		// inverse of how the schema stores it.
		Stability     map[string][]string `yaml:"stability"`
		Distributions []string            `yaml:"distributions"`
		Deprecation   map[string]struct {
			Migration string `yaml:"migration"`
			Date      string `yaml:"date"`
		} `yaml:"deprecation"`
	} `yaml:"status"`
}

// harvest reads every metadata.yaml in an archive and adds the components it
// finds to the schema. Components already present keep their first definition,
// so core wins over contrib for the handful of types shipped by both.
func harvest(cat *schema.Schema, set *schemaSet, index *goIndex, src source, archive string) (int, error) {
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

		n, err := harvestEntry(cat, set, index, src, archiveReader, hdr)
		if err != nil {
			return count, fmt.Errorf("%s: %w", archive, err)
		}

		count += n
	}

	return count, nil
}

// entriesWithAlias is how many schema entries a renamed component produces:
// its current name and the legacy one.
const entriesWithAlias = 2

// add records a component and, when it was renamed upstream, its legacy name.
// It returns how many schema entries it created.
func add(cat *schema.Schema, meta metadata, src source, tarPath string) int {
	comp, kind, ok := convert(meta, src, tarPath)
	if !ok {
		return 0
	}

	if cat.Components[kind] == nil {
		cat.Components[kind] = map[string]*schema.Component{}
	}

	if _, dup := cat.Components[kind][comp.Type]; dup {
		return 0
	}

	cat.Components[kind][comp.Type] = comp

	// A renamed component stays configurable under its old name, so the
	// schema carries both and marks the legacy one deprecated.
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

func convert(meta metadata, src source, tarPath string) (*schema.Component, config.Kind, bool) {
	kind, ok := configurable(meta, tarPath)
	if !ok {
		return nil, "", false
	}

	comp := &schema.Component{
		Type:          meta.Type,
		Alias:         meta.DeprecatedType,
		Stability:     map[string]schema.Stability{},
		Distributions: meta.Status.Distributions,
		Module:        modulePath(src, tarPath),
	}
	if len(comp.Distributions) == 0 {
		comp.Distributions = []string{src.name}
	}

	signals := map[config.Signal]bool{}

	for level, entries := range meta.Status.Stability {
		for _, entry := range entries {
			comp.Stability[entry] = schema.Stability(level)
			switch {
			case entry == "extension":
				// Extensions have no signals.
			case strings.Contains(entry, "_to_"):
				from, to, _ := strings.Cut(entry, "_to_")
				comp.Pairs = append(comp.Pairs, schema.Pair{
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
	Kind       config.Kind   `yaml:"kind"`
	Type       string        `yaml:"type"`
	MinVersion string        `yaml:"minVersion"`
	MaxVersion string        `yaml:"maxVersion"`
	Fields     *schema.Field `yaml:"fields"`
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

func applyOverlays(cat *schema.Schema, overlays []overlay) {
	for _, o := range overlays {
		if o.MinVersion != "" && schema.Compare(cat.CollectorVersion, o.MinVersion) < 0 {
			continue
		}

		if o.MaxVersion != "" && schema.Compare(cat.CollectorVersion, o.MaxVersion) > 0 {
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
