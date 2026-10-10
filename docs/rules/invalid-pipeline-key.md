# invalid-pipeline-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

A pipeline key is `<signal>` or `<signal>/<name>`, where the signal is one the
collector carries: `traces`, `metrics`, `logs` or `profiles`.

## What it reports

A pipeline whose key names no known signal — `tracez`, `trace`, `metric/1` — or
whose identifier is malformed, such as `traces/` or `logs/a b`.

An explicitly present name must be nonempty after trimming, at most 1024 bytes,
and contain no Unicode separators, control characters or symbols. Unicode
letters, digits, punctuation and additional slashes are allowed. The original
key is retained in diagnostics.

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

[`signal-support`](signal-support.md) stands down when the signal is unknown.
Names follow the Collector's
[`pipeline.ID.UnmarshalText` contract at v0.157.0](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/pipeline/pipeline.go),
also used at v0.110.0. The 1024 limit counts bytes in upstream's implementation.

## See also

- [`../../testdata/rules/invalid-pipeline-key.yaml`](../../testdata/rules/invalid-pipeline-key.yaml)
