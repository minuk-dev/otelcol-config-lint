# no-persistent-queue

**Default severity:** `info` · **Group:** [Practice](README.md#practice)

A sending queue held in memory, which a restart drops along with everything in
it. The queue is on by default, so a deploy, an OOM kill or a node drain takes
whatever is still in it — and nothing in the config says so, because an exporter
with a `storage` extension and one without look identical until the restart.

## What it reports

One finding **per exporter**, not per config: the fix is written per exporter,
each queue names its own storage, and a finding without a position cannot say
which of eight exporters to edit.

It stays quiet on:

- `sending_queue.enabled: false`;
- an exporter no pipeline references — [`unused-component`](unused-component.md)
  has that one;
- exporters whose field schema describes their settings and has no queue among
  them, which is what keeps `debug` out of it.

Where the schema resolved nothing for a component — the `datadog` exporter and
`nop` share that — a queue the config writes is the only evidence there is, and
it is taken at face value.

## Example

```yaml
exporters:
  otlp_grpc:                       # the default sending_queue is in memory
    endpoint: backend:4317
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:15:3: info: exporter "otlp_grpc" takes the default sending_queue, which is held in memory; a restart drops whatever is still queued [no-persistent-queue]
    hint: persistence takes three steps: declare a storage extension such as file_storage, list it in service.extensions so the collector starts it, and name it here as sending_queue.storage
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporterhelper/README.md
```

The fix:

```yaml
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage        # without this, a restart drops the queue
```

Persistence takes three steps — a storage extension declared, listed in
`service.extensions`, and named here — which is why the hint names all three and
why [`undefined-extension-reference`](undefined-extension-reference.md) is worth
having alongside it.

## Turning it off

This is the one opinionated rule, and it reports at `info` for that reason. It
is also the rule most likely to be unwanted: a sidecar next to an application
that can re-send, or an agent whose source keeps its own buffer, is right not to
want a writable volume.

`info` is already below `--min-severity warning`, which is a normal way to run,
and `disable: [no-persistent-queue]` in the settings file turns it off for good.
There is deliberately no default-disabled rule concept for it — a second
mechanism meaning roughly the same thing would only be another place to look.

## Docs

- [`exporterhelper`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporterhelper/README.md)

## See also

- [`../../testdata/rules/no-persistent-queue.yaml`](../../testdata/rules/no-persistent-queue.yaml)
