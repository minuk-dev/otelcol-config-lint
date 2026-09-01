# unknown-field

**Default severity:** `warning` (`error` under `--strict`) · **Group:** [Settings](README.md#settings)

A setting the component does not accept. The collector rejects these outright,
so the `warning` default is about the linter's confidence rather than the
collector's tolerance — see [Coverage](#coverage) below.

## What it reports

Every key a component's [field schema](../schemas.md#field-schemas) does not
describe, at any depth, with the accepted settings and a suggestion where the
key is close to one.

`--strict` (`run.strict` in the settings file) raises it to `error`, mirroring
kubeconform's flag.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    compresion: gzip               # "compression" misspelt
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:16:5: warning: unknown setting "compresion" for exporters.otlp_grpc [unknown-field]
    hint: accepted settings: auth, authority, balancer_name, compression, endpoint, headers, keepalive, middlewares, ...; did you mean "compression"?
```

## Coverage

Field schemas are read from each component's `Config` struct and enriched with
the `config.schema.yaml` upstream publishes, covering 92–96% of components on
every release. **What cannot be resolved is left open rather than reported**, so
partial coverage never produces false positives — a third-party config such as
Prometheus's own is a map the generator cannot see into, and everything under it
passes.

`${env:...}` expansions are left alone, and `--ignore-missing-schemas` turns off
reporting for components the schema does not describe at all, which is what a
custom distribution needs.

## See also

- [`required-field`](required-field.md), [`invalid-value`](invalid-value.md),
  [`deprecated-field`](deprecated-field.md) — the other three readers of the
  same schema.
- [`../../testdata/rules/unknown-field.yaml`](../../testdata/rules/unknown-field.yaml)
