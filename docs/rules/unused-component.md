# unused-component

**Default severity:** `warning` · **Group:** [Wiring](README.md#wiring)

A declared component that nothing wires up is never instantiated: it opens no
port, holds no connection, and its settings are never validated.

## What it reports

A component under any section that no pipeline references, and an extension that
`service.extensions` does not list. The two get different wording, because an
extension is enabled by being listed rather than by being referenced from a
pipeline.

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
  otlp/second:                     # declared, referenced by no pipeline
    protocols:
      http:
        endpoint: localhost:4318
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:7:3: warning: receiver "otlp/second" is declared but referenced by no pipeline [unused-component]
    hint: remove it, or reference it so it actually runs
```

The mistake this catches is the second half of a change that was only half made:
a component added and never wired in, or wired out and never removed — and a
`0.0.0.0` endpoint on a receiver nobody references reads alarming while binding
nothing at all.

## Notes

- Without a `service` block every component is unused, so the rule stands down
  and lets [`service-required`](service-required.md) say it once.
- Several rules defer to this one rather than reporting a component that never
  runs: [`debug-exporter-verbosity`](debug-exporter-verbosity.md),
  [`no-persistent-queue`](no-persistent-queue.md),
  [`receiver-binds-all-interfaces`](receiver-binds-all-interfaces.md) and
  [`debug-extension-exposed`](debug-extension-exposed.md) all skip what is not
  wired in. [`hardcoded-secret`](hardcoded-secret.md) is the exception: a
  credential in the repository has already been handed over, wired or not.

## See also

- [`../../testdata/rules/unused-component.yaml`](../../testdata/rules/unused-component.yaml)
