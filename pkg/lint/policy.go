package lint

import (
	"maps"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// forPath makes a local policy view; the original linter remains safe to share.
func (l *Linter) forPath(path string) (*Linter, map[string]diag.Severity) {
	if l.opts.RulePolicy == nil {
		return l, nil
	}

	explicit := maps.Clone(l.opts.RulePolicy(path))
	local := *l

	local.opts.Severities = maps.Clone(l.opts.Severities)
	if local.opts.Severities == nil {
		local.opts.Severities = map[string]diag.Severity{}
	}

	for name, severity := range explicit {
		if local.opts.Severities[name] == diag.Off {
			delete(explicit, name)

			continue
		}

		local.opts.Severities[name] = severity
	}

	return &local, explicit
}
