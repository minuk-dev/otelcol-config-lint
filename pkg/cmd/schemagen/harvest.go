package schemagen

import (
	"archive/tar"
	"compress/gzip"
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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Limits for the harvest.
const (
	// maxSchemaBytes bounds one config schema read out of an archive.
	maxSchemaBytes = 4 << 20
	// maxSourceBytes bounds one Go source file read out of an archive.
	maxSourceBytes = 2 << 20
	// maxMetadataBytes bounds how much of a metadata.yaml is read.
	maxMetadataBytes = 1 << 20
)

// errBadStatus reports an upstream archive answering with an unusable status.
var errBadStatus = errors.New("unexpected status")

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

func (o *Options) build(version string) (*schema.Schema, error) {
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
		archive, err := o.fetch(src, version)
		if err != nil {
			return nil, err
		}

		n, err := harvest(cat, set, index, src, archive)
		if err != nil {
			return nil, err
		}

		cat.Sources[src.name] = src.module
		o.logf("  %s: %d components\n", src.name, n)
	}

	// The sources decide the shape; the published schemas only add to it.
	fromSource := attachSourceFields(cat, index)
	published := attachFields(cat, set)

	o.logf("  %d components from source, %d enriched by %d published schemas\n",
		fromSource, published, set.count())

	return cat, nil
}

// fetch downloads a source archive, caching it so regenerating several versions
// does not re-download what is already on disk.
func (o *Options) fetch(src source, version string) (string, error) {
	mkErr := os.MkdirAll(o.cacheDir, dirPerm)
	if mkErr != nil {
		return "", fmt.Errorf("create cache directory: %w", mkErr)
	}

	dest := filepath.Join(o.cacheDir, strings.ReplaceAll(src.repo, "/", "_")+"-"+version+".tar.gz")

	info, err := os.Stat(dest)
	if err == nil && info.Size() > 0 {
		return dest, nil
	}

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/tags/%s", src.repo, version)
	o.logf("  downloading %s\n", url)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := o.client.Do(req)
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

// harvestEntry takes one archive entry: a component's metadata, its published
// schema, or a Go source file. It returns how many schema entries the metadata
// produced.
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
