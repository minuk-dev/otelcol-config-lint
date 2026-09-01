# unknown-service-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

`service` accepts `extensions`, `pipelines` and `telemetry`, and nothing else.

## What it reports

Any other key inside the `service` block, with the nearest accepted key as a
hint where one is close.

## Example

```yaml
service:
  telemetrey:          # "telemetry" misspelt
    logs:
      level: debug
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:19:3: error: unknown key "telemetrey" in service [unknown-service-key]
    hint: service accepts extensions, pipelines and telemetry; did you mean "telemetry"?
```

A misspelt `telemetry` is the case this is for: the collector starts, the block
is never read, and the debug logging somebody switched on never appears.

## See also

- [`unknown-top-level-key`](unknown-top-level-key.md),
  [`unknown-pipeline-key`](unknown-pipeline-key.md) — the same check at the
  other two levels.
- [`../../testdata/rules/unknown-service-key.yaml`](../../testdata/rules/unknown-service-key.yaml)
