package lint

import (
	"fmt"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

const suppressionPrefix = "otelcol-config-lint:"

// suppressions records directive errors and returns exact source-line exceptions.
func suppressions(file *config.File, rules []rule.Rule, result *Result, opts Options) map[int]string {
	known := make(map[string]bool, len(rules))
	for _, check := range rules {
		known[check.Name()] = true
	}

	out := map[int]string{}

	for line, comment := range file.HeadComments {
		text := strings.TrimSpace(strings.TrimPrefix(comment, "#"))
		if !strings.HasPrefix(text, suppressionPrefix) {
			continue
		}

		parts := strings.Fields(text)
		if len(parts) == 2 && parts[0] == suppressionPrefix+"disable-next-line" && known[parts[1]] {
			out[line+1] = parts[1]

			continue
		}

		addFinding(result, diag.Diagnostic{
			Rule: "invalid-suppression", Severity: diag.Error,
			Position: diag.Position{File: file.Path, Line: line, Column: 1},
			Message:  fmt.Sprintf("invalid suppression %q; expected %sdisable-next-line <known-rule>", text, suppressionPrefix),
		}, opts)
	}

	return out
}

func addFinding(r *Result, d diag.Diagnostic, opts Options) {
	if d.Severity.AtLeast(opts.FailOn) {
		r.Status = Invalid
	}

	if d.Severity.AtLeast(opts.MinSeverity) {
		r.Diagnostics = append(r.Diagnostics, d)
	}
}
