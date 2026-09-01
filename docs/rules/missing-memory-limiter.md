# missing-memory-limiter

**Default severity:** `info` · **Group:** [Practice](README.md#practice)

A pipeline with no `memory_limiter` has nothing bounding what the collector
holds. Under load the OOM killer is the limit, and it takes the whole process
with everything queued in it.

## What it reports

One finding per pipeline that lists no `memory_limiter` processor, on the
`processors` slot — or on the pipeline key where there is no slot to point at. A
pipeline with no receivers is skipped, since it takes in nothing to hold.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]          # under load, the OOM killer is the limit
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:18:19: info: pipeline "traces" has no memory_limiter processor [missing-memory-limiter]
    hint: add memory_limiter as the first processor to bound the collector's memory use
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
```

`memory_limiter` is the one processor this linter tells people to add, which is
why three further rules are about getting it right once it is there:
[`processor-order`](processor-order.md) puts it first,
[`memory-limiter-config`](memory-limiter-config.md) checks what upstream's own
`Validate()` checks, and
[`memory-limiter-sizing`](memory-limiter-sizing.md) checks it against the
container.

## Docs

- [`memorylimiterprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md)

## See also

- [`../../testdata/rules/missing-memory-limiter.yaml`](../../testdata/rules/missing-memory-limiter.yaml)
