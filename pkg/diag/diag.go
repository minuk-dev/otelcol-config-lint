// Package diag defines the diagnostics produced by the linter.
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Severity classifies how serious a diagnostic is.
type Severity string

const (
	// Error marks a config that the collector will reject or that is certainly broken.
	Error Severity = "error"
	// Warning marks a config that loads but is very likely a mistake.
	Warning Severity = "warning"
	// Info marks a stylistic or informational remark.
	Info Severity = "info"
	// Off disables a rule entirely.
	Off Severity = "off"
)

// ParseSeverity converts a textual severity into its typed form.
func ParseSeverity(s string) (Severity, error) {
	switch Severity(strings.ToLower(strings.TrimSpace(s))) {
	case Error:
		return Error, nil
	case Warning:
		return Warning, nil
	case Info:
		return Info, nil
	case Off:
		return Off, nil
	default:
		return "", fmt.Errorf("unknown severity %q (want error, warning, info or off)", s)
	}
}

// rank orders severities from most to least serious. Off sorts last.
func (s Severity) rank() int {
	switch s {
	case Error:
		return 0
	case Warning:
		return 1
	case Info:
		return 2
	default:
		return 3
	}
}

// AtLeast reports whether s is at least as serious as min.
func (s Severity) AtLeast(min Severity) bool { return s.rank() <= min.rank() }

// Position is a location inside a config file. Lines and columns are 1-based;
// a zero Line means the diagnostic could not be anchored to a specific node.
type Position struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// String renders the position in the conventional file:line:col form.
func (p Position) String() string {
	switch {
	case p.Line == 0:
		return p.File
	case p.Column == 0:
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	default:
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
	}
}

// Diagnostic is a single finding reported against a config file.
type Diagnostic struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Position Position `json:"position"`
	// Path is the dotted YAML path the finding applies to, e.g.
	// "service.pipelines.traces.receivers[0]".
	Path string `json:"path,omitempty"`
	// Hint is an optional suggested fix shown below the message.
	Hint string `json:"hint,omitempty"`
}

// Diagnostics is an ordered collection of findings.
type Diagnostics []Diagnostic

// Sort orders diagnostics by file, then position, then rule name so that output
// is stable across runs.
func (d Diagnostics) Sort() {
	sort.SliceStable(d, func(i, j int) bool {
		a, b := d[i], d[j]
		if a.Position.File != b.Position.File {
			return a.Position.File < b.Position.File
		}
		if a.Position.Line != b.Position.Line {
			return a.Position.Line < b.Position.Line
		}
		if a.Position.Column != b.Position.Column {
			return a.Position.Column < b.Position.Column
		}
		return a.Rule < b.Rule
	})
}

// Count returns the number of diagnostics with the given severity.
func (d Diagnostics) Count(s Severity) int {
	n := 0
	for _, x := range d {
		if x.Severity == s {
			n++
		}
	}
	return n
}

// HasErrors reports whether any diagnostic is an error.
func (d Diagnostics) HasErrors() bool { return d.Count(Error) > 0 }
