package schemagen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Releases before v0.150.0 predate the config.schema.yaml files upstream now
// publishes, so their field schemas are read from the Go sources instead: a
// component's config is a struct whose mapstructure tags name the settings it
// accepts. Backporting a newer release's schema was the alternative and is not
// sound -- settings are removed as well as added, so an older config would be
// told a key it legitimately uses is unknown.

// maxAliasHops bounds how far an alias chain is followed.
const maxAliasHops = 8

// componentIDType is the type a setting that names another component decodes
// into. Inside a component's own config the component it names is an
// extension: nothing else is addressable from there. It is what makes an
// extension reference something the sources state rather than something this
// generator has to be told about one setting at a time.
const componentIDType = coreModuleRoot + "/component.ID"

// goType is a type declaration found in the sources, kept with the imports of
// the file that declared it so its own field types can be resolved.
type goType struct {
	strukt *ast.StructType
	// under is what a named non-struct type is defined as, kept as written so
	// an alias to another package's type can be resolved like any reference.
	under ast.Expr
	// pkg is the import path of the package that declared this type, which is
	// what an unqualified reference inside it resolves against.
	pkg     string
	imports map[string]string // local package name -> import path
}

// goIndex holds every type declaration across the distribution's modules, keyed
// by "<import path>.<TypeName>", which is how one package names another's type.
type goIndex struct {
	byName map[string]*goType
	// textual holds the types that decode themselves from text. Their
	// underlying kind says nothing about what a config writes: an int with an
	// UnmarshalText method is spelled as a word, the way the debug exporter's
	// verbosity is "detailed" and not 2.
	textual map[string]bool
	fset    *token.FileSet
}

func newGoIndex() *goIndex {
	return &goIndex{
		byName:  map[string]*goType{},
		textual: map[string]bool{},
		fset:    token.NewFileSet(),
	}
}

// add parses one Go file and records the type declarations in it. Files that
// fail to parse are skipped: the archives contain generated and build-tagged
// sources that need not compile in isolation.
func (g *goIndex) add(importPath string, src []byte) {
	file, err := parser.ParseFile(g.fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return
	}

	imports := fileImports(file)

	for _, decl := range file.Decls {
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc {
			g.addMethod(importPath, fn)

			continue
		}

		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			key := importPath + "." + typeSpec.Name.Name

			//nolint:exhaustruct // one of strukt or under is set, never both
			switch t := typeSpec.Type.(type) {
			case *ast.StructType:
				g.byName[key] = &goType{strukt: t, pkg: importPath, imports: imports}
			default:
				g.byName[key] = &goType{under: typeSpec.Type, pkg: importPath, imports: imports}
			}
		}
	}
}

// addMethod records a type that decodes itself from text, which is what makes
// it a string in a config however it is declared in Go.
func (g *goIndex) addMethod(importPath string, method *ast.FuncDecl) {
	if method.Recv == nil || len(method.Recv.List) == 0 {
		return
	}

	switch method.Name.Name {
	case "UnmarshalText", "UnmarshalYAML", "UnmarshalJSON":
	default:
		return
	}

	name := exprName(unwrap(method.Recv.List[0].Type))
	if name == "" {
		return
	}

	g.textual[importPath+"."+name] = true
}

// fileImports maps the name a file refers to each import by onto its path.
func fileImports(file *ast.File) map[string]string {
	out := map[string]string{}

	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}

		out[name] = p
	}

	return out
}

// fields builds the field schema for a config struct, following embedded and
// referenced types through the index.
func (g *goIndex) fields(key string, seen []string, depth int) *schema.Field {
	if depth > maxFieldDepth || slices.Contains(seen, key) {
		return nil
	}

	decl := g.deref(key)
	if decl == nil || decl.strukt == nil {
		return nil
	}

	out := &schema.Field{Type: typeMap}

	for _, f := range decl.strukt.Fields.List {
		g.addStructField(out, decl, f, append(seen, key), depth)
	}

	if len(out.Children) == 0 {
		return nil
	}

	return out
}

