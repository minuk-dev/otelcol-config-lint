# otelcol-config-lint

A linter for OpenTelemetry Collector config files, validated against a **specific
collector release**. The same config can be valid on v0.110.0 and broken on
v0.157.0 — components get added, renamed and removed every few weeks — so the
release you target is a first-class input, the way `kubeconform` treats the
Kubernetes version.

```console
$ otelcol-config-lint run --collector-version v0.157.0 config.yaml
config.yaml:6:3: error: unknown receiver type "prometheusreceiver" in collector v0.157.0 [unknown-component]
    hint: checked against collector v0.157.0; did you mean "prometheus"?
config.yaml:11:14: error: "timeout" must be a duration such as 5s, 200ms or 1m30s [invalid-value]
config.yaml:17:3: warning: exporter "otlp" is deprecated [deprecated-component]
    hint: renamed to "otlp_grpc" upstream; the old name still resolves for now
config.yaml:29:30: error: service.extensions references "pprof" which is not declared under extensions [undefined-reference]
    hint: declared extensions: health_check
config.yaml:34:5: error: processor "memory_limiter" sets neither limit_mib nor limit_percentage: 'limit_mib' or 'limit_percentage' must be greater than zero [memory-limiter-config]
    hint: set limit_mib to the memory the collector may use; spike_limit_mib then defaults to 20% of it
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
```

No collector binary and no Go toolchain are needed at lint time. The component
schemas are fetched from a published registry, so a new collector release
becomes lintable as soon as its schema is committed — no linter upgrade. Point
`--schema-location` at a directory to run without network access.

## Documentation

| | |
| --- | --- |
| [Rules](docs/rules/README.md) | all 35 rules, one page each: what they report and why |
| [Settings file](docs/configuration.md) | `.otelcol-config-lint.yaml`, rule selection, the deployment environment |
| [Schemas](docs/schemas.md) | the registry, caching, and generating a schema for your own distribution |
| [Working on the linter](docs/contributing.md) | layout, development, adding a rule |

## Install

```sh
go install github.com/minuk-dev/otelcol-config-lint/cmd/otelcol-config-lint@latest
```

Or run it as a container, mounting the configs to check:

```sh
docker run --rm -v "$PWD:/workspace" \
  ghcr.io/minuk-dev/otelcol-config-lint:latest run --summary ./configs
```

Tagged releases publish binaries for Linux, macOS and Windows on amd64 and
arm64, plus a multi-arch image on ghcr.io.

## GitHub Action

In a workflow it is one `uses:` line — no Go toolchain, no install step:

```yaml
- uses: minuk-dev/otelcol-config-lint@v0
  with:
    files: ./configs
    collector-version: v0.157.0
    strict: true
```

Findings land as inline pull-request annotations, because the action defaults to
`--output github`. The step passes when everything is valid, fails when a file
is invalid, and reports a usage error — a rule that does not exist, an unreadable
settings file — distinctly, with exit code 2.

