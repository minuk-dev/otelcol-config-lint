package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// File is a parsed collector config.
type File struct {
	// Path is the file name diagnostics are reported against.
	Path string
	// Root is the top-level mapping node, or nil for an empty document.
	Root *yaml.Node

	// Sections holds the declared components of each kind, in file order.
	Sections map[Kind]*Section
	// Service is the "service" block; never nil, but Node is nil when absent.
	Service *Service
	// Unknown holds top-level keys that are not part of the config schema.
	Unknown []Entry
	// DuplicateKeys records mapping keys that appear more than once.
	DuplicateKeys []Entry
}

// Section is one component-declaring top-level block, e.g. "receivers".
type Section struct {
	Kind Kind
	// KeyNode and Node are the section key and its mapping value.
	KeyNode, Node *yaml.Node
	Components    []Component
}

// Component is a single configured component instance.
type Component struct {
	ID   ID
	Kind Kind
	// KeyNode is the "otlp/name" key; ValueNode is its settings mapping,
	// which is a null node when the component is declared with no settings.
	KeyNode, ValueNode *yaml.Node
}

// Service is the "service" block of a config.
type Service struct {
	KeyNode, Node *yaml.Node
	// Extensions are the entries of service.extensions.
	Extensions []Ref
	// ExtensionsNode is the sequence node, nil when the key is absent.
	ExtensionsNode *yaml.Node
	Pipelines      []Pipeline
	PipelinesNode  *yaml.Node
	// Telemetry is the "telemetry" block. Its Node is nil when the key is
	// absent, so a rule reading it needs no guard of its own.
	Telemetry Telemetry
	Unknown   []Entry
}

// Telemetry is the "service.telemetry" block: how the collector reports on
// itself. It is parsed one level further than the block above -- each signal,
// and that signal's keys with their nodes -- which is as deep as a rule reads.
type Telemetry struct {
	KeyNode, Node *yaml.Node
	// Logs, Metrics and Traces are the signal blocks, nil when the key is
	// absent.
	Logs, Metrics, Traces *TelemetrySignal
	// Other holds the keys that are none of those, "resource" among them.
	// Nothing here is called unknown: which keys telemetry accepts is a rule's
	// judgement, and this is the parser.
	Other []Entry
}

// TelemetrySignal is one signal's block inside service.telemetry, e.g.
// service.telemetry.metrics. Its settings are kept flat, each with the node a
// finding is positioned by.
type TelemetrySignal struct {
	Key           string
	KeyNode, Node *yaml.Node
	Settings      []Entry
}

// Setting returns one setting of the block. A nil block has none, so a rule
// reading a key of a signal the config never wrote does not have to ask
// whether the signal is there first.
func (t *TelemetrySignal) Setting(key string) (Entry, bool) {
	if t == nil {
		return Entry{}, false
	}

	for _, e := range t.Settings {
		if e.Key == key {
			return e, true
		}
	}

	return Entry{}, false
}

// Pipeline is one entry of service.pipelines.
type Pipeline struct {
	// Key is the raw pipeline key, e.g. "traces/internal".
	Key string
	// Signal is the pipeline's telemetry type, derived from Key.
	Signal Signal
	// Name is the optional part after the slash.
	Name string

	KeyNode, Node *yaml.Node

	Receivers, Processors, Exporters []Ref
	// The sequence nodes for each list; nil when the key is absent.
	ReceiversNode, ProcessorsNode, ExportersNode *yaml.Node
	Unknown                                      []Entry
}

// Refs returns the pipeline's component references of the given kind.
func (p Pipeline) Refs(k Kind) []Ref {
	switch k {
	case KindReceiver:
		return p.Receivers
	case KindProcessor:
		return p.Processors
	case KindExporter:
		return p.Exporters
	default:
		return nil
	}
}

// Ref is a reference to a component from inside the service block.
type Ref struct {
	ID   ID
	Node *yaml.Node
	// Path is the dotted YAML path of the reference, from the root of the
	// document rather than from the service block: a pipeline's first exporter
	// is "service.pipelines.traces.exporters[0]". A caller reporting one has
	// nothing to prepend.
	Path string
}

// Entry is a generic key/value pair kept for reporting.
type Entry struct {
	Key           string
	KeyNode, Node *yaml.Node
	Path          string
}

// Pos converts a YAML node into a diagnostic position within the file.
func (f *File) Pos(n *yaml.Node) diag.Position {
	if n == nil {
		return diag.Position{File: f.Path, Line: 0, Column: 0}
	}

	return diag.Position{File: f.Path, Line: n.Line, Column: n.Column}
}

// Component looks up a declared component by kind and ID.
func (f *File) Component(k Kind, id ID) (Component, bool) {
	s := f.Sections[k]
	if s == nil {
		return Component{}, false
	}

	for _, c := range s.Components {
		if c.ID == id {
			return c, true
		}
	}

	return Component{}, false
}

