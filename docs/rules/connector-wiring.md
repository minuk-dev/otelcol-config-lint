# connector-wiring

**Default severity:** `error` · **Group:** [Wiring](README.md#wiring)

A connector joins two pipelines: it is an exporter in the one that feeds it and
a receiver in the one that consumes what it produces. Wired to one side only, it
either gets no input or has its output dropped.

## What it reports

| Config | Finding |
| --- | --- |
| used as a receiver, never as an exporter | `... so it gets no input` |
| used as an exporter, never as a receiver | `... so its output is dropped` |
| both sides of the *same* pipeline | `... which forms a loop` |

## Example

```yaml
connectors:
  span_metrics:                    # nothing exports into it, so it produces nothing
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [otlp_grpc]       # span_metrics is not here
    metrics:
      receivers: [span_metrics]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:19:3: error: connector "span_metrics" is used as a receiver but never as an exporter, so it gets no input [connector-wiring]
    hint: list it under exporters in the pipeline that should feed it
```

Nothing fails at startup. The metrics pipeline runs, and produces nothing, for
as long as nobody looks.

## Notes

- A connector referenced from neither side is left to
  [`unused-component`](unused-component.md).
- Connectors are directional per signal, which
  [`signal-support`](signal-support.md) checks: the exporter side consumes the
  pipeline's signal, the receiver side produces it, and a connector can support
  a signal on one side and not the other.

## See also

- [`../../testdata/rules/connector-wiring.yaml`](../../testdata/rules/connector-wiring.yaml)
