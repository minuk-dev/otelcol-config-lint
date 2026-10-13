# invalid-value

**Default severity:** `error` · **Group:** [Settings](README.md#settings)

A setting whose value has the wrong type, or is outside the set the component
accepts.

## What it reports

Every value the [field schema](../schemas.md#field-schemas) types that the
config writes as something else — a nonzero duration string without a unit, a string where a
list belongs, an enum value that is not one of the enum's — saying what it must
be instead.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    timeout: "5"                   # a nonzero duration string needs a unit
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:16:14: error: "timeout" must be a duration such as 5s, 200ms or 1m30s [invalid-value]
```

The quoted `"5"` is a string with no duration unit. Use `5s` for five seconds.
An unquoted `5` is valid numeric input, but means five nanoseconds.

## Notes

Duration strings use Go's `time.ParseDuration`, matching the Collector's decoder.
Numeric YAML values specify nanoseconds; fractional nanoseconds are truncated.
Values such as `.5s`, `1.s`, `+1s`, `0`, and `1μs` are valid; values outside
the range of `time.Duration` are reported.

Enums come from the `config.schema.yaml` upstream publishes, which the Go source
alone cannot supply — see [Field schemas](../schemas.md#field-schemas).
Expansions are left alone: `${env:TIMEOUT}` is resolved at startup, and the
linter has no value to judge.

## See also

- [`../../testdata/rules/invalid-value.yaml`](../../testdata/rules/invalid-value.yaml)