// ParseFile reads and parses the config at path. A nil fsys reads the real
// filesystem.
func ParseFile(fsys afero.Fs, path string) (*File, error) {
	if fsys == nil {
		fsys = afero.NewOsFs()
	}

	src, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return Parse(path, src)
}

// Parse parses config source that was read from path.
//
// A syntax error is returned as a *SyntaxError so callers can report it as a
// diagnostic instead of a hard failure.
func Parse(path string, src []byte) (*File, error) {
	var doc yaml.Node

	err := yaml.Unmarshal(src, &doc)
	if err != nil {
		return nil, syntaxError(path, err)
	}

	f := &File{Path: path, Sections: map[Kind]*Section{}, Service: &Service{}}
	if len(doc.Content) == 0 {
		return f, nil // empty document
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &SyntaxError{Path: path, Line: root.Line, Column: root.Column,
			Msg: "config root must be a mapping"}
	}

	f.Root = root

	// Duplicates are collected from the document as written, before anything
	// is merged into it: a key the config writes itself replaces one a merge
	// supplies, and that is the merge key's purpose rather than a key declared
	// twice.
	f.DuplicateKeys = collectDuplicates(root, "")

	resolve(root)

	for _, e := range entries(root, "") {
		kind, isSection := SectionKind(e.Key)

		switch {
		case isSection:
			f.Sections[kind] = parseSection(kind, e)
		case e.Key == "service":
			f.Service = parseService(e)
		default:
			f.Unknown = append(f.Unknown, e)
		}
	}

	return f, nil
}

func parseSection(k Kind, e Entry) *Section {
	s := &Section{Kind: k, KeyNode: e.KeyNode, Node: e.Node}
	for _, c := range entries(e.Node, e.Path) {
		s.Components = append(s.Components, Component{
			ID: ParseID(c.Key), Kind: k, KeyNode: c.KeyNode, ValueNode: c.Node,
		})
	}

	return s
}

func parseService(e Entry) *Service {
	s := &Service{KeyNode: e.KeyNode, Node: e.Node}
	for _, sub := range entries(e.Node, e.Path) {
		switch sub.Key {
		case "extensions":
			s.ExtensionsNode = sub.Node
			s.Extensions = refs(sub.Node, sub.Path)
		case "telemetry":
			s.Telemetry = parseTelemetry(sub)
		case "pipelines":
			s.PipelinesNode = sub.Node
			for _, p := range entries(sub.Node, sub.Path) {
				s.Pipelines = append(s.Pipelines, parsePipeline(p))
			}
		default:
			s.Unknown = append(s.Unknown, sub)
		}
	}

	return s
}

func parseTelemetry(e Entry) Telemetry {
	tel := Telemetry{KeyNode: e.KeyNode, Node: e.Node}

	for _, sub := range entries(e.Node, e.Path) {
		switch sub.Key {
		case "logs":
			tel.Logs = telemetrySignal(sub)
		case "metrics":
			tel.Metrics = telemetrySignal(sub)
		case "traces":
			tel.Traces = telemetrySignal(sub)
		default:
			tel.Other = append(tel.Other, sub)
		}
	}

	return tel
}

// telemetrySignal reads one signal block. A signal written as anything but a
// mapping has no settings, which is what a rule reading a key of it sees.
func telemetrySignal(e Entry) *TelemetrySignal {
	return &TelemetrySignal{
		Key: e.Key, KeyNode: e.KeyNode, Node: e.Node,
		Settings: entries(e.Node, e.Path),
	}
}

func parsePipeline(e Entry) Pipeline {
	signal, name, _ := strings.Cut(e.Key, "/")

	p := Pipeline{
		Key: e.Key, Signal: Signal(signal), Name: name,
		KeyNode: e.KeyNode, Node: e.Node,
	}
	for _, sub := range entries(e.Node, e.Path) {
		switch sub.Key {
		case "receivers":
			p.ReceiversNode, p.Receivers = sub.Node, refs(sub.Node, sub.Path)
		case "processors":
			p.ProcessorsNode, p.Processors = sub.Node, refs(sub.Node, sub.Path)
		case "exporters":
			p.ExportersNode, p.Exporters = sub.Node, refs(sub.Node, sub.Path)
		default:
			p.Unknown = append(p.Unknown, sub)
		}
	}

	return p
}

// refs reads a sequence of component IDs, e.g. the value of "receivers:".
func refs(n *yaml.Node, path string) []Ref {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}

	out := make([]Ref, 0, len(n.Content))
	for i, item := range n.Content {
		if item.Kind != yaml.ScalarNode {
			continue
		}

		out = append(out, Ref{
			ID:   ParseID(item.Value),
			Node: item,
			Path: fmt.Sprintf("%s[%d]", path, i),
		})
	}

	return out
}

