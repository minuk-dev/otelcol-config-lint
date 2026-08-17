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
config.yaml:34:5: error: processor "memory_limiter" sets neither limit_mib nor limit_percentage: 'limit_mib' or 'limit_percentage' must be greater than zero [memory-limiter-config]
    hint: set limit_mib to the memory the collector may use; spike_limit_mib then defaults to 20% of it
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
```

A rule that reports what the collector requires or recommends carries a `docs:`
link to the upstream page that says so, so the claim can be checked rather than
taken on trust. It is in the JSON output as `docs`, and in the GitHub
annotations.

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
`fail-on`, `default`, `enable`, `disable`, `severity`, `exclude`, `output`
(default `github`), `config`, `no-config`, `summary` (default `true`),
`verbose` and `exit-on-error`. Only `--concurrency` and `--no-color` are left
out: a runner gains nothing from either.

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

`@v1` follows the newest v1 release, and runs exactly that release's linter: the
action wraps `ghcr.io/minuk-dev/otelcol-config-lint:<release>`, pinned at a tag
and never `latest`, so a step is two small image pulls rather than a Go
toolchain and a compile. Pin a full tag such as `@v1.2.3` to hold a workflow to
one revision. The schemas are a separate registry that tracks its own main, so
pin `schema-location` as well — at a vendored directory, or at a tagged URL —
for a run that cannot change underneath a workflow.

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

The flags below belong to `run`:

Every flag mirrors a key of the settings file, and each row below names the key
it mirrors. The file is what a repository commits; a flag is how one run departs
from it.

| Flag | Settings key | Meaning |
| --- | --- | --- |
| `-c`, `--config` | — | settings file to read (default: `.otelcol-config-lint.yaml`, searched for here and in each parent) |
| `--no-config` | — | ignore any settings file and use the flags alone |
| `--collector-version` | `run.collectorVersion` | release to validate against, e.g. `v0.157.0` (default `latest`) |
| `--distribution` | `run.distribution` | collector binary to validate against: `core`, `contrib` (default), `k8s` or `otlp` |
| `--schema-location` | `run.schemaLocations` | where to find schemas: a registry directory or URL, a `{{.Version}}`/`{{.Distribution}}` template, or `default`. Repeat to search several in order |
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

`--enable`, `--disable`, `--severity`, `--exclude` and `--schema-location` take
a comma-separated list, and may also be repeated.

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
otelcol-config-lint run --default none -E invalid-value ./configs # run one rule and nothing else
otelcol-config-lint run -c ci.yaml ./configs                      # a settings file of its own
```

### Settings file

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

#### Where the file is found

`.otelcol-config-lint.yaml` — or `.otelcol-config-lint.yml` — is looked for in
the working directory and then in each parent, stopping at the directory holding
`.git`, so one file at the repository root governs a run started from any
subdirectory. `--config` names another file, and an explicitly named file that
does not exist is an error. `--no-config` runs on the flags alone. `--verbose`
prints which file was read.

Explicit flags win over the file, with one exception: the rule lists merge, so
`-D` adds to `rules.disable` rather than replacing it.

#### Choosing the rules

`rules.default` is the set to start from — `all`, which is every rule and what a
file that says nothing gets, or `none`, which runs only what `enable` names.
`disable` then takes rules out, and `severity` sets the level a rule reports at.
Naming a rule in both `enable` and `disable` is an error rather than a silent win
for one of them.

```yaml
rules:
  default: none
  enable: [unknown-component, invalid-value, undefined-reference]
```

`otelcol-config-lint list rules` prints what the policy resolves to: a rule that
will not run is listed at severity `off`.

#### Per-rule settings

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

#### Checking the settings file in an editor

`otelcol-config-lint.schema.json` is a JSON Schema for the file, so an editor
underlines a misspelled key, a severity that is not a level, and a rule name
that does not exist — the rule list in it is generated from the rules the linter
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

#### The flat form

