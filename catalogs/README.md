# Component catalogs

One file per OpenTelemetry Collector release, listing every component that
release ships — **core and contrib** — with the signals it supports, its
per-signal stability, the distributions carrying it, and its Go module.

Each release is written twice with identical content:

- `<version>.yaml` — the readable form, meant to be reviewed in pull requests
- `<version>.json` — for tools that prefer JSON

Both are accepted wherever a catalog is read.

## Using them without installing anything

The linter reads catalogs straight from a URL, so this directory doubles as a
public schema registry:

```sh
otelcol-config-lint -catalog-location \
  https://raw.githubusercontent.com/minuk-dev/otelcol-config-lint/main/catalogs/{{.Version}}.yaml \
  -collector-version v0.157.0 config.yaml
```

A location may also be a local directory or a `{{.Version}}` path template, and
the flag can be repeated to search several in order — put a private
distribution's catalog first and fall back to `default` for the built-ins.

## Shape

```yaml
collectorVersion: v0.157.0
distributions: [core, contrib]
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
make catalogs VERSIONS=v0.158.0
```

Do not edit these files by hand; `tools/schemagen` rewrites them from the
upstream `metadata.yaml` files.