// addStructField folds one struct field into the schema being built. A squashed
// field contributes its own children rather than a key of its own, which is how
// the shared config structs are spliced in.
func (g *goIndex) addStructField(out *schema.Field, decl *goType, f *ast.Field, seen []string, depth int) {
	name, squash, ok := mapstructureName(f)
	if !ok {
		return
	}

	if squash {
		nested := g.fields(g.resolve(decl, f.Type), seen, depth+1)
		if nested != nil {
			mergeChildren(out, nested)
		}

		return
	}

	if out.Children == nil {
		out.Children = map[string]*schema.Field{}
	}

	child := g.field(decl, f.Type, firstLine(docText(f)), seen, depth)
	if g.resolve(decl, f.Type) == componentIDType {
		child.ExtensionRef = extensionRole(name)
	}

	out.Children[name] = child
}

// extensionRole says what the extension a setting names is used for. The type
// is what identifies the reference, and it is component.ID either way, so
// which kind of extension it is comes from the key it is written under. A key
// this does not recognise is still a reference; it just reads as a plain
// extension in a diagnostic instead of a storage or an auth one.
func extensionRole(key string) string {
	switch key {
	case "storage":
		return schema.RoleStorage
	case "authenticator":
		return schema.RoleAuth
	default:
		return schema.RoleExtension
	}
}

// field maps a Go type onto a schema field, resolving named types through the
// index. A type that cannot be resolved -- a third-party config such as
// Prometheus's own -- becomes an open map, so its keys are left unchecked
// rather than reported as unknown.
func (g *goIndex) field(decl *goType, expr ast.Expr, doc string, seen []string, depth int) *schema.Field {
	out := &schema.Field{Doc: doc}

	switch t := unwrap(expr).(type) {
	case *ast.ArrayType:
		out.Type = typeList
		out.Children = map[string]*schema.Field{"item": g.field(decl, t.Elt, "", seen, depth+1)}
	case *ast.MapType:
		out.Type = typeMap
		out.Open = true
	case *ast.InterfaceType:
		// An any-typed setting accepts whatever the component makes of it.
	default:
		out.Type = g.scalar(decl, expr)
		if out.Type == typeMap {
			nested := g.fields(g.resolve(decl, expr), seen, depth+1)
			if nested == nil {
				out.Open = true
			} else {
				out.Children = nested.Children
			}
		}
	}

	return out
}

// scalar names the field type for a non-composite Go type, resolving a named
// type to whatever it is defined as. An unknown named type is treated as a
// nested struct.
func (g *goIndex) scalar(decl *goType, expr ast.Expr) string {
	name := exprName(unwrap(expr))
	if name == "time.Duration" {
		return typeDuration
	}

	if kind := basicKind(name); kind != "" {
		return kind
	}

	if name == "" {
		return ""
	}

	key := g.resolve(decl, expr)
	if g.textual[key] {
		return typeString
	}

	under := g.deref(key)
	if under != nil && under.strukt == nil && under.under != nil {
		if exprName(under.under) == "time.Duration" {
			return typeDuration
		}

		return basicKind(exprName(under.under))
	}

	return typeMap
}

// deref follows alias chains to the declaration that actually defines a type,
// so "type QueueConfig = internal.QueueConfig" reaches the struct behind it.
func (g *goIndex) deref(key string) *goType {
	for range maxAliasHops {
		decl, ok := g.byName[key]
		if !ok {
			return nil
		}

		if decl.strukt != nil || decl.under == nil {
			return decl
		}

		next := g.resolve(decl, decl.under)
		if next == "" || next == key {
			return decl
		}

		// A chain ending at a builtin runs off the index; the last named type
		// is what says the underlying kind, so stop on it rather than losing it.
		if _, indexed := g.byName[next]; !indexed {
			return decl
		}

		key = next
	}

	return nil
}

