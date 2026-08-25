package schemagen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	// maxSchemaBytes bounds one config schema read out of a module.
	maxSchemaBytes = 4 << 20
	// maxSourceBytes bounds one Go source file read out of a module.
	maxSourceBytes = 2 << 20
	// maxMetadataBytes bounds how much of a metadata.yaml is read.
	maxMetadataBytes = 1 << 20
)

// metadataFile is what every upstream component declares itself in.
const metadataFile = "metadata.yaml"

// errModuleMissing reports a declared component whose module could not be
// resolved, which would leave the schema short of a component the binary has.
var errModuleMissing = errors.New("module could not be resolved")

// build reads one distribution out of the modules its manifest names.
func (o *options) build(man *manifest) (*schema.Schema, error) {
	mods, err := o.resolveModules(man)
	if err != nil {
		return nil, err
	}

	// A component's settings are mostly shared config types living in modules
	// of their own, so the modules it requires are read too, not only the
	// declared ones. References cross module boundaries, so they are all in
	// hand before any of them is resolved.
	set := newSchemaSet()
	index := newGoIndex()
	metas := map[string]metadata{}
	worth := scannable(man)

	for path, mod := range mods.byPath {
		if worth(path) {
			scanModule(mod, set, index, metas)
		}
	}

	cat := &schema.Schema{
		CollectorVersion: man.collectorVersion(),
		Distribution:     man.Dist.Name,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Sources:          map[string]string{},
		Components:       map[config.Kind]map[string]*schema.Component{},
	}
	if man.Dist.Module != "" {
		cat.Sources[man.Dist.Name] = man.Dist.Module
	}

	declared := 0

	for _, comp := range man.components() {
		mod, ok := mods.lookup(comp.module)
		if !ok {
			// A schema missing a component reports every config that uses it as
			// invalid, which is worse than having no schema for the release, so
			// one module short is one too many.
			return nil, fmt.Errorf("%w: %s", errModuleMissing, comp.module)
		}

		declared += add(cat, comp, metas[mod.Path])
	}

	if cat.Count() == 0 {
		return nil, fmt.Errorf("%s: %w", man.Dist.Name, errNoComponents)
	}

	fromSource := attachSourceFields(cat, index)
	published := attachFields(cat, set)

	o.logf("  %d components, %d from source, %d enriched by %d published schemas\n",
		declared, fromSource, published, set.count())

	return cat, nil
}

// scannable reports which modules are worth reading. A distribution's build
// list is the whole dependency graph -- the AWS SDK, the Kubernetes client,
// Docker -- and none of that can hold a component or a config schema. What can
// is a declared module, or anything from the same repository as one, which is
// where the shared config types live.
func scannable(man *manifest) func(module string) bool {
	roots := append([]string{}, repositoryRoots()...)

	for _, decl := range man.components() {
		roots = append(roots, repositoryRootOf(decl.module))
	}

	return func(module string) bool {
		for _, root := range roots {
			if root != "" && (module == root || strings.HasPrefix(module, root+"/")) {
				return true
			}
		}

		return false
	}
}

// repositoryRootOf is the repository a module lives in: a known upstream root
// when it is one of those, and otherwise "host/owner/repository", which is
// where a private component's shared configuration sits too.
func repositoryRootOf(module string) string {
	for _, root := range repositoryRoots() {
		if module == root || strings.HasPrefix(module, root+"/") {
			return root
		}
	}

	parts := strings.Split(module, "/")
	if len(parts) < repositorySegments {
		return module
	}

	return strings.Join(parts[:repositorySegments], "/")
}

// repositorySegments is how many path elements name a repository on the hosts
// that spell it that way: "github.com/owner/repository".
const repositorySegments = 3

// scanModule reads one module directory: the component it declares, the config
// schemas it publishes and the Go types its settings are written in. Everything
// is keyed by import path, which is how a reference from another module spells
// it.
//
// Only the component search prunes internal and cmd: a component cannot be
// declared there, but the types and schemas its settings are made of very much
// live there -- an exporter's sending_queue is defined in exporterhelper's own
// internal package.
func scanModule(mod resolvedModule, set *schemaSet, index *goIndex, metas map[string]metadata) {
	root := filepath.Clean(mod.Dir)

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner of a module is skipped, not fatal
		}

		if d.IsDir() {
			if skipDir(d.Name()) && p != root {
				return fs.SkipDir
			}

			return nil
		}

		importPath := importPathOf(mod.Path, root, p)

		switch {
		case d.Name() == metadataFile:
			if declarable(importPath, mod.Path) {
				readMetadata(p, importPath, metas)
			}
		case d.Name() == configSchemaFile:
			if raw, ok := readLimited(p, maxSchemaBytes); ok {
				set.add(importPath, raw)
			}
		case isConfigSource(p):
			if raw, ok := readLimited(p, maxSourceBytes); ok {
				index.add(importPath, raw)
			}
		}

		return nil
	})
}

