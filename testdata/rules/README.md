# One invalid config per rule

Every rule in `pkg/ruleset` has a fixture here: a config that breaks it, and a
settings file that says so. Together they answer two questions a unit test
answers less directly — what does a config that trips this rule look like, and
does the rule still fire on it when run through the real command line, the
published schemas and the severity gate.

```
testdata/rules/
  insecure-tls.yaml            the config, with the mistake commented where it is
  insecure-tls.settings.yaml   the rules the run has on, and the run itself
  schemas/v9.9.9.json          a small schema, for what no release describes yet
```

`pkg/cmd/otelcol-config-lint/rules_test.go` reads all of it:

- **every rule has a fixture** that enables it, and no fixture names a rule that
  is not registered. A new rule is not finished until it has one.
- **the fixture reports its rule**, and stays silent about the rules its settings
  file disables.
- **the mistake is commented where it is made**: a comment naming the rule has to
  sit on, or just above, a line the rule reports. Whoever opens a fixture reads
  what is wrong without running anything.

## The settings file

The shape is golangci-lint's: what is switched on, what is switched off, and
per-rule options. Everything under `run` describes the run and has a default, so
most fixtures write nothing there.

```yaml
rules:
  # Rules that must report on the fixture. Normally the one it is named after.
  enable:
    - insecure-tls
  # Rules the run turns off, which must then stay silent. This is how a fixture
  # stays about one mistake when the config that shows it cannot help making
  # another -- say a config with no service block, where every component is
  # also unused.
  disable: []
  # Per-rule options, keyed by rule name. No rule takes one yet; the key is
  # here so that giving a rule its first option is a fixture edit rather than a
  # change to how fixtures are written.
  settings: {}

run:
  minSeverity: info               # default: every finding is visible
  collectorVersion: v0.157.0      # default: the latest schema in testdata/schemas
  distribution: contrib           # default
  schemaLocations: [schemas]      # searched first, relative to this directory
  strict: false                   # default
  kubernetes:                     # for the rules that need to know the container
    memoryRequest: 256Mi
    memoryLimit: 256Mi
```

Each field is a flag the command line already has, so a fixture is a run anyone
can repeat by hand:

```sh
otelcol-config-lint run --schema-location testdata/schemas --min-severity info \
  --memory-limit 256Mi testdata/rules/memory-limiter-sizing.yaml
```

## Adding one

1. Write `<rule>.yaml`: a config that is otherwise clean, with one deliberate
   mistake and a comment naming the rule on the line that makes it.
2. Write `<rule>.settings.yaml` enabling the rule. If the config cannot avoid
   tripping a second rule, disable that one and say why in a comment.
3. `go test ./pkg/cmd/otelcol-config-lint/`.

One rule reports something no published schema carries yet: no release in
`testdata/schemas` marks a setting as deprecated, so `deprecated-field` brings
its own schema in `schemas/` and selects it with `run.collectorVersion` and
`run.schemaLocations`. A rule in that position is the reason `run` exists.
