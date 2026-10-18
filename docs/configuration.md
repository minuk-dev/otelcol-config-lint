# Settings file

Commit the policy instead of repeating flags. Use `run` for the target,
`rules` for policy, `issues` for failure thresholds, and `output` for reporting.

```yaml
version: "1"

run:
  collectorVersion: v0.157.0
  distribution: contrib
  strict: true
  concurrency: 8
  exclude:
    - "*.tmpl.yaml"
  schemaLocations:
    - ./schemas       # this project's own schemas first
    - default         # then the published registry

rules:
  default: all        # or "none", to run only what enable names
  enable: []
  disable:
    - missing-batch
  severity:
    missing-memory-limiter: warning
  settings: {}        # each rule's own block, keyed by rule name

issues:
  minSeverity: warning
  failOn: error
  exitOnError: false

output:
  format: text
  summary: true
  verbose: false
  color: true
```

Relative `run.schemaLocations` paths (including legacy top-level
`schemaLocations`) resolve from the settings file directory. URLs, absolute
paths, and `default` keep their meaning. Explicit `--schema-location` paths
resolve from the current working directory.

Every block is optional, as is every key in it: what a file does not state keeps
its default. A key the linter does not know is an error rather than a line that
quietly does nothing.

Use `otelcol-config-lint run --help` for flags and
[otelcol-config-lint.schema.json](../otelcol-config-lint.schema.json) for all
settings keys and defaults.

## Where the file is found

`.otelcol-config-lint.yaml` — or `.otelcol-config-lint.yml` — is looked for in
the working directory and then in each parent, stopping at the directory holding
`.git`, so one file at the repository root governs a run started from any
subdirectory.

- `--config` names another file. An explicitly named file that does not exist is
  an error.
- `--no-config` runs on the flags alone.
- `--verbose` prints which file was read.

Explicit flags win over the file, with one exception: the rule lists merge, so
`-D` adds to `rules.disable` rather than replacing it.

## Choosing the rules

`rules.default` is the set to start from — `all`, which is every rule and what a
file that says nothing gets, or `none`, which runs only what `enable` names.
`disable` then takes rules out, and `severity` sets the level a rule reports at.

```yaml
rules:
  default: none
  enable: [unknown-component, invalid-value, undefined-reference]
```

Naming a rule in both `enable` and `disable` is an error rather than a silent
win for one of them. `otelcol-config-lint list rules` prints what the policy
resolves to: a rule that will not run is listed at severity `off`.

See [Rule behavior](rules.md) for analysis limits and policy choices.

## Per-rule settings

`rules.settings` holds options keyed by rule name:

```yaml
rules:
  settings:
    missing-batch:
      additional_processors: [resourcebatch]
```

`missing-batch.additional_processors` names custom processor types that your
distribution uses for batching. Only configure types whose batching behavior you
have verified. Rules that do not declare settings still reject a settings block.

## Checking the settings file in an editor

`otelcol-config-lint.schema.json` is a JSON Schema for the file, so an editor
underlines a misspelled key, a severity that is not a level, and a rule name
that does not exist. The rule list in it is generated from the rules the linter
carries, and CI fails on a copy that has fallen behind.

Name it from the file itself, which every YAML language server reads:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/otelcol-config-lint.schema.json
```

`otelcol-config-lint config-schema` prints the same document, so a project that
vendors it gets the list of rules the binary it pins actually runs:

```sh
otelcol-config-lint config-schema > otelcol-config-lint.schema.json
```

## The deployment environment

`memory-limiter-sizing` compares a
`memory_limiter` against the container it runs in, which the config file cannot
state. The `run.kubernetes` block supplies it.

It is resolved **per file**, because a run is not one deployment: an agent
DaemonSet at `256Mi` and a gateway Deployment at `4Gi` sit in the same directory
and are checked in the same run.

```yaml
run:
  kubernetes:
    enabled: true
    memoryRequest: 512Mi        # the defaults, for any file no override matches
    memoryLimit: 512Mi
    overrides:
      - paths: ["configs/agent-*.yaml"]
        memoryRequest: 256Mi
        memoryLimit: 256Mi
      - paths: ["configs/gateway/*.yaml"]
        memoryRequest: 4Gi
        memoryLimit: 4Gi
      - paths: ["configs/legacy/*.yaml"]
        enabled: false          # opt a subtree back out
```

- Overrides are matched in list order and the **first match wins**. A matching
  override replaces the defaults; it does not merge with them.
- `paths` are globs matched against both the whole path and the base name,
  exactly as `--exclude` is, so the two behave identically — including which
  patterns do not work: there is no `**`.
- `enabled` defaults to true when `hostNetwork` or either memory number is written.
- `hostNetwork` is optional: `false` states the pod has its own network namespace;
  `true` states it shares the node network. Only explicit `false` lowers wildcard
  listener findings to info. Omit it when unknown to retain warnings. Each path
  override replaces this value too; it is not inferred from Collector YAML.
- Sizes are Kubernetes quantities: `512Mi`, `1Gi`, `2G`, or a bare byte count.
  Anything else is an error rather than a silently different number.
- A file that matches no override and has no defaults to fall back on simply
  skips `memory-limiter-sizing`. That is per file: the rest of the run is
  unaffected. `--verbose` prints what each file resolved to.
- Config read from stdin is reported as `stdin`, which no glob is meant to
  match, so it gets the defaults.

`--kubernetes`, `--memory-request` and `--memory-limit` are the single-file
convenience: they set the defaults and, like every flag, win over the file.
There is deliberately no flag form of the overrides.

## The flat form

The keys the first release put at the top level — `collectorVersion`,
`distribution`, `schemaLocations`, `strict`, `ignoreMissingSchemas`, `summary`,
`minSeverity`, `failOn`, `disable`, `severity`, `exclude` and `kubernetes` — are
still read, and folded into the blocks above. A run that finds one says which
keys to move. `output: json` is not one of them: a bare format name is still the
shorthand for `output: {format: json}`.

## Suppressing one diagnostic line

Place a standalone directive immediately before the line reported by a rule:

```yaml
service:
  pipelines:
    logs/mismatch:
      exporters:
        # otelcol-config-lint:disable-next-line debug-exporter-verbosity
        - debug/mismatch
```

The directive affects only that rule on the next physical line. It does not
suppress an entire mapping or pipeline, and an intervening blank line means it
will not target the next YAML node. Use one registered rule name. Misspelled
names and unsupported directive syntax produce `invalid-suppression` errors.
Comments inside quoted strings or block scalar values are data, not directives;
inside an extracted ConfigMap Collector config they are parsed as inner YAML.

Suppression happens before failure and output thresholds are evaluated, for all
output formats. Aliases and merges retain the source positions the parser uses
for diagnostics: place the comment at the reported source line, which can be
an anchor definition. Other references and other rules remain checked.

## File-specific rule exceptions

```yaml
rules:
  overrides:
    - paths: ["sink-*.yaml"]
      disable: [debug-exporter-verbosity]
      severity:
        missing-batch: warning
```

Patterns use the same filesystem-path and basename matching as `run.exclude`.
The first matching entry overlays global policy. These are file paths, not
pipeline names or YAML paths. Embedded configs use their outer file's path;
stdin uses global policy only. An override cannot enable a globally disabled
rule. Explicit CLI enables/disables bypass file exceptions for those rule names,
and explicit `--severity` wins over both settings and file exceptions. Explicit
severity also overrides a rule's per-finding default, before failure and output
thresholds are evaluated. Invalid patterns, rule names and severities fail before
any input is linted.
