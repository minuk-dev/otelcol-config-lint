# service-required

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

A config must declare a `service` block with at least one pipeline. Components
are declared, not started: nothing the `service` block does not wire up is ever
instantiated.

## What it reports

Three positions, one at a time:

| Config | Finding |
| --- | --- |
| no `service` block | `config has no service block, so nothing will run` |
| `service` with no `pipelines` | `service has no pipelines, so no telemetry will be processed` |
| `pipelines` written and empty | `service.pipelines is empty, so no telemetry will be processed` |

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
exporters:
  otlp_grpc:
    endpoint: backend:4317
# no service block, so the collector starts nothing at all
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:1:1: error: config has no service block, so nothing will run [service-required]
    hint: add a service.pipelines section wiring the declared components together
```

## Notes

It is the rule that keeps the others quiet. Without a `service` block every
declared component is unused and every extension unstarted, so
[`unused-component`](unused-component.md) and
[`undefined-extension-reference`](undefined-extension-reference.md) stand down
and let this one finding speak for the file.

## See also

- [`../../testdata/rules/service-required.yaml`](../../testdata/rules/service-required.yaml)
