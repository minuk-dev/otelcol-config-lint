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

No collector binary and no Go toolchain are needed at lint time. The component
schemas are fetched from the published registry, so a new collector release
becomes lintable as soon as its schema is committed — no linter upgrade. Point
`--schema-location` at a directory to run without network access.

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
- uses: minuk-dev/otelcol-config-lint@v1
  with:
    files: ./configs
    collector-version: v0.157.0
    strict: true
```

Findings land as inline pull-request annotations, because the action defaults to
`--output github`. The step passes when everything is valid, fails when a file
is invalid, and reports a usage error — a rule that does not exist, an unreadable
settings file — distinctly, with exit code 2.

Every input is the `run` flag of the same name, so the flag table below is the
whole reference: `files` (default `.`, whitespace-separated, globs allowed),
`collector-version`, `distribution`, `schema-location` (one per line to search
several in order), `strict`, `ignore-missing-schemas`, `min-severity`,
`fail-on`, `disable`, `severity`, `exclude`, `output` (default `github`),
`config`, `summary` (default `true`), `verbose` and `exit-on-error`. Only `-n`
and `--no-color` are left out: a runner gains nothing from either.

The counts come back as outputs — `exit-code`, `valid`, `invalid`, `errors`,
`skipped`, `warnings` and `infos` — so a later step can decide what to do with
them:

```yaml
- uses: minuk-dev/otelcol-config-lint@v1
  id: lint
  continue-on-error: true
  with:
    files: ./configs
    fail-on: warning
- run: echo "${{ steps.lint.outputs.invalid }} file(s) failed"
```

`@v1` follows the newest v1 release. Pin a full tag such as `@v1.2.3` to hold a
workflow to one revision of the linter. The schemas are a separate registry that
tracks its own main, so pin `schema-location` as well — at a vendored directory,
or at a tagged URL — for a run that cannot change underneath a workflow.

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
| `list versions` | the schema versions available, honouring `--schema-location` and `--distribution` |
| `version` | print the linter version (also available as `--version`) |

The flags below belong to `run`:

| Flag | Meaning |
| --- | --- |
| `--collector-version` | release to validate against, e.g. `v0.157.0` (default `latest`) |
| `--distribution` | collector binary to validate against: `core`, `contrib` (default), `k8s` or `otlp` |
| `--schema-location` | where to find schemas: a registry directory or URL, a `{{.Version}}`/`{{.Distribution}}` template, or `default`. Repeat to search several in order |
| `--output` | `text`, `json`, `junit`, `tap` or `github` |
| `--strict` | unknown component settings become errors instead of warnings |
| `--ignore-missing-schemas` | do not fail on components absent from the schema (custom distributions) |
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
otelcol-config-lint run --distribution core config.yaml           # target plain otelcol
```

### Settings file

Commit the policy instead of repeating flags. `.otelcol-config-lint.yaml` in the
working directory is picked up automatically; `--config` names another file.
Explicit flags win over the file.

```yaml
collectorVersion: v0.157.0
distribution: contrib
minSeverity: warning
strict: true
exclude:
  - "*.tmpl.yaml"
disable:
  - missing-batch
severity:
  missing-memory-limiter: warning
schemaLocations:
  - ./schemas      # this project's own schemas first
  - default         # then the published registry
```

## What it checks

`otelcol-config-lint list rules` prints the current set with default severities.

**Structure** — `unknown-top-level-key`, `service-required`,
`unknown-service-key`, `invalid-pipeline-key`, `empty-pipeline`,
`unknown-pipeline-key`, `duplicate-key`, `wrong-node-type`.

**Wiring** — `undefined-reference`, `unused-component`, `duplicate-reference`,
`connector-wiring` (a connector must export from one pipeline and receive into
another, and must not close a loop).

**Release and distribution compatibility** — `unknown-component` (with "exists
in v0.110.0 but not in v0.157.0" when a component was removed, or "not in the
core distribution; it ships in contrib, k8s" when the binary simply does not
carry it), `signal-support` (a receiver
that only does traces used in a metrics pipeline), `component-stability`,
`deprecated-component`.

**Settings** — `unknown-field`, `required-field`, `invalid-value`,
`deprecated-field`. Field schemas are read from each component's `Config` struct
and enriched with the `config.schema.yaml` upstream publishes, covering 92-96%
of components on every release. What cannot be resolved — a third-party config
such as Prometheus's own — is left open rather than reported, so partial
coverage never produces false positives. `${env:...}` expansions are left
alone.

**Practice** — `processor-order` (memory_limiter first), `missing-memory-limiter`,
`missing-batch`, `insecure-tls`.

## Schemas

