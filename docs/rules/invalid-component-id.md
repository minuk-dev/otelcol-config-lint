# invalid-component-id

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

Component declarations and references must use `<type>` or `<type>/<name>`.
This includes every component section, service extensions, pipeline slots and
extension references inside component settings.

## What it reports

- An empty type, or a type longer than 63 ASCII characters. Types must start
  with a letter and contain only letters, digits and underscores.
- An explicitly present but empty name, such as `otlp/`.
- A name longer than 1024 bytes, or containing Unicode separators, control
  characters or symbols. Letters, digits, punctuation and extra slashes are valid.

Each part is trimmed as the Collector does. Validation reads the original YAML
value rather than a reconstructed ID, so an empty name cannot disappear and
diagnostics keep the original spelling and position.

## Example

```yaml
receivers:
  otlp/: # invalid: the slash requires a name
    protocols:
      grpc:
service:
  pipelines:
    traces:
      receivers: [otlp/]
      exporters: [debug]
```

Both the declaration and reference produce errors. `otlp/internal` is valid.

## Notes

The checks follow
[`component.ID.UnmarshalText` at v0.157.0](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.157.0/component/identifiable.go),
also used at v0.110.0. The name length limit counts bytes, including for Unicode
names. Pipeline IDs are checked by [`invalid-pipeline-key`](invalid-pipeline-key.md).

## See also

- [`../../testdata/rules/invalid-component-id.yaml`](../../testdata/rules/invalid-component-id.yaml)
