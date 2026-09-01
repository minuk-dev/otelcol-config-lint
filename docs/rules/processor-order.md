# processor-order

**Default severity:** `warning` · **Group:** [Practice](README.md#practice)

Processors run in the order they are listed, and some of them have exactly one
place they work. This rule is about where a processor stands rather than what it
is set to: the config loads, the collector runs, and what comes out the far end
is not what the author asked for.

## What it reports

Three clauses — the two ends of the chain, and the middle.

| Clause | Severity | Why |
| --- | --- | --- |
| `memory_limiter` is not first | `warning` | backpressure has to be applied before any work is done |
| `batch` is not last | `info` | processors behind it see whole batches rather than individual items |
| enrichment runs after sampling | `warning` | the sampler decides before the attributes its policies match are there |

All three match on component *type*, so `memory_limiter/aggressive` and
`tail_sampling/errors` are covered.

## The middle clause

This is the one that is not obvious from reading the file:

```yaml
processors: [memory_limiter, tail_sampling, k8sattributes, batch]
#                            ^^^^^^^^^^^^^ decides before the pod and namespace are on the span
```

`k8sattributes`, `resourcedetection` and `resource` add the attributes a
sampling policy is written against — as do `k8s_attributes` and
`resource_detection`, the names upstream renamed the first two to, which both
count. A policy matching one of them from behind `tail_sampling` or
`probabilistic_sampler` matches nothing at all: no error, no crash, just the
spans it was meant to keep going missing.

`tail_sampling`'s own README states the other half of it — the processor
reassembles spans into new batches, so anything reading the request context has
to have run already.

`attributes` is deliberately left out of the enrichment group. It redacts as
often as it enriches, and stripping fields from what a sampler kept is the right
order to do that in, so including it would report a correct config as often as a
wrong one.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch, memory_limiter]     # the limiter runs after the batch it protects
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:23:27: warning: memory_limiter is processor 2 in pipeline "traces"; it must be first [processor-order]
    hint: move "memory_limiter" to the front of service.pipelines.traces.processors
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
```

## Docs

- [`memorylimiterprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md)
- [`batchprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/batchprocessor/README.md)
- [`tailsamplingprocessor`](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/tailsamplingprocessor/README.md)
- [`probabilisticsamplerprocessor`](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/probabilisticsamplerprocessor/README.md)

## See also

- [`../../testdata/rules/processor-order.yaml`](../../testdata/rules/processor-order.yaml)
