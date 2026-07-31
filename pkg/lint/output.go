package lint

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/minuk-dev/otel-collector-config-linter/pkg/diag"
)

// Formatter renders results as they arrive and writes a trailer at the end.
type Formatter interface {
	// Result renders one file's outcome.
	Result(Result) error
	// Finish writes any trailing output, such as a summary.
	Finish(Summary) error
}

// FormatterOptions configures the built-in formatters.
type FormatterOptions struct {
	// Verbose also reports files that passed.
	Verbose bool
	// Summary appends a count of the outcomes.
	Summary bool
	// Color enables ANSI colouring in the text formatter.
	Color bool
}

// NewFormatter returns the formatter for a name: text, json, junit, tap or
// github.
func NewFormatter(name string, w io.Writer, opts FormatterOptions) (Formatter, error) {
	switch name {
	case "", "text":
		return &textFormatter{w: w, opts: opts}, nil
	case "json":
		return &jsonFormatter{w: w, opts: opts}, nil
	case "junit":
		return &junitFormatter{w: w, opts: opts}, nil
	case "tap":
		return &tapFormatter{w: w, opts: opts}, nil
	case "github":
		return &githubFormatter{w: w, opts: opts}, nil
	default:
		return nil, fmt.Errorf("unknown output format %q (want text, json, junit, tap or github)", name)
	}
}

// ANSI colour codes, used only when output is a terminal.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiGreen  = "\033[32m"
)

type textFormatter struct {
	w    io.Writer
	opts FormatterOptions
}

func (f *textFormatter) color(code, s string) string {
	if !f.opts.Color {
		return s
	}
	return code + s + ansiReset
}

func (f *textFormatter) Result(r Result) error {
	switch r.Status {
	case Error:
		_, err := fmt.Fprintf(f.w, "%s: %s %s\n", r.Path, f.color(ansiRed, "error:"), r.Message())
		return err
	case Skipped:
		if !f.opts.Verbose {
			return nil
		}
		_, err := fmt.Fprintf(f.w, "%s: %s\n", r.Path, f.color(ansiDim, "skipped"))
		return err
	case Valid:
		if len(r.Diagnostics) == 0 {
			if !f.opts.Verbose {
				return nil
			}
			_, err := fmt.Fprintf(f.w, "%s: %s\n", r.Path, f.color(ansiGreen, "valid"))
			return err
		}
	}
	for _, d := range r.Diagnostics {
		if err := f.diagnostic(d); err != nil {
			return err
		}
	}
	return nil
}

func (f *textFormatter) diagnostic(d diag.Diagnostic) error {
	var code string
	switch d.Severity {
	case diag.Error:
		code = ansiRed
	case diag.Warning:
		code = ansiYellow
	default:
		code = ansiBlue
	}
	_, err := fmt.Fprintf(f.w, "%s: %s %s %s\n",
		f.color(ansiBold, d.Position.String()),
		f.color(code, string(d.Severity)+":"),
		d.Message,
		f.color(ansiDim, "["+d.Rule+"]"),
	)
	if err == nil && d.Hint != "" {
		_, err = fmt.Fprintf(f.w, "    %s %s\n", f.color(ansiDim, "hint:"), d.Hint)
	}
	return err
}

func (f *textFormatter) Finish(s Summary) error {
	if !f.opts.Summary {
		return nil
	}
	total := s.Valid + s.Invalid + s.Errors + s.Skipped
	_, err := fmt.Fprintf(f.w,
		"Summary: %d file(s) checked, %d valid, %d invalid, %d error(s), %d skipped (%d warning(s), %d info)\n",
		total, s.Valid, s.Invalid, s.Errors, s.Skipped, s.Warnings, s.Infos)
	return err
}

type jsonReport struct {
	Files   []jsonFile `json:"files"`
	Summary Summary    `json:"summary"`
}

type jsonFile struct {
	Filename    string           `json:"filename"`
	Status      Status           `json:"status"`
	Message     string           `json:"msg,omitempty"`
	Diagnostics diag.Diagnostics `json:"diagnostics,omitempty"`
}

type jsonFormatter struct {
	w    io.Writer
	opts FormatterOptions
	rep  jsonReport
}

