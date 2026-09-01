# signal-support

**Default severity:** `error` · **Group:** [Release compatibility](README.md#release-and-distribution-compatibility)

A component wired into a pipeline whose signal it does not carry — a
traces-only receiver in a metrics pipeline. The collector refuses to start.

## What it reports

Every reference in every pipeline whose component does not support that
pipeline's signal, with the signals it does support as the hint. Connectors are
read directionally: the exporter side has to consume the pipeline's signal, the
receiver side has to produce it.

## Example

```yaml
receivers:
  jaeger:
service:
  pipelines:
    metrics:
      receivers: [jaeger]          # the jaeger receiver carries traces only
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:18:19: error: receiver "jaeger" does not support metrics in pipeline "metrics" [signal-support]
    hint: it supports: traces
```

## Notes

Which signals a component supports comes from the release
[schema](../schemas.md), so this rule stands down when none resolved. It also
stands down where another rule has the better finding: a pipeline
[`invalid-pipeline-key`](invalid-pipeline-key.md) reports has no signal to check
against, an [`undefined-reference`](undefined-reference.md) has no declaration,
and an [`unknown-component`](unknown-component.md) has no schema entry.

## See also

- [`../../testdata/rules/signal-support.yaml`](../../testdata/rules/signal-support.yaml)
