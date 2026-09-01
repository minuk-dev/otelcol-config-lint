# Rules

One page per rule: what it reports, a config that trips it, and why the rule
exists. `otelcol-config-lint list rules` prints the same set with the severities
a run resolves them to.

Every rule can be switched off or re-levelled from the [settings
file](../configuration.md#choosing-the-rules):

```yaml
rules:
  disable: [missing-batch]
  severity:
    missing-memory-limiter: warning
```

## Structure

The config the collector will refuse to load, or will load and silently ignore
half of.

| Rule | Default | Reports |
| --- | --- | --- |
| [`unknown-top-level-key`](unknown-top-level-key.md) | `error` | a root key other than the component sections and `service` |
| [`service-required`](service-required.md) | `error` | no `service` block, or no pipelines in it |
| [`unknown-service-key`](unknown-service-key.md) | `error` | a key inside `service` the collector does not read |
| [`invalid-pipeline-key`](invalid-pipeline-key.md) | `error` | a pipeline named after no signal |
| [`empty-pipeline`](empty-pipeline.md) | `error` | a pipeline with no receivers or no exporters |
| [`unknown-pipeline-key`](unknown-pipeline-key.md) | `error` | a pipeline slot other than the three |
| [`duplicate-key`](duplicate-key.md) | `error` | a mapping key written twice, discarding the first value |
| [`wrong-node-type`](wrong-node-type.md) | `error` | a section or slot written in the wrong YAML shape |

## Wiring

Whether what the config declares and what it references add up.

| Rule | Default | Reports |
| --- | --- | --- |
| [`undefined-reference`](undefined-reference.md) | `error` | the `service` block naming a component nothing declares |
| [`undefined-extension-reference`](undefined-extension-reference.md) | `error` | an extension a component's own settings name, that nothing declares or starts |
| [`unused-component`](unused-component.md) | `warning` | a declaration no pipeline reaches |
| [`duplicate-reference`](duplicate-reference.md) | `warning` | the same component listed twice in one slot |
| [`connector-wiring`](connector-wiring.md) | `error` | a connector wired to one side only, or closing a loop |

## Release and distribution compatibility

The half of the linter that needs to know which collector you run.

| Rule | Default | Reports |
| --- | --- | --- |
| [`unknown-component`](unknown-component.md) | `error` | a component type the targeted release does not ship |
| [`signal-support`](signal-support.md) | `error` | a component in a pipeline whose signal it does not carry |
| [`component-stability`](component-stability.md) | `info` | a component below beta stability |
| [`deprecated-component`](deprecated-component.md) | `warning` | a component upstream has deprecated or stopped maintaining |

## Settings

Read from each component's field schema. See [Field
schemas](../schemas.md#field-schemas) for where those come from and what they
cannot answer.

| Rule | Default | Reports |
| --- | --- | --- |
| [`unknown-field`](unknown-field.md) | `warning` | a setting the component does not accept |
| [`required-field`](required-field.md) | `error` | a setting the component cannot start without |
| [`invalid-value`](invalid-value.md) | `error` | a value of the wrong type, or outside the allowed set |
| [`deprecated-field`](deprecated-field.md) | `warning` | a setting upstream has replaced |

## Practice

Configs the collector loads and runs, and then behaves in a way nobody asked
for. Every rule here cites the upstream page it rests on.

| Rule | Default | Reports |
| --- | --- | --- |
| [`processor-order`](processor-order.md) | `warning` | a processor in the one place it does not work |
| [`missing-memory-limiter`](missing-memory-limiter.md) | `info` | a pipeline with nothing bounding memory use |
| [`missing-batch`](missing-batch.md) | `info` | a pipeline that batches nothing on its way out |
| [`memory-limiter-config`](memory-limiter-config.md) | `error` | a `memory_limiter` the collector refuses to start |
| [`memory-limiter-sizing`](memory-limiter-sizing.md) | `warning` | a `memory_limiter` that does not fit its container |
| [`batch-size-bounds`](batch-size-bounds.md) | `error` | a `batch` processor the collector refuses to start |
| [`no-persistent-queue`](no-persistent-queue.md) | `info` | a sending queue a restart empties |
| [`debug-exporter-verbosity`](debug-exporter-verbosity.md) | `warning` | a `debug` exporter left in a production pipeline |

## The collector's own telemetry

`service.telemetry` is the one block about the run rather than about the data,
and both rules report a silence rather than a failure.

| Rule | Default | Reports |
| --- | --- | --- |
| [`deprecated-telemetry-key`](deprecated-telemetry-key.md) | `warning` | a `service.telemetry` key the collector stopped reading |
| [`telemetry-metrics-disabled`](telemetry-metrics-disabled.md) | `info` | the collector's own metrics turned off |

## Security

| Rule | Default | Reports |
| --- | --- | --- |
| [`insecure-tls`](insecure-tls.md) | `warning` | TLS verification switched off |
| [`receiver-binds-all-interfaces`](receiver-binds-all-interfaces.md) | `warning` | a listener on every interface the host has |
| [`debug-extension-exposed`](debug-extension-exposed.md) | `warning` | `pprof` or `zpages` reachable from off the host |
| [`hardcoded-secret`](hardcoded-secret.md) | `warning` | a credential written into the config |

## What every rule has in common

- **The config the collector will read.** YAML anchors, aliases and `<<` merge
  keys are resolved when the file is parsed, the way confmap resolves them
  before the collector sees a setting. A component whose settings come from a
  shared base is checked against what it will run with, `<<` is never reported
  as a setting, and a key written in place still wins over the one a merge
  supplies. Findings keep the line they were written on, so a setting an anchor
  supplies is reported at the anchor — the line to edit.
- **Expansions are left alone.** `${env:...}` and `${file:...}` are resolved at
  startup, not here, so a rule that cannot see the value stays quiet rather than
  guessing. The exception is where the written half is already the finding:
  `0.0.0.0:${env:PORT}` says plainly who can reach it.
- **A `docs:` link where there is one to give.** Practice, telemetry and
  security rules carry the upstream page that states the recommendation, so the
  claim can be checked instead of taken on trust. It is in the JSON output as
  `docs`, and in the GitHub annotations.
