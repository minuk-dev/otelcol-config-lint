# undefined-reference

**Default severity:** `error` · **Group:** [Wiring](README.md#wiring)

The `service` block may only reference components the config declares. A name it
cannot resolve is a startup failure.

## What it reports

Every name in `service.extensions` and in the three slots of every pipeline that
has no declaration under the matching section. The hint lists what *is* declared
there, and says so when the name is declared under a different section — a
receiver listed among the exporters resolves to nothing, and the fix is moving
the reference rather than adding a component.

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
service:
  pipelines:
    traces:
      receivers: [otlp, jaeger]        # nothing declares "jaeger"
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:21:25: error: pipeline "traces" references receiver "jaeger" which is not declared under receivers [undefined-reference]
    hint: declared receivers: otlp
```

## Notes

A connector is resolvable from a pipeline's receiver *and* exporter slot, which
is what makes it a connector; whether it is wired to both is
[`connector-wiring`](connector-wiring.md)'s question.

## See also

- [`undefined-extension-reference`](undefined-extension-reference.md) — the same
  mistake made inside a component's own settings, where this rule never looks.
- [`unused-component`](unused-component.md) — the other direction: declared and
  never referenced.
- [`../../testdata/rules/undefined-reference.yaml`](../../testdata/rules/undefined-reference.yaml)
