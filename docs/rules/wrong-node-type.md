# wrong-node-type

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

Component sections are mappings of component id to settings; `service.extensions`
and the three pipeline slots are lists. Writing one as the other is a config the
collector cannot decode.

## What it reports

| Where | Must be |
| --- | --- |
| `receivers`, `processors`, `exporters`, `connectors`, `extensions` | a mapping |
| `service.extensions` | a list |
| a pipeline's `receivers`, `processors`, `exporters` | a list |

A null node — a key written with nothing under it — is not reported: that is how
a component with no settings is declared.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers:                 # a slot is a list of component ids
        otlp: {}
      processors: [memory_limiter, batch]
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:22:9: error: receivers must be a list, got a mapping [wrong-node-type]
    hint: write it as a YAML sequence, e.g. receivers: [otlp]
```

## See also

- [`../../testdata/rules/wrong-node-type.yaml`](../../testdata/rules/wrong-node-type.yaml)
