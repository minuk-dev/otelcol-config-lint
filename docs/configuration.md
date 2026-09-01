# Settings file

Commit the policy instead of repeating flags. The file is laid out the way
golangci-lint's is: **what to check** under `run`, **which rules** under
`rules`, **what counts as a failure** under `issues`, and **how it is printed**
under `output`.

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

Every block is optional, as is every key in it: what a file does not state keeps
its default. A key the linter does not know is an error rather than a line that
quietly does nothing.

Each key mirrors a `run` flag of the same meaning — the [flag
table](../README.md#usage) names the pairs. The file is what a repository
commits; a flag is how one run departs from it.

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

See [the rule reference](rules/README.md) for what each one does.

## Per-rule settings

`rules.settings` is where a rule's own knobs go, keyed by rule name, the way
golangci-lint's `linters.settings` works:

```yaml
rules:
  settings:
    some-rule:
      threshold: 10
```

No built-in rule reads a block yet — the schema is here so one can be added
without every settings file having to change shape. Until a rule declares that
it takes settings, writing a block for it is reported as an error: a knob nobody
reads is worse than a knob that is missing.

## Checking the settings file in an editor

`otelcol-config-lint.schema.json` is a JSON Schema for the file, so an editor
underlines a misspelled key, a severity that is not a level, and a rule name
that does not exist. The rule list in it is generated from the rules the linter
carries, and CI fails on a copy that has fallen behind.

Name it from the file itself, which every YAML language server reads:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/otelcol-config-lint.schema.json
```

Or map it once, in VS Code's `settings.json`:

```json
{
  "yaml.schemas": {
    "https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/otelcol-config-lint.schema.json": [
      ".otelcol-config-lint.yaml",
      ".otelcol-config-lint.yml"
    ]
  }
}
```

`otelcol-config-lint config-schema` prints the same document, so a project that
vendors it gets the list of rules the binary it pins actually runs:

```sh
otelcol-config-lint config-schema > otelcol-config-lint.schema.json
```

## The deployment environment

[`memory-limiter-sizing`](rules/memory-limiter-sizing.md) compares a
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
- `enabled` defaults to true when either memory number is written, since a block
  stating what the container has is a block about a container.
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
