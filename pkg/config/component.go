// Package config parses OpenTelemetry Collector config files into a structure
// that keeps every YAML node around, so diagnostics can point at real lines.
package config

import (
	"strings"
)

// Kind is a class of collector component.
type Kind string

// The component kinds a collector config can declare.
const (
	KindReceiver  Kind = "receiver"
	KindProcessor Kind = "processor"
	KindExporter  Kind = "exporter"
	KindExtension Kind = "extension"
	KindConnector Kind = "connector"
)

// Kinds lists every component kind in declaration order.
var Kinds = []Kind{KindReceiver, KindProcessor, KindExporter, KindExtension, KindConnector}

// Section is the top-level config key that declares components of a kind.
func (k Kind) Section() string { return string(k) + "s" }

// SectionKind maps a top-level config key to the kind it declares.
var SectionKind = map[string]Kind{
	"receivers":  KindReceiver,
	"processors": KindProcessor,
	"exporters":  KindExporter,
	"extensions": KindExtension,
	"connectors": KindConnector,
}

// Signal is a telemetry type carried by a pipeline.
type Signal string

// The signals a pipeline can carry.
const (
	SignalTraces   Signal = "traces"
	SignalMetrics  Signal = "metrics"
	SignalLogs     Signal = "logs"
	SignalProfiles Signal = "profiles"
)

// Signals lists every known pipeline signal.
var Signals = []Signal{SignalTraces, SignalMetrics, SignalLogs, SignalProfiles}

// ID identifies a configured component instance, e.g. "otlp" or "otlp/internal".
type ID struct {
	Type string
	Name string
}

// ParseID splits a "type[/name]" key into its parts.
func ParseID(s string) ID {
	typ, name, found := strings.Cut(s, "/")
	if !found {
		return ID{Type: s}
	}
	return ID{Type: typ, Name: name}
}

// String renders the ID back into its config key form.
func (id ID) String() string {
	if id.Name == "" {
		return id.Type
	}
	return id.Type + "/" + id.Name
}
