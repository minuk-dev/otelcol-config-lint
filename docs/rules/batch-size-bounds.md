# batch-size-bounds

**Default severity:** `error` · **Group:** [Practice](README.md#practice)

A `batch` processor the collector will refuse to start. The processor's
`Validate()` relates two fields that a field schema only ever sees one at a
time, so [`invalid-value`](invalid-value.md) cannot catch this.

## What it reports

| Config | Why |
| --- | --- |
| `send_batch_max_size` below `send_batch_size` | a cap below the trigger is refused at startup |
| a negative `timeout` | refused at startup |
| a key repeated in `metadata_keys` | compared case-insensitively, so `tenant` and `Tenant` are one key |
| a batch size outside `uint32` | fails to load rather than to validate |

## Example

```yaml
processors:
  batch:
    send_batch_size: 10000
    send_batch_max_size: 1000      # the cap is below the size a batch is sent at
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:14:26: error: processor "batch" sets send_batch_max_size to 1000 and send_batch_size to 10000: send_batch_max_size must be greater or equal to send_batch_size [batch-size-bounds]
    hint: raise send_batch_max_size to 10000 or more, or leave it out so batches are not split at all
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/batchprocessor/README.md
```

The names read backwards from the semantics: `send_batch_size` is the count that
*triggers* a send, `send_batch_max_size` is a hard cap. So capping at something
reasonable-looking like `1000` while leaving `send_batch_size` at its default of
`8192` is exactly the shape that fails — and since nobody wrote the 8192, the
finding says it is the default rather than quoting it back as if they had.

Both sizes are `uint32` upstream, a range the published field schemas flatten to
`int`, so a negative or over-large count is reported here too: it fails to load
rather than to validate, and saying so beats hinting at a fix that still will
not start.

## Docs

- [`batchprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/batchprocessor/README.md)

## See also

- [`../../testdata/rules/batch-size-bounds.yaml`](../../testdata/rules/batch-size-bounds.yaml)
