package componentstability

import (
	"errors"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

var errSettings = errors.New("expected report_alpha: true or false")

// WithSettings returns a configured copy; the default includes alpha findings.
func (r componentStability) WithSettings(node *yaml.Node) (rule.Rule, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errSettings
	}

	for _, entry := range rule.MapEntries(node, "") {
		if entry.Key != "report_alpha" || entry.Node.Tag != "!!bool" {
			return nil, errSettings
		}

		err := entry.Node.Decode(&r.reportAlpha)
		if err != nil {
			return nil, errSettings
		}
	}

	return r, nil
}
