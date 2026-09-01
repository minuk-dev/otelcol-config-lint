# unknown-top-level-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

A top-level key other than the component sections and `service`. The collector
rejects the file rather than ignoring the key, so this is a config that does not
start.

## What it reports

Any root key outside `receivers`, `processors`, `exporters`, `connectors`,
`extensions` and `service`. The hint lists the six, and names the nearest one
when the key looks like a typo.

## Example

```yaml
recievers:            # "receivers" misspelt
  otlp:
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:1:1: error: unknown top-level key "recievers" [unknown-top-level-key]
    hint: valid top-level keys: connectors, exporters, extensions, processors, receivers, service; did you mean "receivers"?
```

The misspelling is the case worth having the rule for: the file still parses,
the section below it still works, and the collector's own error names a key
rather than the line that made it.

## See also

- [`unknown-service-key`](unknown-service-key.md) for the same mistake one level
  down.
- [`../../testdata/rules/unknown-top-level-key.yaml`](../../testdata/rules/unknown-top-level-key.yaml)
