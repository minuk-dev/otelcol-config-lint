# duplicate-reference

**Default severity:** `warning` · **Group:** [Wiring](README.md#wiring)

The same component listed twice in one pipeline slot. The collector deduplicates
it, so the second entry does nothing.

## What it reports

The second and any later occurrence of a component id within one slot of one
pipeline. Listing the same component in two different pipelines is normal and
never reported.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp, otlp]      # the second entry adds nothing
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:21:25: warning: "otlp" is listed twice in traces.receivers [duplicate-reference]
```

It is usually a merge gone wrong, or a processor somebody meant to name once
with a different suffix — `batch` and `batch/large` are two components, `batch`
and `batch` are one.

## See also

- [`../../testdata/rules/duplicate-reference.yaml`](../../testdata/rules/duplicate-reference.yaml)