Every input is the `run` flag of the same name, so the [flag table](#flags) is
the whole reference: `files` (default `.`, whitespace-separated and so unable to
carry a path with a space in it, globs allowed), `collector-version`,
`distribution`, `schema-location` (one per line to search several in order),
`strict`, `ignore-missing-schemas`, `min-severity`, `fail-on`, `default`,
`enable`, `disable`, `severity`, `exclude`, `output` (default `github`),
`config`, `no-config`, `summary` (default `true`), `verbose` and
`exit-on-error`. `--concurrency`, `--no-color`, `--no-cache` and
`--insecure-schema-location` are left out: a runner gains nothing from the first
two, a fresh container has no cache to read, and a workflow that reads its
schemas over plain HTTP is one whose findings anyone on the path can choose.

The counts come back as outputs — `exit-code`, `valid`, `invalid`, `errors`,
`skipped`, `warnings` and `infos` — so a later step can decide what to do with
them:

```yaml
- uses: minuk-dev/otelcol-config-lint@v0
  id: lint
  continue-on-error: true
  with:
    files: ./configs
    fail-on: warning
- run: echo "${{ steps.lint.outputs.invalid }} file(s) failed"
```

`@v0` follows the newest v0 release, and runs exactly that release's linter: the
action wraps `ghcr.io/minuk-dev/otelcol-config-lint:<release>`, pinned at a tag
and never `latest`, so a step is two small image pulls rather than a Go toolchain
and a compile. The major tag is whatever the release's own major is — the release
workflow moves it — so it becomes `@v1` when 1.0.0 ships, and until then `@v1`
resolves to nothing. Pin a full tag such as `@v0.1.1` to hold a workflow to one
revision. The schemas are a separate registry that tracks its own main, so pin
`schema-location` as well — at a vendored directory, or at a tagged URL — for a
run that cannot change underneath a workflow.

## Usage

```
otelcol-config-lint run [flags] <file|dir|->...
otelcol-config-lint list rules
otelcol-config-lint list versions
otelcol-config-lint config-schema
otelcol-config-lint version
```

| Command | Meaning |
| --- | --- |
| `run` | lint the given files, directories, or `-` for stdin |
| `list rules` | the rules and the severities they will run at, the rule flags and settings file applied |
| `list versions` | the schema versions available, honouring `--schema-location` and `--distribution` |
| `config-schema` | the JSON Schema for the settings file, for an editor to check one against |
| `version` | print the linter version (also available as `--version`) |

```sh
otelcol-config-lint run config.yaml                               # lint one file
otelcol-config-lint run --summary ./configs                       # walk a directory
cat config.yaml | otelcol-config-lint run -                       # read stdin
otelcol-config-lint run --output json ./configs > report.json     # machine-readable
otelcol-config-lint run --output github ./configs                 # PR annotations
otelcol-config-lint run --collector-version v0.110.0 config.yaml  # target an older release
otelcol-config-lint run --distribution core config.yaml           # target plain otelcol
otelcol-config-lint run --default none -E invalid-value ./configs # run one rule and nothing else
otelcol-config-lint run -c ci.yaml ./configs                      # a settings file of its own
```

### Flags

These belong to `run`. Every one mirrors a key of the [settings
file](docs/configuration.md), named in the middle column: the file is what a
repository commits, a flag is how one run departs from it.

| Flag | Settings key | Meaning |
| --- | --- | --- |
| `-c`, `--config` | — | settings file to read (default: `.otelcol-config-lint.yaml`, searched for here and in each parent) |
| `--no-config` | — | ignore any settings file and use the flags alone |
| `--collector-version` | `run.collectorVersion` | release to validate against, e.g. `v0.157.0` (default `latest`) |
| `--distribution` | `run.distribution` | collector binary to validate against: `core`, `contrib` (default), `k8s` or `otlp` |
| `--schema-location` | `run.schemaLocations` | where to find schemas: a registry directory or URL, a `{{.Version}}`/`{{.Distribution}}` template, or `default`. Repeat to search several in order |
| `--insecure-schema-location` | `run.insecureSchemaLocation` | allow a plain `http://` schema location, which is otherwise refused. For a registry served on localhost |
| `--no-cache` | `run.noCache` | fetch schemas again instead of reading the ones kept from earlier runs |
| `--allow-nearest-fallback` | `run.allowNearestFallback` | check against the nearest older release when the registry has no schema for the one asked for. Without it, that is a usage error |
| `--strict` | `run.strict` | unknown component settings become errors instead of warnings |
| `--ignore-missing-schemas` | `run.ignoreMissingSchemas` | do not fail on components absent from the schema (custom distributions) |
| `--exclude` | `run.exclude` | glob patterns to skip when walking directories |
| `-n`, `--concurrency` | `run.concurrency` | files checked in parallel |
| `--kubernetes` | `run.kubernetes.enabled` | the config runs in a Kubernetes pod |
| `--memory-request`, `--memory-limit` | `run.kubernetes.memoryRequest`/`.memoryLimit` | the container's resources, e.g. `256Mi`, `1Gi`, `2G`. Either one implies `--kubernetes` |
| `--default` | `rules.default` | rule set to start from: `all` (the default) or `none` |
| `-E`, `--enable` | `rules.enable` | rules to turn on, on top of `--default` |
| `-D`, `--disable` | `rules.disable` | rules to turn off |
| `--severity` | `rules.severity` | `rule=level` overrides |
| `--min-severity` | `issues.minSeverity` | lowest severity to report: `error`, `warning`, `info` |
| `--fail-on` | `issues.failOn` | severity that makes a file invalid (default `error`) |
| `--exit-on-error` | `issues.exitOnError` | stop at the first file that fails |
| `--output` | `output.format` | `text`, `json`, `junit`, `tap` or `github` |
| `--summary` | `output.summary` | append a count of the outcomes |
| `--verbose` | `output.verbose` | also report the files that passed, and say which settings file was read |
| `--no-color` | `output.color` (inverted) | disable coloured output |

`--enable`, `--disable`, `--severity`, `--exclude` and `--schema-location` take a
comma-separated list, and may also be repeated.

### Exit codes

`0` everything passed, `1` at least one file failed, `2` the command could not
run. A `--collector-version` the registry has no schema for is that last case:
the run ends naming the nearest release available, because checking the config
against a release nobody asked for and exiting `0` says nothing that CI can
read. Pass `--allow-nearest-fallback` to check against it anyway, for a
repository deliberately tracking ahead of the registry.

## Settings file

Commit the policy instead of repeating flags:

```yaml
version: "1"

run:
  collectorVersion: v0.157.0
  distribution: contrib
  strict: true

rules:
  disable:
    - missing-batch
  severity:
    missing-memory-limiter: warning

issues:
  minSeverity: warning
  failOn: error
```

`.otelcol-config-lint.yaml` is looked for in the working directory and then in
each parent, stopping at the repository root. See
[docs/configuration.md](docs/configuration.md) for the whole file: where it is
found, how rules are selected, the JSON Schema an editor checks it against, and
the `run.kubernetes` block that tells `memory-limiter-sizing` what container a
config runs in.

## What it checks

35 rules, listed by `otelcol-config-lint list rules` and documented one page
each under [docs/rules/](docs/rules/README.md).

- **[Structure](docs/rules/README.md#structure)** — the config the collector
  refuses to load, or loads while silently ignoring half of.
  `unknown-top-level-key`, `service-required`, `unknown-service-key`,
  `invalid-pipeline-key`, `empty-pipeline`, `unknown-pipeline-key`,
  `duplicate-key`, `wrong-node-type`.
- **[Wiring](docs/rules/README.md#wiring)** — whether what is declared and what
  is referenced add up. `undefined-reference`,
  `undefined-extension-reference`, `unused-component`, `duplicate-reference`,
  `connector-wiring`.
- **[Release and distribution
  compatibility](docs/rules/README.md#release-and-distribution-compatibility)** —
  `unknown-component` (with "exists in v0.110.0 but not in v0.157.0", or "not in
  the core distribution; it ships in contrib, k8s"), `signal-support`,
  `component-stability`, `deprecated-component`.
- **[Settings](docs/rules/README.md#settings)** — `unknown-field`,
  `required-field`, `invalid-value`, `deprecated-field`, read from field schemas
  covering 92–96% of components. What cannot be resolved is left open rather
  than reported, so partial coverage never produces false positives.
- **[Practice](docs/rules/README.md#practice)** — configs that load and run, and
  then behave in a way nobody asked for. `processor-order`,
  `missing-memory-limiter`, `missing-batch`, `memory-limiter-config`,
  `memory-limiter-sizing`, `batch-size-bounds`, `no-persistent-queue`,
  `debug-exporter-verbosity`.
- **[The collector's own
  telemetry](docs/rules/README.md#the-collectors-own-telemetry)** —
  `deprecated-telemetry-key`, `telemetry-metrics-disabled`. Both report a
  silence rather than a failure.
- **[Security](docs/rules/README.md#security)** — `insecure-tls`,
  `receiver-binds-all-interfaces`, `debug-extension-exposed`,
  `hardcoded-secret`.

Every rule reads the config the collector will read: YAML anchors, aliases and
`<<` merge keys are resolved when the file is parsed, and findings keep the line
they were written on. A rule that reports what the collector requires or
recommends carries a `docs:` link to the upstream page that says so, so the
claim can be checked rather than taken on trust — it is in the JSON output as
`docs`, and in the GitHub annotations.

## Schemas

Component schemas are published at
[minuk-dev/otelcol-config-schemas](https://github.com/minuk-dev/otelcol-config-schemas),
one file per collector release per distribution, generated from the
`metadata.yaml` upstream ships. They are read over HTTPS by default and cached
between runs; `--schema-location ../otelcol-config-schemas` runs against a
checkout instead, with no network access at all.

A weekly job **in that repository** generates the schemas for each new collector
release and opens a pull request there. `cmd/schemagen` in this repository is
what it runs, and is also how a private distribution gets a schema of its own —
see [docs/schemas.md](docs/schemas.md).

## Contributing

[docs/contributing.md](docs/contributing.md) has the repository layout, the
`make` targets, how releases are cut, and what adding a rule takes.
