# deprecated-field

**Default severity:** `warning` · **Group:** [Settings](README.md#settings)

A setting upstream has replaced. It is still read, and the replacement is
usually where the new options live.

## What it reports

Every setting the [field schema](../schemas.md#field-schemas) marks deprecated,
with upstream's own note about what replaces it as the hint.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    compression: gzip              # replaced by "compression_type"
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:19:5: warning: setting "compression" is deprecated [deprecated-field]
    hint: use "compression_type", which also names the compression level
```

## Notes

This is the settings-level counterpart of
[`deprecated-component`](deprecated-component.md). What counts as deprecated is
whatever the schema for the targeted release records, so pointing
`--collector-version` at an older release quietly and correctly stops reporting
a setting that was fine there.

## See also

- [`../../testdata/rules/deprecated-field.yaml`](../../testdata/rules/deprecated-field.yaml)
  — checked against a small schema in `testdata/rules/schemas`, since no
  released schema carries a deprecated setting yet.