// skipDir reports directories that hold nothing worth reading at all.
func skipDir(name string) bool {
	switch name {
	case "testdata", "examples", ".git":
		return true
	default:
		return false
	}
}

// declarable reports whether a metadata file describes a component a config can
// declare. One under "cmd" is mdatagen's own sample, shipped to exercise its
// code generation, and one under "internal" is a component's sub-part, such as
// the resourcedetection providers configured inside their parent's list.
func declarable(importPath, module string) bool {
	rest, found := strings.CutPrefix(importPath, module)
	if !found {
		return false
	}

	for seg := range strings.SplitSeq(strings.Trim(rest, "/"), "/") {
		if seg == "cmd" || seg == "internal" {
			return false
		}
	}

	return true
}

// importPathOf spells a file's directory the way another module's reference
// would: the module path, then the directory inside it.
func importPathOf(module, root, file string) string {
	rel, err := filepath.Rel(root, filepath.Dir(file))
	if err != nil || rel == "." {
		return module
	}

	return module + "/" + filepath.ToSlash(rel)
}

func readLimited(path string, limit int64) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > limit {
		return nil, false
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	return raw, true
}

// readMetadata records a component's own declaration. A few metadata files are
// templates rather than component metadata, and are skipped.
func readMetadata(file, importPath string, metas map[string]metadata) {
	raw, ok := readLimited(file, maxMetadataBytes)
	if !ok {
		return
	}

	var meta metadata

	err := yaml.Unmarshal(raw, &meta)
	if err != nil || meta.Type == "" {
		return
	}

	// A parent means this is a sub-component such as a hostmetrics scraper,
	// which is configured inside its parent rather than declared on its own.
	if meta.Parent != "" {
		return
	}

	metas[importPath] = meta
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
		Stability   map[string][]string `yaml:"stability"`
		Deprecation map[string]struct {
			Migration string `yaml:"migration"`
			Date      string `yaml:"date"`
		} `yaml:"deprecation"`
	} `yaml:"status"`
}

// entriesWithAlias is how many schema entries a renamed component produces:
// its current name and the legacy one.
const entriesWithAlias = 2

// add records a declared component and, when it was renamed upstream, its
// legacy name. It returns how many schema entries it created.
func add(cat *schema.Schema, decl declared, meta metadata) int {
	comp := convert(decl, meta)

	if cat.Components[decl.kind] == nil {
		cat.Components[decl.kind] = map[string]*schema.Component{}
	}

	if _, dup := cat.Components[decl.kind][comp.Type]; dup {
		return 0
	}

	cat.Components[decl.kind][comp.Type] = comp

	// A renamed component stays configurable under its old name, so the
	// schema carries both and marks the legacy one deprecated.
	if comp.Alias == "" {
		return 1
	}

	if _, dup := cat.Components[decl.kind][comp.Alias]; dup {
		return 1
	}

	alias := *comp
	alias.Type = comp.Alias
	alias.Alias = ""
	alias.AliasOf = comp.Type
	alias.Deprecated = "renamed to " + strconv.Quote(comp.Type) +
		" upstream; the old name still resolves for now"
	cat.Components[decl.kind][comp.Alias] = &alias

	return entriesWithAlias
}

// convert builds the schema entry for a declared component. The manifest is
// what says the component is in the binary and which kind it is declared under;
// the metadata, when the module ships one, fills in everything else. A
// component without metadata -- a private one, typically -- is still recorded,
// so a config that names it is not reported as unknown.
func convert(decl declared, meta metadata) *schema.Component {
	comp := &schema.Component{
		Type:      typeOf(decl, meta),
		Alias:     meta.DeprecatedType,
		Stability: map[string]schema.Stability{},
		Module:    decl.module,
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

	return comp
}

// typeOf is the name a config declares the component under: what the component
// says it is called, and failing that what the manifest implies.
func typeOf(decl declared, meta metadata) string {
	if meta.Type != "" {
		return meta.Type
	}

	return decl.typeName()
}
