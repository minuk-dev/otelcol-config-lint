# invalid-pipeline-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

A pipeline key is `<signal>` or `<signal>/<name>`, where the signal is one the
collector carries: `traces`, `metrics`, `logs` or `profiles`.

## What it reports

A pipeline whose key names no known signal — `tracez`, `trace`, `metric/1`. The
name after the slash is free-form and never reported.

## Example

```yaml
service:
  pipelines:
    tracez:            # not a signal
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:20:5: error: pipeline "tracez" does not name a known signal [invalid-pipeline-key]
    hint: pipeline keys look like traces, metrics/internal or logs/2; did you mean "traces"?
```

## Notes

[`signal-support`](signal-support.md) stands down for a pipeline this rule
reports: without a signal there is nothing to check a component against.

## See also

- [`../../testdata/rules/invalid-pipeline-key.yaml`](../../testdata/rules/invalid-pipeline-key.yaml)
