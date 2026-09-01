# memory-limiter-config

**Default severity:** `error` · **Group:** [Practice](README.md#practice)

A `memory_limiter` the collector will refuse to start. Two of its constraints
cannot be written as a field schema, so [`invalid-value`](invalid-value.md)
cannot see them:

- `check_interval` must be greater than zero, and yet defaults to `0s` — so
  leaving it out is an error rather than a choice;
- `limit_mib` and `limit_percentage` are a one-of, and writing neither is
  refused.

This rule checks what upstream's own `Validate()` checks, quoting its wording,
and matches on the component type so `memory_limiter/aggressive` is covered too.

## What it reports

Every clause quotes upstream's own wording, so the message says what the
collector will say.

| Config | Severity |
| --- | --- |
| no `check_interval`, or one at or below `0s` | `error` |
| neither `limit_mib` nor `limit_percentage` | `error` |
| `spike_limit_mib` at or above `limit_mib` (and the same for the percentage pair) | `error` |
| a percentage above 100, which is not a share of anything | `error` |
| a `check_interval` far from the recommended `1s` | `info` |

A value only the collector can resolve — an expansion — is left alone rather
than guessed at.

## Example

```yaml
processors:
  memory_limiter:
    check_interval: 0s             # the interval must be greater than zero
    limit_mib: 512
    spike_limit_mib: 128
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:9:21: error: processor "memory_limiter" sets check_interval to 0s: 'check_interval' must be greater than zero [memory-limiter-config]
    hint: set check_interval: 1s, the value upstream recommends
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
```

## Docs

- [`memorylimiterprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md)
  — states `check_interval`'s `0s` default and its recommended value,
  `spike_limit_mib`'s 20% default, and the ~50MiB the process runs above
  `limit_mib`.

## See also

- [`memory-limiter-sizing`](memory-limiter-sizing.md) — the half that needs to
  know the container.
- [`../../testdata/rules/memory-limiter-config.yaml`](../../testdata/rules/memory-limiter-config.yaml)
