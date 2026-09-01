# debug-extension-exposed

**Default severity:** `warning` · **Group:** [Security](README.md#security)

`pprof` serves heap profiles, goroutine dumps and the collector's command line;
`zpages` serves live traces and pipeline internals. Upstream's [security
guidance](https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/security-best-practices.md)
is direct about avoiding "exposing health or telemetry data outside the
Collector by default", and these are the extensions that do it.

This is narrower than
[`receiver-binds-all-interfaces`](receiver-binds-all-interfaces.md), and
separate from it because the consequence differs in kind: a `0.0.0.0` OTLP
receiver accepts data it should not, while a `0.0.0.0` pprof endpoint *hands
out* process internals.

## What it reports

```yaml
extensions:
  pprof:
    endpoint: 0.0.0.0:1777         # warning: heap profiles, on every interface
  zpages:
    endpoint: 10.0.4.7:55679       # info: deliberate, but that network can read it
```

| Address | Severity |
| --- | --- |
| unspecified — `0.0.0.0`, `[::]`, a bare `:1777` | `warning` |
| a specific non-loopback address | `info` |

The two are reported apart because they have different fixes. The second was
chosen by someone, and the finding says only that the network it sits on can
read the collector's internals. Both match on component type, so
`zpages/internal` is covered.

## Example

```console
$ otelcol-config-lint run config.yaml
config.yaml:20:15: warning: extension "zpages" binds "0.0.0.0:55679", serving live traces and the collector's pipeline internals on every interface of the host [debug-extension-exposed]
    hint: bind it to localhost, and reach it through a port-forward when you need it; upstream's advice is not to expose telemetry or debugging data outside the collector by default
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/security-best-practices.md
```

## What it stays quiet about

- **`health_check` and `healthcheckv2`, deliberately.** A Kubernetes liveness
  probe comes from the kubelet, off the container's loopback interface, so a
  health check bound to `0.0.0.0` is what a correct deployment looks like — and
  a rule that fires on every correct config is a rule people disable, which
  would take `pprof` down with it.
- An extension `service.extensions` does not list. That is the only thing that
  starts one; a declaration left out of it binds no port, and
  [`unused-component`](unused-component.md) has something to say about it.
- An endpoint nobody wrote: it takes the extension's own default, which has been
  `localhost` for both since upstream made `UseLocalHostAsDefaultHost` the
  default in v0.110.
- A hostname other than `localhost`, since what a name resolves to is a property
  of the network rather than of the file, and an address supplied by
  `${env:...}`. An expansion standing in for only the *port* is still reported —
  `0.0.0.0:${env:PPROF_PORT}` says plainly who can reach it.

An endpoint a `<<` merge supplies is read like one written in place, so sharing
one debugging block between extensions does not hide it.

## Docs

- [Security best practices](https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/security-best-practices.md)

## See also

- [`../../testdata/rules/debug-extension-exposed.yaml`](../../testdata/rules/debug-extension-exposed.yaml)
