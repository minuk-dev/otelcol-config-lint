package otelcolconfiglint

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
)

// SettingsSchemaFile is where the generated schema is committed, and the name
// the published copy is served under.
const SettingsSchemaFile = "otelcol-config-lint.schema.json"

// SettingsSchemaID is the URL an editor points at. It names the copy on main
// rather than a release, because the rule names it enumerates track the rules
// the linter has, and an editor checking a file against a year-old list would
// underline names that work.
const SettingsSchemaID = "https://raw.githubusercontent.com/minuk-dev/" +
	"otelcol-config-lint/main/" + SettingsSchemaFile

// object is one JSON Schema node. The schema is built as data rather than
// written out by hand so the parts that move -- the rule names, the severity
// levels -- come from the same declarations the linter itself reads.
type object = map[string]any

// SettingsSchema returns the JSON Schema for a settings file, indented and
// newline-terminated, exactly as the committed copy is written.
func SettingsSchema() ([]byte, error) {
	doc, err := json.MarshalIndent(settingsSchema(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}

	return append(doc, '\n'), nil
}

// settingsSchema builds the whole document. Every block is closed --
// additionalProperties is false throughout -- because the linter rejects a key
// it does not know, and an editor that accepted one would be quietly promising
// that a misspelled setting is in force.
func settingsSchema() object {
	properties := object{
		"version": object{
			"description": "The schema this file is written against. A file that omits it is read as " +
				SettingsVersion + ".",
			"type": "string",
			"enum": []any{SettingsVersion},
		},
		"run":    runSchema(),
		"rules":  rulesSchema(),
		"issues": issuesSchema(),
		"output": outputSchema(),
	}

	maps.Copy(properties, legacySchema())

	return object{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  SettingsSchemaID,
		"title":                "otelcol-config-lint settings",
		"description":          "Settings for otelcol-config-lint, read from " + DefaultSettingsFile + " or --config.",
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"$defs": object{
			"rule":     ruleSchema(),
			"severity": severitySchema(),
			"quantity": quantitySchema(),
		},
	}
}

// runSchema is the "run" block: which collector the configs target and which
// files are checked against it.
func runSchema() object {
	return block("What to check, and against which collector.", object{
		"collectorVersion": object{
			"description": "Collector release to validate against. \"latest\" uses the newest schema available.",
			"type":        "string",
			"examples":    []any{"latest", "v0.157.0"},
		},
		"distribution": object{
			"description": "Collector binary the config will run on. Any distribution the schema " +
				"registry carries is accepted, so this is not a closed set.",
			"type":     "string",
			"examples": []any{"core", "contrib", "k8s", "otlp"},
		},
		"schemaLocations": object{
			"description": "Where to find schemas, searched in order: a directory, a URL, a " +
				"{{.Version}}/{{.Distribution}} template, or \"default\" for the published registry.",
			"type":  "array",
			"items": object{"type": "string"},
		},
		"strict": object{
			"description": "Report unknown component settings as errors instead of warnings.",
			"type":        "boolean",
		},
		"ignoreMissingSchemas": object{
			"description": "Do not fail on components the schema does not describe, for a custom distribution.",
			"type":        "boolean",
		},
		"concurrency": object{
			"description": "How many files to check in parallel.",
			"type":        "integer",
			"minimum":     1,
		},
		"exclude": object{
			"description": "Glob patterns to skip when walking a directory. Matched against both the " +
				"whole path and the base name; there is no \"**\".",
			"type":  "array",
			"items": object{"type": "string"},
		},
		"kubernetes": kubernetesSchema(),
	})
}

// kubernetesSchema is the deployment environment the sizing rules need, which
// a config file cannot state about itself.
func kubernetesSchema() object {
	defaults := object{
		"enabled": object{
			"description": "The configs run in a Kubernetes pod. Defaults to true when either memory number is written.",
			"type":        "boolean",
		},
		"memoryRequest": ref("#/$defs/quantity", "The container's memory request, e.g. 256Mi."),
		"memoryLimit":   ref("#/$defs/quantity", "The container's memory limit, e.g. 512Mi."),
	}

	override := object{
		"description": "One path-matched environment. It replaces the defaults for the files it " +
			"matches rather than merging with them.",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"paths"},
		"properties": object{
			"paths": object{
				"description": "Glob patterns, matched exactly as run.exclude is.",
				"type":        "array",
				"minItems":    1,
				"items":       object{"type": "string"},
			},
			"enabled":       defaults["enabled"],
			"memoryRequest": defaults["memoryRequest"],
			"memoryLimit":   defaults["memoryLimit"],
		},
	}

	properties := object{"overrides": object{
		"description": "Per-path environments, matched in list order; the first match wins.",
		"type":        "array",
		"items":       override,
	}}
	maps.Copy(properties, defaults)

	return block("The pods the configs run in, for the rules that cannot judge a config "+
		"without knowing what it runs in.", properties)
}

