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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
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

var sources = []source{
	{name: "core", repo: "open-telemetry/opentelemetry-collector",
		module: "go.opentelemetry.io/collector"},
	{name: "contrib", repo: "open-telemetry/opentelemetry-collector-contrib",
		module: "github.com/open-telemetry/opentelemetry-collector-contrib"},
}

func main() {
	versions := flag.String("version", "", "comma-separated collector releases, e.g. v0.157.0")
	out := flag.String("out", "catalogs", "directory to write catalogs into")
	overlays := flag.String("overlays", "overlays", "directory of field-schema overlays")
	formats := flag.String("formats", "yaml,json", "catalog formats to write")
	cache := flag.String("cache", filepath.Join(os.TempDir(), "otelcol-config-lint-schemagen"),
		"directory to cache downloaded archives in")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-download timeout")
	flag.Parse()

	if *versions == "" {
		fmt.Fprintln(os.Stderr, "schemagen: -version is required")
		flag.Usage()
		os.Exit(2)
	}
	if err := run(splitList(*versions), *out, *overlays, *cache, splitList(*formats), *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(versions []string, outDir, overlayDir, cacheDir string, formats []string, timeout time.Duration) error {
	loaded, err := loadOverlays(overlayDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: timeout}

	for _, v := range versions {
		v = catalog.Normalize(v)
		cat, err := build(client, v, cacheDir)
		if err != nil {
			return fmt.Errorf("%s: %w", v, err)
		}
		applyOverlays(cat, loaded)

		for _, format := range formats {
			dest := filepath.Join(outDir, v+"."+format)
			if err := write(dest, cat, catalog.Format(format)); err != nil {
				return err
			}
			fmt.Printf("wrote %s (%d components)\n", dest, cat.Count())
		}
	}
	return nil
}

// write serialises a catalog to disk, replacing whatever was there before.
func write(dest string, cat *catalog.Catalog, format catalog.Format) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
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
	for _, src := range sources {
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
		fmt.Printf("  %s: %d components\n", src.name, n)
	}
	return cat, nil
}

// fetch downloads a source archive, caching it so regenerating several versions
// does not re-download what is already on disk.
func fetch(client *http.Client, src source, version, cacheDir string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(cacheDir, strings.ReplaceAll(src.repo, "/", "_")+"-"+version+".tar.gz")
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return dest, nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/tags/%s", src.repo, version)
	fmt.Printf("  downloading %s\n", url)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dest, os.Rename(tmp, dest)
}

// metadata is the subset of an upstream metadata.yaml the catalog needs.
type metadata struct {
	Type string `yaml:"type"`
	// DeprecatedType is the legacy name a renamed component is still
	// registered under, which existing configs keep using.
	DeprecatedType string `yaml:"deprecated_type"`
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
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer gz.Close()

	count := 0
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "metadata.yaml" {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return count, err
		}
		var md metadata
		if err := yaml.Unmarshal(raw, &md); err != nil {
			continue // a few metadata files are templates, not component metadata
		}
		comp, kind, ok := convert(md, src, hdr.Name)
		if !ok {
			continue
		}
		if cat.Components[kind] == nil {
			cat.Components[kind] = map[string]*catalog.Component{}
		}
		if _, dup := cat.Components[kind][comp.Type]; dup {
			continue
		}
		cat.Components[kind][comp.Type] = comp
		count++

		// A renamed component stays configurable under its old name, so the
		// catalog carries both and marks the legacy one deprecated.
		if comp.Alias == "" {
			continue
		}
		if _, dup := cat.Components[kind][comp.Alias]; dup {
			continue
		}
		alias := *comp
		alias.Type = comp.Alias
		alias.Alias = ""
		alias.AliasOf = comp.Type
		alias.Deprecated = "renamed to " + strconv.Quote(comp.Type) + " upstream; the old name still resolves for now"
		cat.Components[kind][comp.Alias] = &alias
		count++
	}
	return count, nil
}

// classKind maps an upstream status.class to a linter component kind. Classes
// outside this set (cmd, pkg, scraper, ...) are not configurable components.
var classKind = map[string]config.Kind{
	"receiver":  config.KindReceiver,
	"processor": config.KindProcessor,
	"exporter":  config.KindExporter,
	"extension": config.KindExtension,
	"connector": config.KindConnector,
}

func convert(md metadata, src source, tarPath string) (*catalog.Component, config.Kind, bool) {
	kind, ok := classKind[md.Status.Class]
	if !ok || md.Type == "" || md.Parent != "" {
		// A parent means this is a sub-component such as a hostmetrics
		// scraper, which is configured inside its parent rather than declared
		// on its own.
		return nil, "", false
	}

	comp := &catalog.Component{
		Type:          md.Type,
		Alias:         md.DeprecatedType,
		Stability:     map[string]catalog.Stability{},
		Distributions: md.Status.Distributions,
		Module:        modulePath(src, tarPath),
	}
	if len(comp.Distributions) == 0 {
		comp.Distributions = []string{src.name}
	}

	signals := map[config.Signal]bool{}
	for level, entries := range md.Status.Stability {
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
	for _, s := range config.Signals {
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

	for _, dep := range md.Status.Deprecation {
		if dep.Migration != "" {
			comp.Deprecated = dep.Migration
			break
		}
	}
	return comp, kind, true
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
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var o overlay
		if err := yaml.Unmarshal(raw, &o); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if o.Type == "" || o.Kind == "" {
			return fmt.Errorf("%s: kind and type are required", p)
		}
		out = append(out, o)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	fmt.Printf("loaded %d field overlay(s)\n", len(out))
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
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