Schemas are published at
[minuk-dev/otelcol-config-schemas](https://github.com/minuk-dev/otelcol-config-schemas),
one file per collector release per distribution, in both YAML (the readable
form, meant to be reviewed in pull requests) and JSON. They live in a repository
of their own because a registry grows without bound while a run reads one file
from it; keeping them here would charge every clone for schemas it never opens. They are
generated from the `metadata.yaml` that every upstream component ships, across
**both core and contrib**, split into one schema per distribution:
`contrib` (323 components for v0.157.0, the default), `core` (32), `k8s` (83)
and `otlp` (5).

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
      module: go.opentelemetry.io/collector/receiver/otlpreceiver
```

They are read over HTTP by default, so nothing needs installing or cloning. To
pin a copy, or to run without network access, point at a checkout:

```sh
otelcol-config-lint run --schema-location ../otelcol-config-schemas config.yaml
```

A location is a registry directory or URL (one holding `index.json`, laid out
as `<distribution>/<version>.<ext>`), a `{{.Version}}`/`{{.Distribution}}`
template naming a single file, or `default` for the published registry. Repeat
the flag to search several in order, so a private distribution's schema can take
precedence over the public ones, or so a vendored copy answers before the
network is tried.

### Adding a release

A distribution is described by the same [OCB builder
manifest](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder)
that builds it, so the schema lists exactly the components the binary carries.
Upstream ships one per distribution in
[`opentelemetry-collector-releases`](https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions):

```sh
# ../opentelemetry-collector-releases checked out at the release to generate
make schemas                                    # writes to ../otelcol-config-schemas
make schemas RELEASES=/path/to/opentelemetry-collector-releases \
             SCHEMAS=/path/to/otelcol-config-schemas
```

Or name a manifest directly, which is how a private distribution is covered.
A single run writes one schema, so `--out` names the file (or `-`, the default,
for stdout); `--registry` is what fills a directory with the
`<distribution>/<version>.<ext>` layout and its `index.json`:

```sh
go run ./cmd/schemagen --builder ./builder.yaml --out ./my-collector.yaml
go run ./cmd/schemagen --builder acme=./builder.yaml --registry ./schemas
```

`cmd/schemagen` downloads every module the manifest names, plus everything they
require, and writes `<distribution>/<version>.yaml` and `.json` into the schema
repository, along with the `index.json` listing them. The download goes through
the `go` command, so `GOPROXY`, `GOPRIVATE` and whatever credentials this
machine builds with apply unchanged: a **private component resolves exactly as
it does for the build that consumes it**, and a `replaces:` entry pointing at a
checkout is read from there. A component that ships no `metadata.yaml` is still
recorded, from what the manifest says, so a config naming it is not reported as
unknown.

In the registry the file name comes from the manifest -- `dist.otelcol_version`
(or `dist.version`) and `dist.name` -- so `--builder <name>=<path>` overrides
the name where the registry spells a distribution differently from upstream, as
it does for `otelcol`, which is filed as `core`.

Component renames are carried through: upstream is moving types to snake_case,
so from v0.157.0 the OTLP gRPC exporter is `otlp_grpc` with `otlp` kept as a
deprecated alias. Both resolve, and using the old name reports
`deprecated-component`.

### Keeping the registry current

Nobody has to remember. The [`schemas`](.github/workflows/schemas.yaml) workflow
runs weekly, reads upstream's release tags, and for each one the registry does
not have yet generates the schemas and opens a pull request against
`otelcol-config-schemas`, one per release. A release with no schema is not a gap
the linter reports — `--collector-version v0.158.0` falls back to the newest
release it does have and checks the config against the wrong component set — so
the registry has to keep up on its own. Dispatch the workflow with a version to
generate one by hand, present or not.

The generated diff is several megabytes of YAML, so the pull request leads with
what actually changed. `--summary` writes it: the components the release adds,
drops and renames, and the ones that crossed the beta line, which is what
`component-stability` reports on and so what changes the findings an unchanged
config gets.

```sh
go run ./cmd/schemagen --builder acme=./builder.yaml --registry ./schemas \
  --summary -
```

```
### contrib: `v0.157.0` → `v0.158.0`

4 added, 1 renamed, 2 across the beta line; 327 components in total.

**Added**

- receiver `faro`
…
```

A registry grows without bound — one file per release per distribution, in two
formats — so `--retain n` keeps only the newest `n` releases of each
distribution, and `--retain-every n` holds on to every `n`th minor for good, so
a config pinned to a round release keeps resolving after its neighbours are
dropped. Neither is on by default; the scheduled workflow sets them, a local
`make schemas` keeps everything. Pruning bounds what the registry serves and
what a checkout costs, not the history of the repository holding it.

### Field schemas

`metadata.yaml` describes components but not their settings, so those are read
from the modules themselves: every component's `Config` struct, and the
`config.schema.yaml` upstream publishes alongside it, which contributes
descriptions and enums the Go source cannot:

```yaml
fields:
  type: map
  children:
    timeout: {type: duration, doc: how long to wait before sending a batch}
    send_batch_size: {type: int}
```

References between those schemas are followed across modules, so a component's
shared settings — `sending_queue`, `retry_on_failure`, TLS, gRPC client options
— are expanded in full. What cannot be resolved stays open rather than being
reported, so partial coverage never produces false positives.

## Layout

```
cmd/otelcol-config-lint/      the linter entry point
cmd/schemagen/                the schema generator entry point
pkg/cmd/otelcol-config-lint/  the cobra command: flags, settings files, reporting
pkg/cmd/schemagen/            the generator: harvests upstream metadata into schemas
pkg/scanner/                  expands the given paths into the files to lint
pkg/sets/                     a set built on a map, in the shape of k8s.io/apimachinery
pkg/config/                   YAML parsing that keeps positions, so findings have line numbers
pkg/schema/                   schema types, version resolution and location lookup
pkg/rule/                     the rules and the registry
pkg/lint/                     the engine and the output formatters
pkg/diag/                     diagnostics, severities and positions
action.yml                    the GitHub Action; Dockerfile is the image it runs
```

## Development

```sh
make test            # go test -race with coverage
make lint            # golangci-lint
make build           # ./bin/otelcol-config-lint
make build-snapshot  # every release target, the way CI builds them
make schemas         # regenerate the schemas into ../otelcol-config-schemas
```

CI runs the tests with coverage reported on the pull request, builds every
release target, lints this repository's own example configs, exercises the
action against `testdata/`, and publishes binaries and container images from
tags. A weekly job generates the schemas for each new collector release and
opens a pull request against the schema registry.
