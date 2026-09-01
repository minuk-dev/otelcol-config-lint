# insecure-tls

**Default severity:** `warning` · **Group:** [Security](README.md#security)

TLS verification switched off on a component that talks over the network. The
connection is neither encrypted nor authenticated, and anyone on the path can
read it or stand in for the other end.

## What it reports

`insecure: true` or `insecure_skip_verify: true`, at **any depth** inside a
declared component — exporters bury these under sub-blocks such as auth or queue
settings, so the walk goes all the way down. Only a literal boolean `true`
counts.

## Example

```yaml
exporters:
  otlp_grpc:
    endpoint: backend:4317
    tls:
      insecure: true               # neither encrypted nor verified
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:17:17: warning: "tls.insecure" disables TLS verification for exporter "otlp_grpc" [insecure-tls]
    hint: supply ca_file/cert_file instead of skipping verification outside local testing
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md
```

`warning` rather than `error`: a local test collector talking to a local backend
over plaintext is a normal thing to write. `--severity insecure-tls=error` makes
it fatal where it should be.

## Docs

- [`configtls`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md)

## See also

- [`../../testdata/rules/insecure-tls.yaml`](../../testdata/rules/insecure-tls.yaml)
