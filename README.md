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
config.yaml:22:3: error: unknown exporter type "logging" in collector v0.157.0 [unknown-component]
    hint: "logging" exists in v0.110.0 but not in v0.157.0
config.yaml:29:30: error: service.extensions references "pprof" which is not declared under extensions [undefined-reference]
    hint: declared extensions: health_check
```

No collector binary, no network access and no Go toolchain are needed at lint
time: the component catalogs are compiled into the binary.

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

## Usage

```
otelcol-config-lint run [flags] <file|dir|->...
otelcol-config-lint list rules
otelcol-config-lint list versions
otelcol-config-lint version
```

| Command | Meaning |
| --- | --- |
| `run` | lint the given files, directories, or `-` for stdin |
| `list rules` | the rules and their default severities, `--disable`/`--severity` applied |
| `list versions` | the catalog versions available, honouring `--catalog-location` |
| `version` | print the linter version (also available as `--version`) |

The flags below belong to `run`:

| Flag | Meaning |
| --- | --- |
| `--collector-version` | release to validate against, e.g. `v0.157.0` (default `latest`) |
| `--catalog-location` | where to find catalogs: a directory, a `{{.Version}}` template, a URL, or `default`. Repeat to search several in order |
| `--output` | `text`, `json`, `junit`, `tap` or `github` |
| `--strict` | unknown component settings become errors instead of warnings |
| `--ignore-missing-schemas` | do not fail on components absent from the catalog (custom distributions) |
| `--min-severity` | lowest severity to report: `error`, `warning`, `info` |
| `--fail-on` | severity that makes a file invalid (default `error`) |
| `--disable` | comma-separated rules to turn off |
| `--severity` | comma-separated `rule=level` overrides |
| `--exclude` | glob patterns to skip when walking directories |
| `-n` | files checked in parallel |
| `--summary`, `--verbose`, `--no-color`, `--exit-on-error` | output control |

Exit codes: `0` everything passed, `1` at least one file failed, `2` the command
could not run.

```sh
otelcol-config-lint run config.yaml                               # lint one file
otelcol-config-lint run --summary ./configs                       # walk a directory
cat config.yaml | otelcol-config-lint run -                       # read stdin
otelcol-config-lint run --output json ./configs > report.json     # machine-readable
otelcol-config-lint run --output github ./configs                 # PR annotations
otelcol-config-lint run --collector-version v0.110.0 config.yaml  # target an older release
```

### Settings file

Commit the policy instead of repeating flags. `.otelcol-config-lint.yaml` in the
working directory is picked up automatically; `--config` names another file.
Explicit flags win over the file.

```yaml
collectorVersion: v0.157.0
minSeverity: warning
strict: true
exclude:
  - "*.tmpl.yaml"
disable:
  - missing-batch
severity:
  missing-memory-limiter: warning
catalogLocations:
  - ./catalogs      # this project's own catalogs first
  - default         # then the built-in ones
```

## What it checks

`otelcol-config-lint list rules` prints the current set with default severities.

**Structure** — `unknown-top-level-key`, `service-required`,
`unknown-service-key`, `invalid-pipeline-key`, `empty-pipeline`,
`unknown-pipeline-key`, `duplicate-key`, `wrong-node-type`.

**Wiring** — `undefined-reference`, `unused-component`, `duplicate-reference`,
`connector-wiring` (a connector must export from one pipeline and receive into
another, and must not close a loop).

**Release compatibility** — `unknown-component` (with "exists in v0.110.0 but
not in v0.157.0" when a component was removed), `signal-support` (a receiver
that only does traces used in a metrics pipeline), `component-stability`,
`deprecated-component`.

**Settings** — `unknown-field`, `required-field`, `invalid-value`,
`deprecated-field`. These only run for components with a field schema, so
partial coverage never produces false positives. `${env:...}` expansions are
left alone.

**Practice** — `processor-order` (memory_limiter first), `missing-memory-limiter`,
`missing-batch`, `insecure-tls`.

## Catalogs

[`catalogs/`](catalogs) holds one file per collector release, in both YAML (the
readable form, meant to be reviewed in pull requests) and JSON. They are
generated from the `metadata.yaml` that every upstream component ships, across
**both core and contrib** — 342 components for v0.157.0.

```yaml
components:
  receiver:
    otlp:
      type: otlp
      signals: [traces, metrics, logs, profiles]
      stability:
        traces: stable
        metrics: stable
        logs: stable
        profiles: alpha
      distributions: [core, contrib, k8s, otlp]
      module: go.opentelemetry.io/collector/receiver/otlpreceiver
```

Because the catalogs sit at the repository root, they can be consumed without
installing or cloning anything:

```sh
otelcol-config-lint run --catalog-location \
  https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/catalogs/{{.Version}}.yaml \
  config.yaml
```

A location is a directory, a `{{.Version}}` template, an `http(s)` URL, or
`default` for the built-ins. Repeat the flag to search several in order, so a
private distribution's catalog can take precedence over the public ones.

### Adding a release

```sh
make catalogs VERSIONS=v0.158.0
```

`tools/schemagen` downloads the core and contrib source archives for the tag,
reads every `metadata.yaml`, and writes `catalogs/<version>.yaml` and `.json`.
It needs no collector dependencies, so the linter itself stays a two-package
build.

Component renames are carried through: upstream is moving types to snake_case,
so from v0.157.0 the OTLP gRPC exporter is `otlp_grpc` with `otlp` kept as a
deprecated alias. Both resolve, and using the old name reports
`deprecated-component`.

### Field schemas

`metadata.yaml` describes components but not their settings, so field-level
schemas come from hand-written overlays in [`overlays/`](overlays), merged into
the catalog at generation time:

```yaml
kind: processor
type: batch
fields:
  type: map
  children:
    timeout: {type: duration}
    send_batch_size: {type: int}
```

Overlays accept `minVersion`/`maxVersion` when a component's settings change
between releases. Components without an overlay are simply not field-checked.

## Layout

```
cmd/otelcol-config-lint/      the entry point
pkg/cmd/otelcol-config-lint/  the cobra command: flags, settings files, reporting
pkg/scanner/              expands the given paths into the files to lint
pkg/sets/                 a set built on a map, in the shape of k8s.io/apimachinery
pkg/config/               YAML parsing that keeps positions, so findings have line numbers
pkg/catalog/              catalog types, version resolution and location lookup
pkg/rule/                 the rules and the registry
pkg/lint/                 the engine and the output formatters
pkg/diag/                 diagnostics, severities and positions
catalogs/                 generated per-release catalogs (yaml + json), embedded
overlays/                 hand-written field schemas
tools/schemagen/          catalog generator
```

## Development

```sh
make test            # go test -race with coverage
make lint            # golangci-lint
make build           # ./bin/otelcol-config-lint
make build-snapshot  # every release target, the way CI builds them
make catalogs VERSIONS=v0.158.0
```

CI runs the tests with coverage reported on the pull request, builds every
release target, lints this repository's own example configs, and publishes
binaries and container images from tags.
