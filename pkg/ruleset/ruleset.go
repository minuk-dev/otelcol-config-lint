// Package ruleset collects every built-in rule into one set.
//
// It is separate from pkg/rule so that a rule package can import the vocabulary
// it is written in without the two importing each other: pkg/rule defines what
// a rule is, each package under it defines one rule, and this package is the
// only place that knows about all of them.
//
// Adding a rule is therefore two edits: a new package under pkg/rule, and one
// line in the list below. Two more are enforced by the tests: an invalid config
// the rule reports on, under testdata/rules, which is where a rule is shown
// working through the command line rather than against a stand-in schema; and
// "make config-schema", because the JSON Schema an editor checks a settings
// file against lists every name in this set.
package ruleset

import (
	"slices"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/batchsizebounds"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/componentstability"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/connectorwiring"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugexporterverbosity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugextensionexposed"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/deprecatedcomponent"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/deprecatedfield"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/deprecatedtelemetrykey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/duplicatekey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/duplicatereference"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/emptypipeline"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/hardcodedsecret"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/insecuretls"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidpipelinekey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidvalue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimiterconfig"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimitersizing"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/missingbatch"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/missingmemorylimiter"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/nopersistentqueue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/processororder"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/receiverbindsallinterfaces"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/requiredfield"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/servicerequired"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/signalsupport"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/telemetrymetricsdisabled"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/undefinedextensionreference"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/undefinedreference"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknowncomponent"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknownfield"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknownpipelinekey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknownservicekey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknowntoplevelkey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unusedcomponent"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/wrongnodetype"
)

// All returns every built-in rule, sorted by name. The set is built on each
// call rather than registered into package state, so nothing depends on
// initialisation order and callers can safely keep or filter the result.
func All() []rule.Rule {
	rules := []rule.Rule{
		// The shape of the document itself.
		unknowntoplevelkey.New(),
		servicerequired.New(),
		unknownservicekey.New(),
		invalidpipelinekey.New(),
		emptypipeline.New(),
		unknownpipelinekey.New(),
		duplicatekey.New(),
		wrongnodetype.New(),

		// How the service block wires components together.
		undefinedreference.New(),
		undefinedextensionreference.New(),
		unusedcomponent.New(),
		duplicatereference.New(),
		connectorwiring.New(),

		// Declarations against the targeted release's schema.
		unknowncomponent.New(),
		signalsupport.New(),
		componentstability.New(),
		deprecatedcomponent.New(),

		// Component settings against their field schema.
		unknownfield.New(),
		requiredfield.New(),
		invalidvalue.New(),
		deprecatedfield.New(),

		// What a field schema cannot state: one setting against another.
		batchsizebounds.New(),
		memorylimiterconfig.New(),
		memorylimitersizing.New(),

		// Configurations that load but behave badly.
		processororder.New(),
		missingmemorylimiter.New(),
		missingbatch.New(),
		nopersistentqueue.New(),
		debugexporterverbosity.New(),

		// The collector's own observability, which is the one part of a config
		// that is about the run rather than about the data.
		deprecatedtelemetrykey.New(),
		telemetrymetricsdisabled.New(),

		// What a config hands to the world around it.
		insecuretls.New(),
		debugextensionexposed.New(),
		receiverbindsallinterfaces.New(),
		hardcodedsecret.New(),
	}
	slices.SortFunc(rules, func(a, b rule.Rule) int { return strings.Compare(a.Name(), b.Name()) })

	return rules
}

// Lookup returns a built-in rule by name.
func Lookup(name string) (rule.Rule, bool) {
	for _, r := range All() {
		if r.Name() == name {
			return r, true
		}
	}

	return nil, false
}
