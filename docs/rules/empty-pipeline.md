# empty-pipeline

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

Every pipeline needs at least one receiver and one exporter. Processors are
optional; the two ends are not.

## What it reports

One finding per missing end, on the slot where it is missing — a pipeline with
neither gets two.

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
service:
  pipelines:
    traces:            # no exporters: what this receives goes nowhere
      receivers: [otlp]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:9:5: error: pipeline "traces" has no exporters [empty-pipeline]
```

## See also

- [`../../testdata/rules/empty-pipeline.yaml`](../../testdata/rules/empty-pipeline.yaml)
