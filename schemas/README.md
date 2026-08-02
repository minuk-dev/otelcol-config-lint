# Component schemas

One file per OpenTelemetry Collector release **per distribution**, listing every
component that release ships with the signals it supports, its per-signal
stability, the settings it accepts, and its Go module.

```
schemas/
  index.json                     what this registry can serve
  contrib/v0.157.0.{yaml,json}   323 components — the default
  core/…                          32
  k8s/…                           83
  otlp/…                           5
```

Each holds only what that binary ships, so a config using `filelog` resolves
against `contrib` and not against `core`. There is deliberately no merged
schema: it would be exactly these put back together, no collector ships it,
and checking against one could only ever hide a component the binary lacks.

`contrib` is the default, being the distribution most collectors are built
from and the widest published one.

Each schema is written twice with identical content:

- `<version>.yaml` — the readable form, meant to be reviewed in pull requests
- `<version>.json` — for tools that prefer JSON

Both are accepted wherever a schema is read.

`index.json` records the distributions and versions available. A local
directory could be listed instead, but a remote registry cannot, so it publishes
the answer as a file:

Coverage differs between distributions, so versions are listed per
distribution — upstream had no `otlp` distribution before v0.120.0:

```json
{"distributions": {"contrib": ["v0.157.0", "…", "v0.110.0"],
                   "otlp":    ["v0.157.0", "…", "v0.120.0"]}}
```

## Using them without installing anything

The linter reads schemas straight from a URL, so this directory doubles as a
public schema registry. Point at the directory itself — the linter appends the
distribution and version, and reads `index.json` to know what is available:

```sh
otelcol-config-lint run --schema-location \
  https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/schemas \
  --collector-version v0.157.0 config.yaml
```

A location may also be a local directory or a path template naming one file,
where `{{.Version}}` and `{{.Distribution}}` are substituted:

```sh
otelcol-config-lint run --schema-location 'schemas/{{.Distribution}}/{{.Version}}.json' config.yaml
```

A directory without an `index.json` is read as the flat `<version>.<ext>` layout
used before schemas were split by distribution.

The flag can be repeated to search several locations in order — put a private
distribution's schema first and fall back to `default` for this registry.

The distribution is recorded once, at the top of the file. Components carry no
membership of their own: the file they sit in already answers that, and a second
answer could only disagree with it.

## Shape

```yaml
collectorVersion: v0.157.0
distribution: core                 # which binary this file describes
sources:                           # which upstream repos were harvested
  core: go.opentelemetry.io/collector
  contrib: github.com/open-telemetry/opentelemetry-collector-contrib
components:
  receiver:
    otlp:
      type: otlp
      signals: [traces, metrics, logs, profiles]
      stability:
        traces: stable
        profiles: alpha
      module: go.opentelemetry.io/collector/receiver/otlpreceiver
  connector:
    spanmetrics:
      type: spanmetrics
      pairs:
        - {from: traces, to: metrics}
```

Renamed components appear under both names; the legacy one carries `aliasOf`
and a `deprecated` note.

Components also carry a `fields` schema describing the settings they accept.
It is read from the component's `Config` struct — the `mapstructure` tags name
the settings, and embedded types are followed across both upstream repositories
— then enriched with the `config.schema.yaml` upstream publishes, which
contributes descriptions and enum constraints the sources cannot.

The sources decide the shape rather than the published schemas, because those
are lossy in a way that matters: an embedded field carrying a name, such as
``configretry.BackOffConfig `mapstructure:"retry_on_failure"` ``, is emitted as
an `allOf` that merges those settings into the parent and loses the key they
live under. Taken alone it would report a valid `retry_on_failure:` as unknown.

**Every file here stands on its own** — all references are resolved at
generation time, so nothing carries a `$ref` or needs a second file to be read.
What cannot be resolved, a third-party config such as Prometheus's own or a
reference cycle in the stanza operators, is left `open` so its keys go
unchecked rather than being reported as unknown.

Coverage is 92-96% of components on every release. The hand-written overlays in
[`../overlays`](../overlays) are applied last and override both sources.

## Regenerating

```sh
make schemas VERSIONS=v0.158.0
```

Do not edit these files by hand; `tools/schemagen` rewrites them from the
upstream `metadata.yaml` files.