The keys the first release put at the top level — `collectorVersion`,
`distribution`, `schemaLocations`, `strict`, `ignoreMissingSchemas`, `summary`,
`minSeverity`, `failOn`, `disable`, `severity`, `exclude` and `kubernetes` — are
still read, and folded into the blocks above. A run that finds one says which
keys to move. `output: json` is not one of them: a bare format name is still the
shorthand for `output: {format: json}`.

#### The deployment environment

`memory-limiter-sizing` compares a `memory_limiter` against the container it
runs in, which the config file cannot state. The `run.kubernetes` block
supplies it.

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
- `enabled` defaults to true when either memory number is written, since a
  block stating what the container has is a block about a container.
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

## What it checks

`otelcol-config-lint list rules` prints the current set with default severities.

**Structure** — `unknown-top-level-key`, `service-required`,
`unknown-service-key`, `invalid-pipeline-key`, `empty-pipeline`,
`unknown-pipeline-key`, `duplicate-key`, `wrong-node-type`.

**Wiring** — `undefined-reference`, `undefined-extension-reference` (an
extension a component's own settings name — an exporter's
`sending_queue.storage`, an `auth.authenticator` — that nothing declares, or
that is declared and then left out of `service.extensions`, which the collector
refuses to start on), `unused-component`, `duplicate-reference`,
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

**Practice** — `processor-order` (`memory_limiter` first, enrichment before
sampling, `batch` last), `missing-memory-limiter`,
`missing-batch`, `memory-limiter-config`, `memory-limiter-sizing`,
`batch-size-bounds`, `no-persistent-queue`, `debug-exporter-verbosity`.

**Security** — `insecure-tls`, `receiver-binds-all-interfaces`,
`debug-extension-exposed`, `hardcoded-secret`.

Every practice and security rule cites the upstream page it rests on — the
`memorylimiterprocessor`, `batchprocessor`, `exporterhelper`, `debugexporter`,
`tailsamplingprocessor` and `probabilisticsamplerprocessor` READMEs,
`configtls`, upstream's security best practices, and the Kubernetes resource
docs for the container's request and limit.

`processor-order` is about where a processor stands rather than what it is set
to. Two of its clauses are the ends of the chain: `memory_limiter` first, so
backpressure is applied before any work is done, and `batch` last, so the
processors ahead of it see individual items. The third is the middle, where the
two orders are equally valid YAML and only one of them works.

```yaml
processors: [memory_limiter, tail_sampling, k8sattributes, batch]
#                            ^^^^^^^^^^^^^ decides before the pod and namespace are on the span
```

`k8sattributes`, `resourcedetection` and `resource` add the attributes a
sampling policy is written against — as do `k8s_attributes` and
`resource_detection`, the names upstream renamed the first two to, which both
count — so a policy matching one of them from behind
`tail_sampling` or `probabilistic_sampler` matches nothing at all — no error, no
crash, just the spans it was meant to keep going missing. `tail_sampling`'s own
README states the other half of it: the processor reassembles spans into new
batches, so anything reading the request context has to have run already.
`attributes` is deliberately left out of the group, because it redacts as often
as it enriches and stripping fields from what a sampler kept is the right order
to do that in.

`memory_limiter` is the one processor this linter tells people to add, and two
of its constraints cannot be written as a field schema:
`check_interval` must be greater than zero and yet defaults to `0s`, so leaving
it out is an error rather than a choice, and `limit_mib`/`limit_percentage` are
a one-of. `memory-limiter-config` checks what upstream's own `Validate()`
checks, quoting its wording, and matches on the component type so
`memory_limiter/aggressive` is covered too.

