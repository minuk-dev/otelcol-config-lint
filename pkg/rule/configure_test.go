package rule_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// configurableRule stands in for a rule that takes settings. No built-in rule
// does yet -- the schema is there so one can, without every caller having to
// change -- so the mechanism is exercised against a rule written here.
type configurableRule struct {
	limit int
}

func (configurableRule) Name() string            { return "configurable" }
func (configurableRule) Description() string     { return "a rule that reads settings" }
func (configurableRule) Severity() diag.Severity { return diag.Warning }
func (configurableRule) Check(_ *rule.Context)   {}

func (r configurableRule) WithSettings(node *yaml.Node) (rule.Rule, error) {
	var block struct {
		Limit int `yaml:"limit"`
	}

	err := node.Decode(&block)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return configurableRule{limit: block.Limit}, nil
}

// plainRule takes no settings, which is every built-in rule today.
type plainRule struct{}

func (plainRule) Name() string            { return "plain" }
func (plainRule) Description() string     { return "a rule that reads no settings" }
func (plainRule) Severity() diag.Severity { return diag.Warning }
func (plainRule) Check(_ *rule.Context)   {}

// block parses one settings block, as the settings file's decoder hands it over.
func block(t *testing.T, src string) yaml.Node {
	t.Helper()

	var doc yaml.Node

	require.NoError(t, yaml.Unmarshal([]byte(src), &doc))

	return *doc.Content[0]
}

func TestConfigureAppliesARulesOwnBlock(t *testing.T) {
	t.Parallel()

	rules := []rule.Rule{configurableRule{limit: 0}, plainRule{}}

	out, err := rule.Configure(rules, map[string]yaml.Node{"configurable": block(t, "limit: 7")})
	require.NoError(t, err)

	configured, ok := out[0].(configurableRule)
	require.True(t, ok, "the configured rule should keep its type")
	assert.Equal(t, 7, configured.limit)
}

// TestConfigureLeavesTheRuleSetItWasGiven pins that a rule set stays reusable
// after being configured: a rule that configured itself in place would carry
// one run's settings into the next.
func TestConfigureLeavesTheRuleSetItWasGiven(t *testing.T) {
	t.Parallel()

	rules := []rule.Rule{configurableRule{limit: 1}}

	_, err := rule.Configure(rules, map[string]yaml.Node{"configurable": block(t, "limit: 9")})
	require.NoError(t, err)

	first, ok := rules[0].(configurableRule)
	require.True(t, ok)
	assert.Equal(t, 1, first.limit, "the set handed in must not change")
}

// TestSettingsForARuleThatTakesNoneAreReported guards the whole point of the
// interface: a block nobody reads is a policy its author believes is in force.
func TestSettingsForARuleThatTakesNoneAreReported(t *testing.T) {
	t.Parallel()

	_, err := rule.Configure([]rule.Rule{plainRule{}}, map[string]yaml.Node{"plain": block(t, "limit: 7")})

	require.ErrorIs(t, err, rule.ErrNoSettings)
	assert.Contains(t, err.Error(), "plain")
}

func TestConfigureReportsABlockARuleCannotRead(t *testing.T) {
	t.Parallel()

	_, err := rule.Configure(
		[]rule.Rule{configurableRule{limit: 0}},
		map[string]yaml.Node{"configurable": block(t, "limit: not-a-number")},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "configurable")
}

// TestConfigureIgnoresARuleTheSetDoesNotHold pins the division of labour: this
// package cannot tell a rule that was turned off from one that was misspelled,
// so the caller that knows the difference is the one that reports it.
func TestConfigureIgnoresARuleTheSetDoesNotHold(t *testing.T) {
	t.Parallel()

	rules := []rule.Rule{plainRule{}}

	out, err := rule.Configure(rules, map[string]yaml.Node{"absent": block(t, "limit: 7")})

	require.NoError(t, err)
	assert.Equal(t, rules, out)
}