// mappingStride is the step between keys in a yaml.Node mapping, whose Content
// alternates key, value, key, value.
const mappingStride = 2

// entries walks a mapping node's key/value pairs. A nil or non-mapping node
// yields nothing, so callers do not have to guard every access.
func entries(n *yaml.Node, parentPath string) []Entry {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}

	out := make([]Entry, 0, len(n.Content)/mappingStride)
	for i := 0; i+1 < len(n.Content); i += mappingStride {
		k, v := n.Content[i], n.Content[i+1]
		out = append(out, Entry{Key: k.Value, KeyNode: k, Node: v, Path: join(parentPath, k.Value)})
	}

	return out
}

// collectDuplicates walks the whole document for mapping keys declared more
// than once. Only the later occurrences are returned, since YAML keeps the last
// one and it is the earlier value that silently disappears.
func collectDuplicates(n *yaml.Node, path string) []Entry {
	var out []Entry

	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for _, e := range entries(n, path) {
			if seen[e.Key] {
				out = append(out, e)
			}

			seen[e.Key] = true
			out = append(out, collectDuplicates(e.Node, e.Path)...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, collectDuplicates(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	default:
		// Scalars, aliases and the document node have no keys of their own.
	}

	return out
}

func join(parent, key string) string {
	if parent == "" {
		return key
	}

	return parent + "." + key
}

// SyntaxError is a YAML parse failure with a position, when one is available.
//
// One parse can fail more than once: a yaml.TypeError carries an entry for
// every value in the document that could not be unmarshalled. The first is
// this error's own position and message and [SyntaxError.Rest] holds the
// others, so a config with three type errors reports three rather than
// handing them out one re-run at a time.
type SyntaxError struct {
	Path         string
	Line, Column int
	Msg          string
	// Rest is the further failures from the same parse, in the order the
	// parser reported them. It is empty for a parse that failed once, which
	// is every plain syntax error.
	Rest []Fault
}

// Fault is one parse failure, without the file it was found in: the
// *SyntaxError carrying it already names that.
type Fault struct {
	Line, Column int
	Msg          string
}

func (e *SyntaxError) Error() string {
	msgs := make([]string, 0, len(e.Rest)+1)
	for _, f := range e.faults() {
		p := diag.Position{File: e.Path, Line: f.Line, Column: f.Column}
		msgs = append(msgs, p.String()+": "+f.Msg)
	}

	return strings.Join(msgs, "; ")
}

// Diagnostic renders the first failure as a linter finding.
func (e *SyntaxError) Diagnostic() diag.Diagnostic {
	return e.Diagnostics()[0]
}

// Diagnostics renders every failure the parse reported as a linter finding.
func (e *SyntaxError) Diagnostics() diag.Diagnostics {
	out := make(diag.Diagnostics, 0, len(e.Rest)+1)
	for _, f := range e.faults() {
		out = append(out, diag.Diagnostic{
			Rule:     "yaml-syntax",
			Severity: diag.Error,
			Message:  f.Msg,
			Position: diag.Position{File: e.Path, Line: f.Line, Column: f.Column},
		})
	}

	return out
}

// faults is the error as the flat list it stands for, first failure included.
func (e *SyntaxError) faults() []Fault {
	return append([]Fault{{Line: e.Line, Column: e.Column, Msg: e.Msg}}, e.Rest...)
}

// unlocated is added to a message the parser reported no line for, so a
// diagnostic that lands at the top of the file says why it is there rather
// than reading as a finding about the first line.
const unlocated = " (the parser reported no line for this)"

// syntaxError converts a yaml.v3 error into a *SyntaxError, keeping every
// failure it reported and recovering the line numbers that yaml.v3 only
// reports inside the message text.
func syntaxError(path string, err error) error {
	var te *yaml.TypeError

	msgs := []string{err.Error()}
	if errors.As(err, &te) && len(te.Errors) > 0 {
		msgs = te.Errors
	}

	first := fault(msgs[0])

	out := &SyntaxError{Path: path, Line: first.Line, Column: first.Column, Msg: first.Msg}
	for _, msg := range msgs[1:] {
		out.Rest = append(out.Rest, fault(msg))
	}

	return out
}

// fault splits one yaml.v3 message into the position it names and the rest of
// what it says.
func fault(msg string) Fault {
	msg = strings.TrimPrefix(msg, "yaml: ")

	rest, ok := strings.CutPrefix(msg, "line ")
	if !ok {
		return Fault{Msg: msg + unlocated}
	}

	num, tail, found := strings.Cut(rest, ":")
	if !found {
		return Fault{Msg: msg + unlocated}
	}

	line, convErr := strconv.Atoi(strings.TrimSpace(num))
	if convErr != nil {
		return Fault{Msg: msg + unlocated}
	}

	return Fault{Line: line, Msg: strings.TrimSpace(tail)}
}
