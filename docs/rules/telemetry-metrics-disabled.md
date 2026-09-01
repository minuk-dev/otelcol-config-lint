# telemetry-metrics-disabled

**Default severity:** `info` · **Group:** [The collector's own telemetry](README.md#the-collectors-own-telemetry)

`service.telemetry.metrics.level: none` is a legitimate setting with an
unadvertised cost: the metrics it turns off are the ones that say the collector
is dropping data.

## What it reports

A `level` of `none`, compared case-insensitively the way configtelemetry's own
decoder folds it, so `None` is the same level. A value only the collector can
resolve — an expansion, or one a merge key supplies — is read as not disabled:
a finding quoting a level nobody wrote would be a guess.

## Example

```yaml
service:
  telemetry:
    metrics:
      level: none                  # nothing is reported about the collector itself
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:23:14: info: service.telemetry.metrics.level is "none", so the collector reports no metrics about itself [telemetry-metrics-disabled]
    hint: there are reasons to run without them, but they are the metrics that say the collector is dropping data -- otelcol_exporter_send_failed_* and the queue sizes among them; "basic" is the level that keeps those
    docs: https://opentelemetry.io/docs/collector/internal-telemetry/
```

The cost is that `otelcol_exporter_send_failed_*` and the queue sizes are not
emitted either, so a pipeline in trouble looks exactly like a healthy one.
`basic` is the level that keeps those.

## Docs

- [Internal telemetry](https://opentelemetry.io/docs/collector/internal-telemetry/)

## See also

- [`../../testdata/rules/telemetry-metrics-disabled.yaml`](../../testdata/rules/telemetry-metrics-disabled.yaml)
