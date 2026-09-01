# invalid-value

**Default severity:** `error` · **Group:** [Settings](README.md#settings)

A setting whose value has the wrong type, or is outside the set the component
accepts.

## What it reports

Every value the [field schema](../schemas.md#field-schemas) types that the
config writes as something else — a duration without a unit, a string where a
list belongs, an enum value that is not one of the enum's — saying what it must
be instead.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    timeout: 5                     # a duration needs a unit, so this is not 5 seconds
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:16:14: error: "timeout" must be a duration such as 5s, 200ms or 1m30s [invalid-value]
```

The bare `5` is the one worth having the rule for. It parses, and it is not five
seconds.

## Notes

Enums come from the `config.schema.yaml` upstream publishes, which the Go source
alone cannot supply — see [Field schemas](../schemas.md#field-schemas).
Expansions are left alone: `${env:TIMEOUT}` is resolved at startup, and the
linter has no value to judge.

## See also

- [`../../testdata/rules/invalid-value.yaml`](../../testdata/rules/invalid-value.yaml)
