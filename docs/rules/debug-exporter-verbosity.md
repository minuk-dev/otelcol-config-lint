# debug-exporter-verbosity

**Default severity:** `warning` · **Group:** [Practice](README.md#practice)

The exporter somebody added to find out why an export was failing, and never
took out again. It writes what it is given to the collector's own log, and at
`verbosity: detailed` that is every field of every span, data point and record
in the pipeline — the collector spends its time formatting log lines, and the
log backend receives a second copy of all the telemetry the collector was meant
to be forwarding.

## What it reports

| Config | Severity |
| --- | --- |
| a `debug` exporter at `verbosity: detailed` | `warning` |
| a `debug` exporter at any other verbosity | `info` |

One finding per pipeline reference, not both. `basic` costs a line per batch
rather than per record, which is cheap enough not to be a warning — but it is still a diagnostic tool left running, and upstream
keeps no stable output format, so nothing downstream should be parsing it
either.

Both clauses match on the component type, so `debug/verbose` counts, and both
report **per pipeline** that references the exporter: that is what the message
names, and where the reader takes it out of.

## Example

```yaml
exporters:
  debug:
    verbosity: detailed            # a line per record, not per batch
service:
  pipelines:
    traces:
      exporters: [otlp, debug]     # and it ships to production like this
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:22:19: warning: exporter "debug" runs at verbosity: detailed in pipeline "traces", logging every record it receives [debug-exporter-verbosity]
    hint: set verbosity: basic, or remove the exporter from service.pipelines.traces.exporters; sampling_initial and sampling_thereafter bound the rate if it has to stay at detailed
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/debugexporter/README.md
```

## Notes

- An exporter no pipeline references prints nothing and is
  [`unused-component`](unused-component.md)'s to report; the `logging` exporter
  that came before it is [`deprecated-component`](deprecated-component.md)'s.
- `warning` rather than `error` is deliberate: a deliberate debug run is
  legitimate and should not fail a CI job. `--severity
  debug-exporter-verbosity=error` is there for anyone who wants it fatal.

## Docs

- [`debugexporter`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/debugexporter/README.md)

## See also

- [`../../testdata/rules/debug-exporter-verbosity.yaml`](../../testdata/rules/debug-exporter-verbosity.yaml)
