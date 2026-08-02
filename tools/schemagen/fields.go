package main

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

// typeMap is the field type for a YAML mapping, spelled once.
const typeMap = "map"

// keysPerSchema is how many keys a schema is indexed under: its repository path
// and its module path.
const keysPerSchema = 2

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

// schemaSet holds every config schema found across the source archives, keyed
// both by repository path ("/receiver/filelogreceiver") and by module path
// ("go.opentelemetry.io/collector/config/confighttp"), because references use
// both spellings.
type schemaSet struct {
	byKey map[string]*jsonSchema
}

func newSchemaSet() *schemaSet { return &schemaSet{byKey: map[string]*jsonSchema{}} }

// add records a config schema under both spellings a reference may use. dir is
// the component directory inside the archive, e.g. "receiver/filelogreceiver".
func (s *schemaSet) add(src source, dir string, raw []byte) {
	var doc jsonSchema

	err := yaml.Unmarshal(raw, &doc)
	if err != nil {
		return
	}

	s.byKey["/"+dir] = &doc
	s.byKey[src.module+"/"+dir] = &doc
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
		return s.def("/"+fromDir, ref)
	}

	if strings.HasPrefix(where, "./") || strings.HasPrefix(where, "../") {
		return s.def("/"+path.Join(fromDir, where), name)
	}

	return s.def(where, name)
}

// count reports how many config schemas were collected, counting each file
// once rather than once per spelling it is keyed under.
func (s *schemaSet) count() int { return len(s.byKey) / keysPerSchema }

func (s *schemaSet) def(key, name string) *jsonSchema {
	owner, ok := s.byKey[key]
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
		out.Type = "list"
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

	if len(out.Children) > 0 && out.Type == "" {
		out.Type = typeMap
	}

	// A map that accepts arbitrary keys must not have them reported as unknown.
	if doc.AdditionalProperties != nil {
		out.Open = true
	}

	// A map whose keys were not expanded, because of a cycle or a reference
	// into something not published, has to stay open or every key under it
	// reads as unknown.
	if out.Type == typeMap && len(out.Children) == 0 {
		out.Open = true
	}

	return out
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
		return "/" + fromDir + "." + ref
	}

	if strings.HasPrefix(where, "./") || strings.HasPrefix(where, "../") {
		return "/" + path.Join(fromDir, where) + "." + name
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
	if doc.Format == "duration" {
		return "duration"
	}

	switch doc.Type {
	case "object":
		return typeMap
	case "array":
		return "list"
	case "string":
		return "string"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "boolean":
		return "bool"
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

// attachFields gives every component the field schema upstream published for
// it. A component whose directory has no config.schema.yaml keeps whatever an
// overlay supplies, since overlays are applied afterwards and are the only
// source for releases predating these files.
func attachFields(cat *schema.Schema, set *schemaSet) int {
	n := 0

	for _, byType := range cat.Components {
		for _, comp := range byType {
			dir := moduleDir(comp.Module)

			doc, ok := set.byKey[comp.Module]
			if !ok || dir == "" {
				continue
			}

			f := set.field(doc, dir, nil, 0)
			if f == nil || len(f.Children) == 0 {
				continue
			}

			comp.Fields = f
			n++
		}
	}

	return n
}

// moduleDir is the in-archive directory a module path corresponds to, which is
// what relative references inside its schema resolve against.
func moduleDir(module string) string {
	for _, src := range sources() {
		if rest, found := strings.CutPrefix(module, src.module+"/"); found {
			return rest
		}
	}

	return ""
}
