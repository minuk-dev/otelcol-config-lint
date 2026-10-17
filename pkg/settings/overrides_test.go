package settings_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/settings"
)

func TestRulePolicy(t *testing.T) {
	t.Parallel()

	file, err := settings.Parse([]byte(`rules:
  severity: {debug-exporter-verbosity: error}
  overrides:
    - paths: ["sink.yaml"]
      disable: [debug-exporter-verbosity]
    - paths: ["*.yaml"]
      severity: {debug-exporter-verbosity: info}
`))
	require.NoError(t, err)

	for _, tt := range []struct {
		name, path string
		flags      ruleset.Selection
		want       diag.Severity
	}{
		{name: "first match", path: "/repo/sink.yaml", flags: ruleset.Selection{}, want: diag.Off},
		{name: "second match", path: "other.yaml", flags: ruleset.Selection{}, want: diag.Info},
		{name: "stdin", path: "-", flags: ruleset.Selection{}, want: diag.Error},
		{name: "explicit enable", path: "sink.yaml",
			flags: ruleset.Selection{Enable: []string{"debug-exporter-verbosity"}}, want: diag.Error},
		{name: "explicit severity", path: "sink.yaml",
			flags: ruleset.Selection{Severity: []string{"debug-exporter-verbosity=warning"}}, want: diag.Warning},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolve, err := file.RulePolicy(tt.flags)
			require.NoError(t, err)
			require.Equal(t, tt.want, resolve(tt.path)["debug-exporter-verbosity"])
			levels := resolve(tt.path)
			levels["debug-exporter-verbosity"] = diag.Info

			require.Equal(t, tt.want, resolve(tt.path)["debug-exporter-verbosity"])
		})
	}
}

func TestRulePolicyRejectsInvalidOverrides(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, entry string }{
		{"missing paths", "disable: [missing-batch]"},
		{"empty path", "paths: ['']"},
		{"invalid glob", "paths: ['[']"},
		{"unknown disabled rule", "paths: ['*'], disable: [typo]"},
		{"unknown severity rule", "paths: ['*'], severity: {typo: info}"},
		{"invalid severity", "paths: ['*'], severity: {missing-batch: typo}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := settings.Parse([]byte("rules:\n  overrides:\n    - {" + tt.entry + "}\n"))
			require.NoError(t, err)
			_, err = file.RulePolicy(ruleset.Selection{})
			require.ErrorContains(t, err, "rules.overrides[0]")
		})
	}
}