// rulesSchema is the "rules" block: which rules run, at what level, and with
// what settings of their own.
func rulesSchema() object {
	return block("Which rules run and at what level.", object{
		"default": object{
			"description": "The set to start from: every rule, or only what enable names.",
			"type":        "string",
			"enum":        []any{defaultAll, defaultNone},
		},
		"enable": object{
			"description": "Rules to turn on, on top of the set named by default.",
			"type":        "array",
			"items":       ref("#/$defs/rule", ""),
		},
		"disable": object{
			"description": "Rules to turn off. Naming a rule here and under enable is an error.",
			"type":        "array",
			"items":       ref("#/$defs/rule", ""),
		},
		"severity": object{
			"description": "The level a rule reports at. Writing \"off\" disables it, exactly as " +
				"listing it under disable does.",
			"type":                 "object",
			"propertyNames":        ref("#/$defs/rule", ""),
			"additionalProperties": ref("#/$defs/severity", ""),
		},
		"settings": object{
			"description": "Each rule's own settings, keyed by rule name. No built-in rule reads a " +
				"block yet, so writing one is currently reported as an error.",
			"type":                 "object",
			"propertyNames":        ref("#/$defs/rule", ""),
			"additionalProperties": object{"type": "object"},
		},
	})
}

// issuesSchema is the "issues" block: which findings are reported and which of
// them make a file fail.
func issuesSchema() object {
	return block("Which findings are reported, and which of them fail a file.", object{
		"minSeverity": ref("#/$defs/severity", "The lowest severity worth printing."),
		"failOn":      ref("#/$defs/severity", "The severity that makes a file invalid."),
		"exitOnError": object{
			"description": "Stop the run at the first file that fails.",
			"type":        "boolean",
		},
	})
}

// outputSchema is the "output" block, which also accepts a bare format name:
// that is the flat form the first release shipped, and it still means the same
// thing.
func outputSchema() object {
	return object{
		"description": "How the findings are printed. A bare format name is shorthand for the block.",
		"oneOf": []any{
			formatSchema(),
			block("", object{
				"format":  formatSchema(),
				"summary": object{"description": "Append a count of the outcomes.", "type": "boolean"},
				"verbose": object{
					"description": "Also report the files that passed, and say which settings file was read.",
					"type":        "boolean",
				},
				"color": object{
					"description": "Print in colour when the destination is a terminal.",
					"type":        "boolean",
				},
			}),
		},
	}
}

func formatSchema() object {
	return object{
		"description": "Output format.",
		"type":        "string",
		"enum":        []any{"text", "json", "junit", "tap", "github"},
	}
}

// legacySchema describes the flat keys the first release shipped. They are
// still read, so an editor must not underline a file that uses them -- but they
// are marked deprecated, and each one says where it moved.
func legacySchema() object {
	run, rules := runSchema(), rulesSchema()
	issues := issuesSchema()

	moved := map[string]struct {
		from object
		to   string
	}{
		"collectorVersion":     {run, "run.collectorVersion"},
		"distribution":         {run, "run.distribution"},
		"schemaLocations":      {run, "run.schemaLocations"},
		"strict":               {run, "run.strict"},
		"ignoreMissingSchemas": {run, "run.ignoreMissingSchemas"},
		"exclude":              {run, "run.exclude"},
		"kubernetes":           {run, "run.kubernetes"},
		"disable":              {rules, "rules.disable"},
		"severity":             {rules, "rules.severity"},
		"minSeverity":          {issues, "issues.minSeverity"},
		"failOn":               {issues, "issues.failOn"},
	}

	out := object{
		"summary": deprecate(object{
			"description": "Append a count of the outcomes.", "type": "boolean",
		}, "output.summary"),
	}

	for name, m := range moved {
		//nolint:forcetypeassert // the blocks above are built here, one property deep
		out[name] = deprecate(m.from["properties"].(object)[name].(object), m.to)
	}

	return out
}

// deprecate marks a property as replaced, keeping the type it had so a file
// that still uses it is checked rather than merely tolerated.
func deprecate(prop object, moved string) object {
	out := object{"deprecated": true}
	maps.Copy(out, prop)

	out["description"] = fmt.Sprintf("Deprecated: moved to %s.", moved)

	return out
}

// ruleSchema enumerates the rules the linter carries, which is what makes an
// editor able to complete a name and underline a typo. It is why the committed
// schema is generated rather than written: the list moves with the rule set.
func ruleSchema() object {
	names := make([]any, 0, len(ruleset.All()))
	for _, r := range ruleset.All() {
		names = append(names, r.Name())
	}

	return object{
		"description": "A rule the linter carries.",
		"type":        "string",
		"enum":        names,
	}
}

func severitySchema() object {
	return object{
		"description": "A severity level.",
		"type":        "string",
		"enum":        []any{string(diag.Error), string(diag.Warning), string(diag.Info), string(diag.Off)},
	}
}

func quantitySchema() object {
	return object{
		"description": "A Kubernetes quantity such as 512Mi, 1Gi or 2G, or a bare byte count.",
		"type":        "string",
		"pattern":     `^[0-9]+(\.[0-9]+)?(([KMGTPE]i)|[kMGTPE])?$`,
	}
}

// block builds a closed object with the given properties.
func block(description string, properties object) object {
	out := object{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}

	if description != "" {
		out["description"] = description
	}

	return out
}

// ref points at a definition, optionally saying what it means here: a $ref
// alongside a description reads as the description in an editor's tooltip.
func ref(target, description string) object {
	out := object{"$ref": target}
	if description != "" {
		out["description"] = description
	}

	return out
}
