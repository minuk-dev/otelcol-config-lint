package schemagen

import (
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// configSchemaFile is the name upstream publishes a component's config schema
// under, alongside its metadata.yaml.
const configSchemaFile = "config.schema.yaml"

// The field types a schema can state, spelled once.
const (
	typeMap      = "map"
	typeList     = "list"
	typeString   = "string"
	typeInt      = "int"
	typeFloat    = "float"
	typeBool     = "bool"
	typeDuration = "duration"
)

// maxFieldDepth is a backstop only. Expansion normally terminates by detecting
// a reference cycle; this catches a schema that nests deeply without repeating
// a reference, so a malformed input cannot run away.
const maxFieldDepth = 64

// jsonSchema is the subset of JSON Schema upstream emits. Only the keywords
// that actually appear are modelled; anyOf, oneOf and required never do.
type jsonSchema struct {
	Ref         string                 `yaml:"$ref"`
	Type        string                 `yaml:"type"`
	Format      string                 `yaml:"format"`
	Description string                 `yaml:"description"`
	Properties  map[string]*jsonSchema `yaml:"properties"`
	Items       *jsonSchema            `yaml:"items"`
	Enum        []yaml.Node            `yaml:"enum"`
	Defs        map[string]*jsonSchema `yaml:"$defs"`
	AllOf       []*jsonSchema          `yaml:"allOf"`
	// AdditionalProperties is a bool or a schema, so it is kept as a node and
	// only tested for presence.
	AdditionalProperties *yaml.Node `yaml:"additionalProperties"`
}

// schemaSet holds every config schema found across the distribution's modules,
// keyed by import path
// ("go.opentelemetry.io/collector/config/confighttp"), which is how a reference
// from another module spells it.
type schemaSet struct {
	byKey map[string]*jsonSchema
	// seen counts the files, which byKey cannot: one file is keyed twice.
	seen map[string]bool
}

func newSchemaSet() *schemaSet {
	return &schemaSet{byKey: map[string]*jsonSchema{}, seen: map[string]bool{}}
}

// add records a config schema under both spellings a reference may use: the
// import path it was published under, and the repository-absolute path, which
// is how upstream's own schemas refer to each other -- "/config/configtls"
// rather than "go.opentelemetry.io/collector/config/configtls".
func (s *schemaSet) add(importPath string, raw []byte) {
	var doc jsonSchema

	err := yaml.Unmarshal(raw, &doc)
	if err != nil {
		return
	}

	s.byKey[importPath] = &doc
	s.seen[importPath] = true

	if repo, ok := repositoryPath(importPath); ok {
		s.byKey[repo] = &doc
	}
}

// repositoryPath is the repository-absolute spelling of an import path, and
// reports whether the module belongs to a repository that uses that spelling.
// Only the two upstream repositories publish config schemas, and only they
// write references this way, so only they have to be recognised.
func repositoryPath(importPath string) (string, bool) {
	for _, root := range repositoryRoots() {
		if rest, found := strings.CutPrefix(importPath, root+"/"); found {
			return "/" + rest, true
		}
	}

	return "", false
}

// coreModuleRoot is the module prefix of the collector itself, whose release is
// the one a config is written against.
const coreModuleRoot = "go.opentelemetry.io/collector"

// repositoryRoots are the module prefixes of the repositories whose published
// schemas reference each other repository-absolutely.
func repositoryRoots() []string {
	return []string{
		coreModuleRoot,
		"github.com/open-telemetry/opentelemetry-collector-contrib",
	}
}

// lookup resolves a $ref against the file that contained it. A reference is
// "<where>.<name>", where <where> is a module path, a repository-absolute path
// or a path relative to the referring file, and <name> names an entry in that
// file's $defs. A reference with no path at all names a $def in the file
// itself.
func (s *schemaSet) lookup(ref, fromDir string) *jsonSchema {
	where, name, found := lastDot(ref)
	if !found {
		// A bare name is local to the referring file.
		return s.def(fromDir, ref)
	}

	if strings.HasPrefix(where, "./") || strings.HasPrefix(where, "../") {
		return s.def(path.Join(fromDir, where), name)
	}

	return s.def(where, name)
}

// count reports how many config schemas were collected, counting each file
// once rather than once per spelling it is keyed under.
func (s *schemaSet) count() int { return len(s.seen) }

// def looks a definition up in the file keyed by either spelling. A reference
// relative to a module-qualified location resolves to a module-qualified key,
// while one relative to a repository path resolves to a rooted key, and the
// same target has to be reachable both ways.
func (s *schemaSet) def(key, name string) *jsonSchema {
	owner, ok := s.byKey[key]
	if !ok {
		owner, ok = s.byKey["/"+key]
	}

	if !ok {
		owner, ok = s.byKey[strings.TrimPrefix(key, "/")]
	}

	if !ok {
		return nil
	}

	return owner.Defs[name]
}

// lastDot splits a reference at its final ".", which separates the path from
// the definition name. Module paths contain dots of their own
// ("go.opentelemetry.io/collector/..."), so splitting at the first one would
// cut in the wrong place.
func lastDot(ref string) (string, string, bool) {
	i := strings.LastIndex(ref, ".")
	if i < 0 {
		return ref, "", false
	}

	return ref[:i], ref[i+1:], true
}

// field converts a config schema into the linter's field model, following
// references through the set. dir is the directory of the file being converted,
// so relative references resolve against it.
func (s *schemaSet) field(doc *jsonSchema, dir string, seen []string, depth int) *schema.Field {
	if doc == nil || depth > maxFieldDepth {
		return nil
	}

	out := &schema.Field{
		Type: fieldType(doc),
		Doc:  firstLine(doc.Description),
		Enum: enumValues(doc.Enum),
	}

	if doc.Ref != "" {
		// A reference already on this path would expand forever. Stanza
		// operators genuinely recurse, so the tree stops here and stays open
		// rather than reporting the keys below it as unknown.
		key := refKey(doc.Ref, dir)
		if slices.Contains(seen, key) {
			out.Type = typeMap
			out.Open = true

			return out
		}

		return s.merge(out, s.field(s.lookup(doc.Ref, dir), refDir(doc.Ref, dir), append(seen, key), depth+1))
	}

	for _, one := range doc.AllOf {
		out = s.merge(out, s.field(one, dir, seen, depth+1))
	}

	if doc.Items != nil {
		out.Type = typeList
		out.Children = map[string]*schema.Field{"item": s.field(doc.Items, dir, seen, depth+1)}
	}

	for name, prop := range doc.Properties {
		child := s.field(prop, dir, seen, depth+1)
		if child == nil {
			continue
		}

		if out.Children == nil {
			out.Children = map[string]*schema.Field{}
		}

		out.Children[name] = child
	}

	settleShape(out, doc)

	return out
}

// settleShape decides what an object turned out to be, once its children are
// known.
//
// An object with no keys of its own is two different things, and only one of
// them is a mapping. With additionalProperties it is a map of arbitrary keys:
// free-form, but still a mapping, so it keeps the type and stays worth
// checking.
//
// Without, it states nothing at all. That is what upstream's config schema
// renders a Go `any` as, and the resource processor's attributes[].value is one
// -- it takes a plain string. Typing that as a map made the linter demand a
// mapping and report `value: "log"`, which is how the processor is ordinarily
// used, as an error. So it is left unconstrained, which is what the empty type
// already means everywhere else: nothing is known, so nothing is checked.
//
// Either way the keys under it stay open, or a map whose children were never
// expanded -- from a cycle, or a module publishing no schema -- has every key
// below it read as unknown.
func settleShape(out *schema.Field, doc *jsonSchema) {
	if len(out.Children) > 0 && out.Type == "" {
		out.Type = typeMap
	}

	if doc.AdditionalProperties != nil {
		out.Open = true
	}

	if out.Type == typeMap && len(out.Children) == 0 {
		out.Open = true

		if doc.AdditionalProperties == nil {
			out.Type = ""
		}
	}
}

// merge folds a referenced or composed schema into the one referring to it.
// The referring schema wins, since it is the more specific statement.
func (s *schemaSet) merge(into, from *schema.Field) *schema.Field {
	if from == nil {
		return into
	}

	if into.Type == "" {
		into.Type = from.Type
	}

	if into.Doc == "" {
		into.Doc = from.Doc
	}

	if len(into.Enum) == 0 {
		into.Enum = from.Enum
	}

	if into.ExtensionRef == "" {
		into.ExtensionRef = from.ExtensionRef
	}

	if from.Open {
		into.Open = true
	}

	for name, child := range from.Children {
		if into.Children == nil {
			into.Children = map[string]*schema.Field{}
		}

		if _, taken := into.Children[name]; !taken {
			into.Children[name] = child
		}
	}

	return into
}

// refKey identifies a reference target independently of how it was spelled, so
// a cycle is detected however it is reached.
func refKey(ref, fromDir string) string {
	where, name, found := lastDot(ref)
	if !found {
		return fromDir + "." + ref
	}

	if strings.HasPrefix(where, "./") || strings.HasPrefix(where, "../") {
		return path.Join(fromDir, where) + "." + name
	}

	return where + "." + name
}

// refDir is the directory a reference points into, which is what any relative
// reference inside the target resolves against.
func refDir(ref, fromDir string) string {
	where, _, found := lastDot(ref)
	if !found {
		return fromDir
	}

	switch {
	case strings.HasPrefix(where, "./"), strings.HasPrefix(where, "../"):
		return path.Join(fromDir, where)
	case strings.HasPrefix(where, "/"):
		return strings.TrimPrefix(where, "/")
	default:
		return where
	}
}

// fieldType maps a JSON Schema type onto the linter's. An unrecognised type is
// left unconstrained rather than guessed at.
func fieldType(doc *jsonSchema) string {
	if doc.Format == typeDuration {
		return typeDuration
	}

	switch doc.Type {
	case "object":
		return typeMap
	case "array":
		return typeList
	case "string":
		return typeString
	case "integer":
		return typeInt
	case "number":
		return typeFloat
	case "boolean":
		return typeBool
	default:
		return ""
	}
}

func enumValues(nodes []yaml.Node) []string {
	if len(nodes) == 0 {
		return nil
	}

	out := make([]string, 0, len(nodes))

	for _, n := range nodes {
		if n.Value != "" {
			out = append(out, n.Value)
		}
	}

	return out
}

// firstLine keeps a description short enough to sit in a diagnostic hint.
func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")

	return strings.TrimSpace(s)
}

