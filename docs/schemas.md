# Schemas

Every rule that knows anything about a collector release reads it from a schema.
Schemas are published at
[minuk-dev/otelcol-config-schemas](https://github.com/minuk-dev/otelcol-config-schemas),
one file per collector release per distribution, in both YAML (the readable
form, meant to be reviewed in pull requests) and JSON.

They live in a repository of their own because a registry grows without bound
while a run reads one file from it; keeping them in the linter would charge
every clone for schemas it never opens.

They are generated from the `metadata.yaml` that every upstream component ships,
across **both core and contrib**, split into one schema per distribution:
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

## Where they are read from

Over HTTPS by default, so nothing needs installing or cloning. To pin a copy, or
to run without network access, point at a checkout:

```sh
otelcol-config-lint run --schema-location ../otelcol-config-schemas config.yaml
```

A location is one of:

- a registry directory or URL — one holding `index.json`, laid out as
  `<distribution>/<version>.<ext>`, optionally with the `components.json`
  described [below](#which-releases-have-a-component);
- a `{{.Version}}`/`{{.Distribution}}` template naming a single file;
- `default`, for the published registry.

Repeat the flag to search several in order, so a private distribution's schema
can take precedence over the public ones, or so a vendored copy answers before
the network is tried.

A remote location must be `https://`. The schema is what every rule reasons from
— which components exist, which settings they take — so anyone able to rewrite
one in flight decides what the linter reports. A plain `http://` location is
refused unless `--insecure-schema-location` says otherwise, which is there for a
registry served on localhost. One download is capped at 32 MiB, so a registry
that is hostile or merely broken cannot stream the linter out of memory.

## Which releases have a component

An unknown component is worth explaining — `"logging" exists in v0.110.0 but not
in v0.157.0` says something a "did you mean" cannot — but the question is about
every release at once, and a schema describes one. Answering it by reading them
is a multi-megabyte download per release, tens of them, to produce a single line
of hint, and against a rate limit that counts requests it is the shape most
likely to be throttled.

So the registry publishes `components.json` beside the index: the releases each
component type is shipped in, per distribution, as one document read in one
request. It is only read by a run that actually meets an unknown component, and
it is written as spans (`from`, and `to` once the component is gone), so a new
release does not rewrite the entry of everything it left alone.

A registry publishing none is still read the old way, one schema per release —
without limit for a directory on disk, and no further back than the twelve
newest releases over the network, which are the ones a hint is usually about. A
walk that lost half its releases to a throttled registry reports nothing rather
than naming whichever release happened to answer: a hint that is quietly wrong
is worse than no hint at all.

## Caching

A schema describes one release of one distribution, so what it says under a
version does not change. Fetched schemas are kept between runs, under
`$XDG_CACHE_HOME/otelcol-config-lint` (the platform's own cache directory when
the environment names none), and a second run reads them from there without
asking the registry. The index, which grows a line per release, is offered back
with its ETag instead, so an up-to-date run pays a `304` rather than the file.

`--no-cache` reads nothing and keeps nothing. The registry tracks its own main,
so a schema can be corrected under a version this machine has already read; that
is when to reach for it. Deleting the directory has the same effect once.

A throttled or briefly failing registry is asked again — three attempts, waiting
as long as a `Retry-After` asks for and backing off from half a second otherwise
— so one `429` does not fail a lint. A `404` is not retried: it means the
registry does not carry that version, which waiting will not change.

## Keeping the registry current

Nobody has to remember. The
[`schemas` workflow](https://github.com/minuk-dev/otelcol-config-schemas/blob/main/.github/workflows/schemas.yaml)
runs weekly **in the `otelcol-config-schemas` repository**, not in this one: it
reads upstream's release tags, and for each one the registry does not have yet
generates the schemas and opens a pull request there, one per release. It takes
`cmd/schemagen` from a public checkout of this repository, which needs no
credential, while its writes are to the registry that `GITHUB_TOKEN` already
covers — run the other way round it would need a personal access token with
write access to the registry, stored as a secret and rotated by hand.

A release with no schema stops a lint — `--collector-version v0.158.0` is a
usage error naming the newest release the registry does have — so the registry
has to keep up on its own. Dispatch the workflow with a version to generate one
by hand, present or not.

The generated diff is several megabytes of YAML, so the pull request leads with
what actually changed. `--summary` writes it: the components the release adds,
drops and renames, the ones that crossed the beta line, which is what
[`component-stability`](rules/component-stability.md) reports on and so what
changes the findings an unchanged config gets, and how many are [left
open](#field-schemas) — the components no unknown setting is reported for, which
is the one part of a schema that is a gap rather than a statement.

```sh
go run ./cmd/schemagen generate --builder acme=./builder.yaml --registry ./schemas \
  --summary -
```

```
### contrib: `v0.157.0` → `v0.158.0`

4 added, 1 renamed, 2 across the beta line; 327 components in total.

12 of them are left open: their settings are not fully described, so
`unknown-field` does not check them.

**Added**

- receiver `faro`
…
```

## Regenerating a release the registry already has

A schema is regenerated in place when the generator learns to describe
something differently, which is a correction to every release already published
rather than to the next one — the weekly run only ever looks forward, so it
will not do this on its own. Dispatch the workflow with the versions to redo,
oldest first; `base` stacks one batch on the last so a long backfill arrives as
a few reviewable pull requests instead of one.

The summary of such a run compares the release **against itself**, not against
the release before it: what there is to review is what this run changed about
it. So a backfill reads as the list of components that moved:

```
### contrib: `v0.153.0` regenerated

1 left open; 284 components in total.

1 of them is left open: its settings are not fully described, so
`unknown-field` does not check it.

**Left open**

- receiver `hostmetrics`
```

Comparing against the previous release would answer the other question, and in
a batch generating oldest first it would answer it against a file the same run
had just rewritten, so the change being made would appear nowhere.

## Adding a release by hand

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

Or name a manifest directly, which is how a private distribution is covered. A
single run writes one schema, so `--out` names the file (or `-`, the default,
for stdout); `--registry` is what fills a directory with the
`<distribution>/<version>.<ext>` layout and its `index.json`:

```sh
go run ./cmd/schemagen generate --builder ./builder.yaml --out ./my-collector.yaml
go run ./cmd/schemagen generate --builder acme=./builder.yaml --registry ./schemas
```

`cmd/schemagen` downloads every module the manifest names, plus everything they
require, and writes `<distribution>/<version>.yaml` and `.json` into the schema
repository, along with the `index.json` listing them. The index also records the
extension each distribution's schemas should be fetched with, so reading one
over the network costs a single request instead of probing `.yaml`, `.yml` and
`.json` in turn; a distribution whose releases do not agree on one form names
none, and is probed as before. `components.json` is written beside it, from the
schemas the registry is left holding, so that [an unknown component can be
placed](#which-releases-have-a-component) without downloading the registry; a
distribution whose releases could not all be read is left out of it rather than
described from half of them.

The download goes through the `go` command, so `GOPROXY`, `GOPRIVATE` and
whatever credentials this machine builds with apply unchanged: a **private
component resolves exactly as it does for the build that consumes it**, and a
`replaces:` entry pointing at a checkout is read from there. A component that
ships no `metadata.yaml` is still recorded, from what the manifest says, so a
config naming it is not reported as unknown.

In the registry the file name comes from the manifest — `dist.otelcol_version`
(or `dist.version`) and `dist.name` — so `--builder <name>=<path>` overrides the
name where the registry spells a distribution differently from upstream, as it
does for `otelcol`, which is filed as `core`.

Component renames are carried through: upstream is moving types to snake_case,
so from v0.157.0 the OTLP gRPC exporter is `otlp_grpc` with `otlp` kept as a
deprecated alias. Both resolve, and using the old name reports
[`deprecated-component`](rules/deprecated-component.md).

A registry grows without bound — one file per release per distribution, in two
formats — so `--retain n` keeps only the newest `n` releases of each
distribution, and `--retain-every n` holds on to every `n`th minor for good, so
a config pinned to a round release keeps resolving after its neighbours are
dropped. Neither is on by default; the scheduled workflow sets them, a local
`make schemas` keeps everything. Pruning bounds what the registry serves and
what a checkout costs, not the history of the repository holding it.

## Field schemas

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
reported, so partial coverage never produces false positives. Coverage runs at
92–96% of components on every release.

That holds for a component resolved only in part, too. A `Config` that decodes
itself — it has its own `Unmarshal(*confmap.Conf)` — and keeps a field
mapstructure cannot fill accepts settings no tag names: the hostmetrics
receiver declares `Scrapers` as `mapstructure:"-"` and reads the `scrapers`
section by hand, and the receiver creator holds its `receivers` the same way.
The settings that were resolved are still recorded and still checked; the
mapping is left open, because presenting half of a component's settings as all
of them is what turns a coverage gap into a false positive.

A published `config.schema.yaml` can close such a mapping again, but only where
it describes the hand-read section. Upstream derives those files from the same
mapstructure tags, so a section read by hand is missing from both: hostmetrics
has published one since v0.145.0 that lists `root_path` and
`metadata_collection_interval`, and says nothing of `scrapers` until v0.154.0.
A published schema settles openness only where it names a key the sources never
resolved, which is the evidence that it describes more than the tags do; one
that adds nothing has not looked into that section either, and the mapping
stays open.

An open component is a check that does not run: `unknown-field` lets every
setting through for it, a typo in one the generator *did* resolve included. That
is the right trade against reporting a valid config as wrong, but it is silent,
so `--summary` counts the components a release leaves open — see [keeping the
registry current](#keeping-the-registry-current).

This is what [`unknown-field`](rules/unknown-field.md),
[`required-field`](rules/required-field.md),
[`invalid-value`](rules/invalid-value.md) and
[`deprecated-field`](rules/deprecated-field.md) read.

A setting that decodes into a `component.ID` is marked, since nothing in the
value says so — a storage id and a directory path are both strings:

```yaml
sending_queue:
  type: map
  children:
    storage: {type: string, extensionRef: storage}
```

That marker is what
[`undefined-extension-reference`](rules/undefined-extension-reference.md) reads,
so which settings name an extension is derived from the sources rather than
maintained as a list in the linter. The role — `storage`, `auth`, or a plain
`extension` for a key the generator has no name for — is only what the
diagnostic calls it. Schemas generated before the marker existed keep working:
the rule still knows `sending_queue.storage` and `auth.authenticator` on its
own, and that fallback falls away as the registry is regenerated.
