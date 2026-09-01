# receiver-binds-all-interfaces

**Default severity:** `warning` · **Group:** [Security](README.md#security)

A component listening on every network interface the host has. This is the one
upstream's [config best
practices](https://opentelemetry.io/docs/security/config-best-practices/) lead
with, and the most common way a collector ends up reachable from more of the
network than anyone meant.

## What it reports

An endpoint something *listens* on, bound to an unspecified address. Every
spelling is matched: `0.0.0.0`, `[::]`, a bare `:4317`, and `:::4317` — which is
what netstat prints for an IPv6 listener and which Go refuses outright, so a
collector handed one does not start at all.

The split is by kind: **receivers and extensions bind**, while an exporter's or
a connector's `endpoint` names somewhere to send *to*. `0.0.0.0` there is also
wrong, but it is a different mistake with a different fix, and a bind-address
hint on a line about a backend would be worse than silence.

Two key names are read, not one: the stanza-based log receivers call it
`listen_address` — `tcplog`, `udplog`, and `syslog` under each of its `tcp` and
`udp` blocks — and reading only `endpoint` would leave every one of them
unreported. Within a component the walk is by key name rather than by a table of
where each type keeps its address, since `otlp` writes one per protocol and most
others write one at the top; each is reported separately, because each is its
own line to edit.

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317     # every interface, not just the one you meant
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:6:19: warning: "protocols.grpc.endpoint" binds "0.0.0.0:4317", every interface the host has, for receiver "otlp" [receiver-binds-all-interfaces]
    hint: bind the interface you meant, e.g. 127.0.0.1:4317 when every client is local; in Kubernetes write ${env:MY_POD_IP}:4317, the pod IP the downward API supplies
    docs: https://opentelemetry.io/docs/security/config-best-practices/
```

The collector changed its own default to `localhost` in v0.110.0 for exactly
this reason. The example configs the ecosystem copies from did not — the ones on
opentelemetry.io say outright that they use the unspecified address "as a
convenience" — and those get pasted into production.

## What it stays quiet about

- A receiver no pipeline names, and an extension `service.extensions` leaves
  out: neither is ever instantiated, so neither binds anything.
- An endpoint nobody wrote, which takes the component's own default.
- `${env:MY_POD_IP}:4317` — that is the fix. But `0.0.0.0:${env:PORT}` is still
  reported: the address says plainly who can reach it, and only the port is left
  open.
- `health_check` and `healthcheckv2`, for the reason
  [`debug-extension-exposed`](debug-extension-exposed.md) leaves them out.
  `pprof` and `zpages` are that rule's to report.

`warning`, not `error`: a gateway behind a service mesh with its own network
policy binds every interface deliberately.

## Docs

- [Config best practices](https://opentelemetry.io/docs/security/config-best-practices/)

## See also

- [`../../testdata/rules/receiver-binds-all-interfaces.yaml`](../../testdata/rules/receiver-binds-all-interfaces.yaml)
