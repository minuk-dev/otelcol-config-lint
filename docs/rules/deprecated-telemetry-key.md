# deprecated-telemetry-key

**Default severity:** `warning` · **Group:** [The collector's own telemetry](README.md#the-collectors-own-telemetry)

A `service.telemetry` key the collector no longer reads. The config is not
refused — it is obeyed somewhere else, which is what makes it worth a finding.

## What it reports

`service.telemetry.metrics.address`, which is ignored from collector **v0.123.0**
onward. A collector upgraded across that release starts, and serves its metrics
wherever its `readers` say, while the scrape target the config names stays
empty.

The rule holds its tongue when `--collector-version` is older, because there the
key still works, and when no schema resolved, because there is no release to
judge against.

## Example

```yaml
service:
  telemetry:
    metrics:
      address: localhost:8888      # ignored from v0.123.0; a pull reader replaces it
```

```console
$ otelcol-config-lint run --collector-version v0.157.0 config.yaml
config.yaml:24:7: warning: setting "address" in service.telemetry.metrics is ignored from collector v0.123.0 onward [deprecated-telemetry-key]
    hint: its host and port move into a pull reader: readers: [{pull: {exporter: {prometheus: {host: localhost, port: 8888}}}}]; as written, the collector serves its metrics where the readers say and not here
    docs: https://opentelemetry.io/docs/collector/internal-telemetry/
```

## Docs

- [Internal telemetry](https://opentelemetry.io/docs/collector/internal-telemetry/)

## See also

- [`../../testdata/rules/deprecated-telemetry-key.yaml`](../../testdata/rules/deprecated-telemetry-key.yaml)
