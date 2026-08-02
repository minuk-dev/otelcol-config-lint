# Component schemas

One file per OpenTelemetry Collector release **per distribution**, listing every
component that release ships with the signals it supports, its per-signal
stability, the distributions carrying it, and its Go module.

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
distribution's schema first and fall back to `default` for the built-ins.

## Shape

```yaml
collectorVersion: v0.157.0
distribution: core          # which binary this file describes
distributions: [core, contrib]   # which upstream repos were harvested
components:
  receiver:
    otlp:
      type: otlp
      signals: [traces, metrics, logs, profiles]
      stability:
        traces: stable
        profiles: alpha
      distributions: [core, contrib, k8s, otlp]
      module: go.opentelemetry.io/collector/receiver/otlpreceiver
  connector:
    spanmetrics:
      type: spanmetrics
      pairs:
        - {from: traces, to: metrics}
```

Renamed components appear under both names; the legacy one carries `aliasOf`
and a `deprecated` note. Components covered by an overlay also carry a `fields`
schema — see [`../overlays`](../overlays).

## Regenerating

```sh
make schemas VERSIONS=v0.158.0
```

Do not edit these files by hand; `tools/schemagen` rewrites them from the
upstream `metadata.yaml` files.
