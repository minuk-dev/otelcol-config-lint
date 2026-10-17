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
    hint: configure trusted CA certificates and enable verification outside local testing
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md
```

`warning` rather than `error`: a local test collector talking to a local backend
over plaintext is a normal thing to write. `--severity insecure-tls=error` makes
it fatal where it should be.

## Docs

- [`configtls`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md)

## See also

- [`../../testdata/rules/insecure-tls.yaml`](../../testdata/rules/insecure-tls.yaml)

When the same TLS block contains a nonempty literal `ca_file` and
`insecure_skip_verify: true`, the warning explains that server verification is
still disabled despite the configured CA. Remove the skip flag or set it to
false while retaining the intended CA. The CA file may still be loaded and can
cause a startup error if invalid; it is not described as unread or wholly unused.
An absent, empty or unresolved CA keeps the generic diagnostic.