// resolve turns a type expression into the index key it is stored under.
func (g *goIndex) resolve(decl *goType, expr ast.Expr) string {
	switch named := unwrap(expr).(type) {
	case *ast.Ident:
		return decl.pkg + "." + named.Name
	case *ast.SelectorExpr:
		pkg, ok := named.X.(*ast.Ident)
		if !ok {
			return ""
		}

		return decl.imports[pkg.Name] + "." + named.Sel.Name
	default:
		return ""
	}
}

// unwrap strips the wrappers that do not change what a setting accepts:
// pointers, and the generic containers upstream uses for optional settings.
func unwrap(expr ast.Expr) ast.Expr {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return unwrap(t.X)
	case *ast.IndexExpr: // configoptional.Optional[T]
		return unwrap(t.Index)
	default:
		return expr
	}
}

func exprName(expr ast.Expr) string {
	switch named := expr.(type) {
	case *ast.Ident:
		return named.Name
	case *ast.SelectorExpr:
		pkg, ok := named.X.(*ast.Ident)
		if !ok {
			return ""
		}

		return pkg.Name + "." + named.Sel.Name
	case *ast.StarExpr:
		return exprName(named.X)
	default:
		return ""
	}
}

// basicKind names the field type for a Go builtin. Matching is exact: a prefix
// test would read "internal.QueueConfig" as an integer.
func basicKind(name string) string {
	switch name {
	case "string":
		return typeString
	case "bool":
		return typeBool
	case "float32", "float64":
		return typeFloat
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
		return typeInt
	default:
		return ""
	}
}

// mapstructureName reads the setting name a struct field is decoded from. A
// field with no tag, or tagged "-", is not configurable. An embedded field with
// no name is squashed, which is also spelled explicitly as ",squash".
func mapstructureName(f *ast.Field) (string, bool, bool) {
	tag := ""

	if f.Tag != nil {
		unquoted, err := strconv.Unquote(f.Tag.Value)
		if err == nil {
			tag = reflect.StructTag(unquoted).Get("mapstructure")
		}
	}

	name, opts, _ := strings.Cut(tag, ",")

	if strings.Contains(opts, "squash") {
		return "", true, true
	}

	if name == "-" || name == "" {
		return "", false, false
	}

	if len(f.Names) > 0 && !f.Names[0].IsExported() {
		return "", false, false
	}

	return name, false, true
}

// docText is the comment on a struct field, preferring the one above it.
func docText(f *ast.Field) string {
	if f.Doc != nil {
		return f.Doc.Text()
	}

	if f.Comment != nil {
		return f.Comment.Text()
	}

	return ""
}

// mergeChildren splices a squashed struct's keys into its parent, leaving any
// the parent already declares alone.
func mergeChildren(into, from *schema.Field) {
	for name, child := range from.Children {
		if into.Children == nil {
			into.Children = map[string]*schema.Field{}
		}

		if _, taken := into.Children[name]; !taken {
			into.Children[name] = child
		}
	}
}

// isConfigSource reports whether an archive entry is a Go file worth parsing.
// Tests declare no configuration, and the volume of them is most of the tree.
func isConfigSource(tarPath string) bool {
	base := path.Base(tarPath)

	return strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go")
}

// attachSourceFields gives a field schema to every component still without one,
// read from its Config struct. Components that already carry a schema from
// upstream keep it: it is generated from the same sources but with full type
// information, so it is the better answer where it exists.
func attachSourceFields(cat *schema.Schema, index *goIndex) int {
	n := 0

	for _, byType := range cat.Components {
		for _, comp := range byType {
			if comp.Fields != nil || comp.Module == "" {
				continue
			}

			f := index.fields(comp.Module+".Config", nil, 0)
			if f == nil {
				continue
			}

			comp.Fields = f
			n++
		}
	}

	return n
}