`memory-limiter-sizing` is the other half, and it needs a number the config
cannot carry: the container's memory limit. A limiter that passes every check
above is still decoration if `limit_mib` sits at or above the container limit,
because the kernel kills the collector before the limiter ever engages. It runs
only for files the [`kubernetes` block](#the-deployment-environment) describes,
so a run that configures nothing sees nothing new.

`batch-size-bounds` is the same idea for the `batch` processor, whose
`Validate()` relates two fields a field schema only ever sees one at a time:
`send_batch_size` is the count that triggers a send, `send_batch_max_size` is a
hard cap, and a cap below the trigger is refused at startup. The names read
backwards from the semantics, so capping at something reasonable-looking like
`1000` while leaving `send_batch_size` at its default of `8192` is exactly the
shape that fails — and since nobody wrote the 8192, the finding says it is the
default rather than quoting it back as if they had. It also reports a negative
`timeout` and a key repeated in `metadata_keys`, which are compared
case-insensitively, so `tenant` and `Tenant` are one key. Both sizes are
`uint32` upstream, a range the published field schemas flatten to `int`, so a
negative or over-large count is reported here too — it fails to load rather than
to validate, and saying so beats hinting at a fix that still will not start.

`missing-batch` reports a pipeline that batches nothing on its way out, and
there are now two ways not to be one. `exporterhelper` batches inside the
sending queue, under `sending_queue.batch`, which sits behind the queue and the
retry logic rather than in front of them; the `batch` processor
[drops the data in a batch that fails to send](https://github.com/open-telemetry/opentelemetry-collector/issues/12443),
and the queue batcher does not. That is why the hint leads with the exporter
setting and names the processor as what also works. The processor is not
deprecated — upstream has explicitly not decided that — so a pipeline that uses
it is a pipeline that batches, and nothing here says to take it out.

```yaml
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue:
      batch:                   # off by default; writing the block turns it on
        flush_timeout: 200ms
```

A pipeline whose exporters all do that gets no finding: it batches, and a
`batch` processor added on top would be a second layer with a flush timing of
its own. Where only some of them do, the pipeline is under-batched on the legs
that do not, so the finding names the exporter and sits on the line that wires
it in — that exporter's settings are where the fix is written. A connector in
the exporter slot is not a leg out of the collector and is not counted; it feeds
another pipeline, which has exporters of its own. An exporter whose settings
come from a merge key or an expansion counts as unknown rather than as not
batching, for the same reason the field rules leave what they cannot resolve
alone.

What the document writes decides whether there is a finding; what the field
schema describes decides only which fix the hint may name. The queue batcher is
off until something turns it on, so a config that writes no
`sending_queue.batch` is not batching there whatever the schema knows about the
type — which is why an exporter it describes no settings for, `nop` and
`datadog` among them, is reported like any other rather than passed over in
silence.

The hint is the half that follows the schema, and it asks for
`sending_queue.batch` itself rather than for the queue holding it. The batcher
is younger than the queue: on a release from before it moved in, every exporter
has a `sending_queue` and none of them has a `batch` under it, and a hint naming
one there would be advising a setting the collector rejects on startup —
`unknown-field`, reading the same schema, would say so in the same run. So
where the field is not there to write — an exporter with no `sending_queue` on
the targeted release, `debug` on `v0.157` among them; any exporter on a release
that predates the batcher; anything the schema does not describe, `nop` and
`datadog` among them — the hint names the processor instead, because a fix the
release has no setting for is no fix. Which exporters those are is the schema's
answer and moves with the release, which is the point of asking it. Where one finding covers several exporters
at once it names the queue batcher only if all of them accept it; where they
disagree it names the processor and says that only some of them can take a
`sending_queue.batch`, since the reason it gives otherwise would be false of
half of them.

`no-persistent-queue` is the one opinionated rule, and it reports at `info` for
that reason. The sending queue is on by default and lives in memory, so a
deploy, an OOM kill or a node drain takes everything still in it — and nothing
in the config says so, because an exporter with a `storage` extension and one
without look identical until the restart.

```yaml
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage    # without this, a restart drops the queue
```

Persistence takes three steps — a storage extension declared, listed in
`service.extensions`, and named here — which is why the finding names all three
and why `undefined-extension-reference` above is worth having alongside it. The
The rule stays quiet on `sending_queue.enabled: false`, on an exporter no
pipeline references, and on exporters whose field schema describes their
settings and has no queue among them, which is what keeps `debug` out of it.
Where the schema resolved nothing for a component — the `datadog` exporter and
`nop` share that — a queue the config writes is the only evidence there is, and
it is taken at face value.

It reports once per exporter, not once per config: the fix is written per
exporter, each queue names its own storage, and a finding without a position
cannot say which of eight exporters to edit. It is also the rule most likely to
be unwanted — a sidecar next to an application that can re-send, or an agent
whose source keeps its own buffer, is right not to want a writable volume. There
is deliberately no default-disabled rule concept for it: `info` is already below
`--min-severity warning`, which is a normal way to run, and `disable:
[no-persistent-queue]` in the settings file turns it off for good. A second
mechanism that means roughly the same thing would only be another place to look.

`debug-exporter-verbosity` catches the exporter somebody added to find out why
an export was failing and never took out again. It prints what it is given to
the collector's own log, and at `verbosity: detailed` that is every field of
every span, data point and record in the pipeline — the collector spends its
time formatting log lines, and the log backend receives a second copy of all the
telemetry the collector was meant to be forwarding.

```yaml
exporters:
  debug:
    verbosity: detailed    # a line per record, not per batch
service:
  pipelines:
    traces:
      exporters: [otlp, debug]    # and it ships to production like this
```

That is the `warning`. Below it, at `info`, is one quiet note for a `debug`
exporter at any verbosity: `basic` costs a line per batch rather than per
record, which is cheap enough not to be a warning, but it is still a diagnostic
tool left running, and upstream keeps no stable output format, so nothing
downstream should be parsing it either. Both clauses match on the type, so
`debug/verbose` counts, and both report per pipeline that references the
exporter — that is what the message names and where the reader takes it out of.
An exporter no pipeline references prints nothing and is `unused-component`'s to
report; the `logging` exporter that came before it is `deprecated-component`'s.
`warning` rather than `error` is deliberate: a deliberate debug run is
legitimate and should not fail a CI job, and `--severity
debug-exporter-verbosity=error` is there for anyone who wants it fatal.

`receiver-binds-all-interfaces` is the one upstream's [config best
practices](https://opentelemetry.io/docs/security/config-best-practices/) lead
with, and the most common way a collector ends up reachable from more of the
network than anyone meant:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317   # every interface, not just the one you meant
```

The collector changed its own default to `localhost` in `v0.110.0` for exactly
this reason. The example configs the ecosystem copies from did not — the ones on
opentelemetry.io say outright that they use the unspecified address "as a
convenience" — and those get pasted into production. Every spelling of it is
matched: `0.0.0.0`, `[::]`, a bare `:4317`, and `:::4317`, which is what netstat
prints for an IPv6 listener and which Go refuses outright, so a collector handed
one does not start at all.

Only endpoints something *listens* on are read, and the split is by kind:
receivers and extensions bind, while an exporter's or a connector's `endpoint`
names somewhere to send to. `0.0.0.0` there is also wrong, but it is a different
mistake with a different fix, and a bind-address hint on a line about a backend
would be worse than silence. Within a component the walk is by key name rather
than by a table of where each type keeps its address, since `otlp` writes one per
protocol and most others write one at the top; each is reported separately,
because each is its own line to edit. Two key names are read, not one: the
stanza-based log receivers call it `listen_address` — `tcplog`, `udplog`, and
`syslog` under each of its `tcp` and `udp` blocks — and reading only `endpoint`
would leave every one of them unreported. A receiver no pipeline names and an
extension `service.extensions` leaves out are both skipped — neither is ever
instantiated, so neither binds anything — as is an endpoint nobody wrote, which
takes the component's own default. `${env:MY_POD_IP}:4317` is the fix, so it is
not a finding; `0.0.0.0:${env:PORT}` still is, since the address says plainly
who can reach it and only the port is left open.

The hint carries the fix rather than only the objection: bind `127.0.0.1` when
every client is local, and in Kubernetes write `${env:MY_POD_IP}`, taking the pod
IP from the downward API. It reports at `warning`, not `error` — a gateway behind
a service mesh with its own network policy binds every interface deliberately.
`health_check` and `healthcheckv2` are left out for the same reason
`debug-extension-exposed` leaves them out, below, and `pprof` and `zpages` are
that rule's to report.

`debug-extension-exposed` rests on upstream's [security guidance](https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/security-best-practices.md),
which is direct about avoiding "exposing health or telemetry data outside the
Collector by default", and the debugging extensions are the ones that do it.
`pprof` serves heap profiles, goroutine dumps and the collector's command line;
`zpages` serves live traces and pipeline internals.
This is narrower than the general bind-address check above, and separate from it
because the consequence differs in kind: a `0.0.0.0` OTLP receiver accepts data
it should not, while a `0.0.0.0` pprof endpoint *hands out* process internals.

```yaml
extensions:
  pprof:
    endpoint: 0.0.0.0:1777     # warning: heap profiles, on every interface
  zpages:
    endpoint: 10.0.4.7:55679   # info: deliberate, but that network can read it
```

The two cases are reported apart because they have different fixes. An
unspecified address — `0.0.0.0`, `[::]`, a bare `:1777` — is reachable from
every interface the host has and reports at `warning`; a specific non-loopback
address was chosen by someone and reports at `info`, saying only that the
network it sits on can read the collector's internals. Both are matched on
component type, so `zpages/internal` is covered.

`health_check` and `healthcheckv2` are deliberately left out. A Kubernetes
liveness probe comes from the kubelet, off the container's loopback interface,
so a health check bound to `0.0.0.0` is what a correct deployment looks like —
and a rule that fires on every correct config is a rule people disable, which
would take `pprof` down with it.

The rule only reports extensions `service.extensions` actually lists, since
that is the only thing that starts one — a declaration left out of it binds no
port, and `unused-component` is what has something to say about it. An endpoint
nobody wrote is left alone too: it takes the extension's own default, which has
been `localhost` for both since upstream made `UseLocalHostAsDefaultHost` the
default in `v0.110`. So is a hostname other than `localhost`, since what a name
resolves to is a property of the network rather than of the file, and an
address supplied by `${env:...}`. An expansion standing in for only the *port*
is still reported — `0.0.0.0:${env:PPROF_PORT}` says plainly who can reach it —
and an endpoint a `<<` merge supplies is read from the mapping it merges, so
sharing one debugging block between extensions does not hide it.

`hardcoded-secret` is the one rule about the file rather than the collector.
Configs live in git, and exporter credentials are written in the same file as
everything else:

```yaml
exporters:
  otlp:
    endpoint: backend:4317
    headers:
      authorization: Bearer ${env:OTLP_TOKEN}   # not the literal token
```

Upstream's guidance is to keep sensitive values in a secret store or on an
encrypted filesystem and pull them in with an expansion, and CI is the last
moment before the value reaches a remote. The rule walks every declared
component — wired or not, since a credential in the repository has already been
handed over — and reports a scalar whose key names a credential (`password`,
`token`, `api_key`, `secret`, `credential`, `private_key`, `key_pem`,
`access_key`, `passphrase`, matched as a case-insensitive substring, so
`sasl_password` is covered) and whose value is written out in full. A list is
matched by the key that named it, so `api_keys: [AKIA…]` is reported too, and
under `headers:` so are `authorization` and any value opening with `Bearer` or
`Basic`.

**It never prints the value**, only the path: copying the secret into the CI log
is the one thing this rule must not do. False positives are the risk it carries,
so it gives up findings freely. `${env:...}` and `${file:...}` are the fix and
say so; empty values and placeholders (`changeme`, `<your-token>`, `REPLACE_ME`,
`none`) are a config with no credential in it yet, behind an auth scheme as much
as on their own; a boolean is a switch rather than a credential, whatever the
setting is called; and a key naming *where* a credential lives rather than
holding one — `private_key_file`, `token_url`, `cert_pem` — is a correctly
configured component, so keys ending in `_file`, `_path`, `_url`, `_uri` and
`_name` are excluded before anything else. It reports at `warning`, because a
local config with a dummy credential is legitimate and this rule will meet
plenty of them.

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
go run ./cmd/schemagen generate --builder ./builder.yaml --out ./my-collector.yaml
go run ./cmd/schemagen generate --builder acme=./builder.yaml --registry ./schemas
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
go run ./cmd/schemagen generate --builder acme=./builder.yaml --registry ./schemas \
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
pkg/rule/                     what a rule is: the interface, the context and the shared readers
pkg/rule/<rule-name>/         one rule and its tests, one package each
pkg/rule/ruletest/            the fixtures a rule's tests are written against
pkg/ruleset/                  the registry: every rule collected into one set
pkg/lint/                     the engine and the output formatters
pkg/diag/                     diagnostics, severities and positions
pkg/quantity/                 Kubernetes memory quantities, parsed and printed back
pkg/version/                  the linter's own version, stamped at build time
testdata/rules/               one invalid config per rule, with the run that shows it
action.yml                    the GitHub Action; Dockerfile wraps the released image it runs
build/docker/Dockerfile       the distroless linter image releases publish
```

## Adding a rule

A rule is a package. `pkg/rule` defines what one is -- the `Rule` interface, the
`Context` a check reads, the `Finding` it reports -- along with the YAML readers
and phrasing helpers every rule shares. `pkg/ruleset` is the only place that
knows about all of them, which is what keeps a rule free to import the
vocabulary it is written in.

So a new rule is one directory and one line:

```go
// pkg/rule/mynewrule/rule.go
package mynewrule

// New builds the rule.
func New() rule.Rule {
	return myNewRule{rule.NewBase("my-new-rule",
		"one line saying what this reports", diag.Warning)}
}

type myNewRule struct{ rule.Base }

func (r myNewRule) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		ctx.Report(rule.Finding{
			Node: p.KeyNode, Path: "service.pipelines." + p.Key,
			Message: "pipeline " + rule.Quote(p.Key) + " is doing the thing",
			Hint:    "stop doing the thing",
		})
	}
}
```

Then add `mynewrule.New()` to the list in `pkg/ruleset/ruleset.go`, and write
`pkg/rule/mynewrule/rule_test.go` against `pkg/rule/ruletest`, which carries the
stand-in schema and the clean config every rule's tests start from:

```go
found, err := ruletest.Run(mynewrule.New(), src)
```

A test that is about two rules meeting -- one reporting where the other stays
quiet -- belongs in `pkg/ruleset` instead, which is where the whole set is run
at once.

Last, the rule needs a config in `testdata/rules`: `my-new-rule.yaml`, which
breaks it, with a comment naming the rule on the line that breaks it, and
`my-new-rule.settings.yaml`, which says which rules the run has on in the shape
golangci-lint uses:

```yaml
rules:
  enable: [my-new-rule]  # must report on my-new-rule.yaml
  disable: []            # switched off for this run, and must stay quiet
  settings: {}           # per-rule options, keyed by rule name
```

The tests refuse a rule with no fixture, so this is where a rule is shown
working through the real command line, the published schemas and the severity
gate rather than against the stand-in schema. `testdata/rules/README.md` has the
rest of the schema, including how a fixture selects its own collector release or
states the container it runs in.

## Development

```sh
make test            # go test -race with coverage
make lint            # golangci-lint
make build           # ./bin/otelcol-config-lint
make build-snapshot  # every release target, the way CI builds them
make schemas         # regenerate the schemas into ../otelcol-config-schemas
```

Nothing injects the version. The Go toolchain records the module version and
the repository state in every binary it builds, and `pkg/version` reads that
back, so a tagged build reports its tag because it was built from that tag:

| built from | `otelcol-config-lint version` |
| --- | --- |
| a tag | `v1.2.3`, or `v1.2.3+dirty` from a modified tree |
| `go install ...@v1.2.3` | `v1.2.3` |
| a commit between tags | `b7dbdd5`, or `b7dbdd5-dirty` |
| no repository, or `go run` | `devel` |

CI runs the tests with coverage reported on the pull request, builds every
release target, lints this repository's own example configs, exercises the
action against `testdata/`, and publishes binaries and container images from
tags. A weekly job generates the schemas for each new collector release and
opens a pull request against the schema registry.

Releasing is bump then tag, because the action's image is pinned at the release
it ships in and a tag cannot reference an image built from itself:

```sh
make release-pin RELEASE=v1.2.3
git commit -am 'chore(release): pin the action at v1.2.3'
git tag v1.2.3 && git push origin main v1.2.3
```

The release workflow refuses a tag whose pin names another release, and moves
`v1` onto the release once it has published.