// attachFields folds the schema upstream published for a component into
// whatever was read from its sources.
//
// Upstream's generator is explicitly a temporary one, and it is lossy in a way
// that matters: an embedded field carrying a name, such as
// `configretry.BackOffConfig \`mapstructure:"retry_on_failure"\“, is emitted as
// an allOf, which merges those settings into the parent and loses the key they
// actually live under. Trusting it alone reports a valid "retry_on_failure:"
// as an unknown setting. So the sources decide the shape and this only adds to
// it -- descriptions and enums, which the sources cannot supply, and any key
// the source pass could not reach.
func attachFields(cat *schema.Schema, set *schemaSet) int {
	n := 0

	for _, byType := range cat.Components {
		for _, comp := range byType {
			doc, ok := set.byKey[comp.Module]
			if !ok {
				continue
			}

			f := set.field(doc, comp.Module, nil, 0)
			if f == nil || len(f.Children) == 0 {
				continue
			}

			if comp.Fields == nil {
				comp.Fields = f
			} else {
				enrich(comp.Fields, f)
			}

			n++
		}
	}

	return n
}

// settleOpenness decides whether the secondary closes a mapping the primary
// left open.
//
// A published schema states a component's sections outright, the ones the Go
// sources can only decode by hand included, so where it describes a section no
// tag names it is the better answer about whether that mapping is closed. But
// only where it does. Upstream derives these files from the same mapstructure
// tags, so a component that reads a section by hand is missing it from both:
// hostmetrics has published a schema since v0.145.0 that lists root_path and
// metadata_collection_interval and says nothing of scrapers until v0.154.0.
// Closing on that puts the false positive back, and puts it back for exactly
// the releases people are still running.
//
// So a secondary settles openness only where it says something the primary did
// not already know. A key the sources never resolved is the evidence that this
// schema describes more than the tags do; one that lists only keys they did
// resolve has not looked into the hand-read section either, and the open
// mapping stands.
func settleOpenness(primary, secondary *schema.Field) {
	if secondary.Open {
		primary.Open = true

		return
	}

	if !primary.Open || len(secondary.Children) == 0 {
		return
	}

	for name := range secondary.Children {
		if _, known := primary.Children[name]; !known {
			primary.Open = false

			return
		}
	}
}

// enrich adds to a field schema without ever taking away from it. The primary
// states the shape; the secondary contributes what it knows and nothing else,
// because a key dropped here becomes a false report against a valid config.
func enrich(primary, secondary *schema.Field) {
	if primary.Doc == "" {
		primary.Doc = secondary.Doc
	}

	if len(primary.Enum) == 0 {
		primary.Enum = secondary.Enum
	}

	if primary.ExtensionRef == "" {
		primary.ExtensionRef = secondary.ExtensionRef
	}

	settleOpenness(primary, secondary)

	for name, child := range secondary.Children {
		if primary.Children == nil {
			primary.Children = map[string]*schema.Field{}
		}

		if existing, ok := primary.Children[name]; ok {
			enrich(existing, child)

			continue
		}

		primary.Children[name] = child
	}
}
