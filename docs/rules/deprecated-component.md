# deprecated-component

**Default severity:** `warning` · **Group:** [Release compatibility](README.md#release-and-distribution-compatibility)

A component upstream has marked deprecated or unmaintained. It still works, for
now; the point of the finding is that the migration is cheaper before removal
than after.

## What it reports

Two sources, in order:

1. an explicit deprecation note in the schema, which is quoted as the hint —
   this is where a rename lands;
2. a `deprecated` or `unmaintained` stability level on any signal, with
   `plan a migration; it may be removed in a future release`.

## Example

```yaml
exporters:
  otlp:                            # renamed to "otlp_grpc" upstream
    endpoint: backend:4317
```

```console
$ otelcol-config-lint run --collector-version v0.157.0 config.yaml
config.yaml:14:3: warning: exporter "otlp" is deprecated [deprecated-component]
    hint: renamed to "otlp_grpc" upstream; the old name still resolves for now
```

Upstream is moving component types to snake_case, so from v0.157.0 the OTLP gRPC
exporter is `otlp_grpc` and `otlp` is a deprecated alias. Both resolve, so
[`unknown-component`](unknown-component.md) stays quiet and this rule is what
says the old name is on its way out.

## Notes

Only components a pipeline uses are reported; a declaration nothing wires up is
[`unused-component`](unused-component.md)'s. The `logging` exporter — removed
rather than deprecated — is [`unknown-component`](unknown-component.md)'s.

## See also

- [`../../testdata/rules/deprecated-component.yaml`](../../testdata/rules/deprecated-component.yaml)
