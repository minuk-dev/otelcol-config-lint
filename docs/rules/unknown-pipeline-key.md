# unknown-pipeline-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

A pipeline has three slots — `receivers`, `processors`, `exporters` — and takes
no other key.

## What it reports

Any fourth key inside a pipeline. `connectors` is the common one: a connector
joins a pipeline as a receiver or an exporter, never as a slot of its own.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
      connectors: [span_metrics]     # a connector joins as receiver or exporter
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:24:7: error: unknown key "connectors" in pipeline "traces" [unknown-pipeline-key]
    hint: pipelines accept receivers, processors and exporters
```

## See also

- [`connector-wiring`](connector-wiring.md) for how a connector is wired
  instead.
- [`../../testdata/rules/unknown-pipeline-key.yaml`](../../testdata/rules/unknown-pipeline-key.yaml)