func (f *jsonFormatter) Result(r Result) error {
	if r.Status == Valid && len(r.Diagnostics) == 0 && !f.opts.Verbose {
		return nil
	}
	f.rep.Files = append(f.rep.Files, jsonFile{
		Filename: r.Path, Status: r.Status, Message: r.Message(), Diagnostics: r.Diagnostics,
	})
	return nil
}

func (f *jsonFormatter) Finish(s Summary) error {
	f.rep.Summary = s
	if f.rep.Files == nil {
		f.rep.Files = []jsonFile{}
	}
	enc := json.NewEncoder(f.w)
	enc.SetIndent("", "  ")
	return enc.Encode(f.rep)
}

type junitFormatter struct {
	w     io.Writer
	opts  FormatterOptions
	cases []junitCase
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string         `xml:"name,attr"`
	ClassName string         `xml:"classname,attr"`
	Failures  []junitFailure `xml:"failure,omitempty"`
	Error     *junitFailure  `xml:"error,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

func (f *junitFormatter) Result(r Result) error {
	c := junitCase{Name: r.Path, ClassName: "otelcol-config-lint"}
	if r.Status == Error {
		c.Error = &junitFailure{Message: r.Message(), Type: "error"}
	}
	for _, d := range r.Diagnostics {
		if d.Severity != diag.Error {
			continue
		}
		c.Failures = append(c.Failures, junitFailure{
			Message: d.Message, Type: d.Rule,
			Text: d.Position.String() + ": " + d.Message,
		})
	}
	f.cases = append(f.cases, c)
	return nil
}

func (f *junitFormatter) Finish(s Summary) error {
	suite := junitSuite{
		Name:     "otelcol-config-lint",
		Tests:    len(f.cases),
		Failures: s.Invalid,
		Errors:   s.Errors,
		Cases:    f.cases,
	}
	if _, err := io.WriteString(f.w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(f.w)
	enc.Indent("", "  ")
	if err := enc.Encode(suite); err != nil {
		return err
	}
	_, err := io.WriteString(f.w, "\n")
	return err
}

type tapFormatter struct {
	w     io.Writer
	opts  FormatterOptions
	lines []string
	n     int
}

func (f *tapFormatter) Result(r Result) error {
	f.n++
	status := "ok"
	if r.Status == Invalid || r.Status == Error {
		status = "not ok"
	}
	line := fmt.Sprintf("%s %d - %s", status, f.n, r.Path)
	if r.Status == Skipped {
		line += " # SKIP"
	}
	f.lines = append(f.lines, line)
	if r.Status == Error {
		f.lines = append(f.lines, "# "+r.Message())
	}
	for _, d := range r.Diagnostics {
		f.lines = append(f.lines, fmt.Sprintf("# %s: %s: %s [%s]",
			d.Position.String(), d.Severity, d.Message, d.Rule))
	}
	return nil
}

func (f *tapFormatter) Finish(Summary) error {
	_, err := fmt.Fprintf(f.w, "1..%d\n%s\n", f.n, strings.Join(f.lines, "\n"))
	return err
}

// githubFormatter emits GitHub Actions workflow commands so findings show up as
// inline annotations on a pull request.
type githubFormatter struct {
	w    io.Writer
	opts FormatterOptions
}

func (f *githubFormatter) Result(r Result) error {
	if r.Status == Error {
		_, err := fmt.Fprintf(f.w, "::error file=%s::%s\n", r.Path, escapeGitHub(r.Message()))
		return err
	}
	for _, d := range r.Diagnostics {
		level := "notice"
		switch d.Severity {
		case diag.Error:
			level = "error"
		case diag.Warning:
			level = "warning"
		}
		msg := d.Message + " [" + d.Rule + "]"
		if d.Hint != "" {
			msg += "\nhint: " + d.Hint
		}
		if _, err := fmt.Fprintf(f.w, "::%s file=%s,line=%d,col=%d::%s\n",
			level, d.Position.File, d.Position.Line, d.Position.Column, escapeGitHub(msg)); err != nil {
			return err
		}
	}
	return nil
}

func (f *githubFormatter) Finish(s Summary) error {
	if !f.opts.Summary {
		return nil
	}
	_, err := fmt.Fprintf(f.w, "::notice::%d valid, %d invalid, %d error(s)\n", s.Valid, s.Invalid, s.Errors)
	return err
}

// escapeGitHub encodes the characters that would otherwise end a workflow
// command early.
func escapeGitHub(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}
