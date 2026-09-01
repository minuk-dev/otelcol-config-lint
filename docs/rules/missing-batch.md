# missing-batch

**Default severity:** `info` · **Group:** [Practice](README.md#practice)

A pipeline that batches nothing on its way out. Every record becomes its own
export request, which costs throughput at the collector and request volume at
the backend.

## Two ways to batch

There are now two, and either satisfies the rule:

```yaml
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue:
      batch:                       # off by default; writing the block turns it on
        flush_timeout: 200ms
```

`exporterhelper` batches inside the sending queue, behind the queue and the
retry logic rather than in front of them. The `batch` processor
[drops the data in a batch that fails to send](https://github.com/open-telemetry/opentelemetry-collector/issues/12443);
the queue batcher does not. That is why the hint leads with the exporter setting
and names the processor as what also works. The processor is not deprecated —
upstream has explicitly not decided that — so a pipeline using it is a pipeline
that batches, and nothing here says to take it out.

## What it reports

- **The whole pipeline**, when it has no `batch` processor and none of its
  exporters batches in `sending_queue`.
- **One exporter**, when only some of them do. The pipeline is under-batched on
  the legs that do not, so the finding names the exporter and sits on the line
  that wires it in — that exporter's settings are where the fix is written.

A connector in the exporter slot is not a leg out of the collector and is not
counted; it feeds another pipeline, which has exporters of its own.

## Example

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter]   # nothing batches, so a record is an export
      exporters: [otlp_grpc]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:21:19: info: pipeline "traces" has no batch processor, and none of its exporters batches in sending_queue [missing-batch]
    hint: configure sending_queue.batch on the exporters, which batches behind the retry queue; the batch processor also works but drops data that fails to send
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporterhelper/README.md
```

## Which fix the hint names

What the document writes decides whether there is a finding; what the field
schema describes decides only which fix the hint may name.

The queue batcher is off until something turns it on, so a config that writes no
`sending_queue.batch` is not batching there whatever the schema knows about the
type — which is why an exporter the schema describes no settings for, `nop` and
`datadog` among them, is reported like any other rather than passed over.

The hint is the half that follows the schema. The batcher is younger than the
queue: on a release from before it moved in, every exporter has a `sending_queue`
and none of them has a `batch` under it, and a hint naming one there would be
advising a setting the collector rejects on startup —
[`unknown-field`](unknown-field.md), reading the same schema, would say so in
the same run. So where the field is not there to write, the hint names the
processor instead:

- an exporter with no `sending_queue` on the targeted release (`debug` on
  v0.157 among them);
- any exporter on a release that predates the batcher;
- anything the schema does not describe.

Where one finding covers several exporters it names the queue batcher only if
all of them accept it; where they disagree it names the processor and says only
some can take a `sending_queue.batch`.

An exporter whose queue is built from an expansion counts as unknown rather than
as not batching. A merge key is not one of those: it is resolved when the file
is parsed, so what an anchor supplies is read like anything written in place.

## Docs

- [`exporterhelper`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporterhelper/README.md)

## See also

- [`../../testdata/rules/missing-batch.yaml`](../../testdata/rules/missing-batch.yaml)
