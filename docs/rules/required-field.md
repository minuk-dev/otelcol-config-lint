# required-field

**Default severity:** `error` · **Group:** [Settings](README.md#settings)

A setting the component cannot start without.

## What it reports

Every setting the [field schema](../schemas.md#field-schemas) marks required
that the config does not write, reported on the component it belongs to.

## Example

```yaml
receivers:
  otlp:                            # needs "protocols"; with none it enables nothing
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:3:8: error: missing required setting "protocols" for receivers.otlp [required-field]
```

## Notes

Required-ness is what the schema records, so a component the schema does not
describe contributes nothing here — the same coverage rule as
[`unknown-field`](unknown-field.md), and for the same reason: silence beats a
false positive.

## See also

- [`../../testdata/rules/required-field.yaml`](../../testdata/rules/required-field.yaml)
