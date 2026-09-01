# memory-limiter-sizing

**Default severity:** `warning` · **Group:** [Practice](README.md#practice)

A `memory_limiter` whose limits do not fit the container it runs in. A limiter
that passes every other check is still decoration if `limit_mib` sits at or
above the container's memory limit: the kernel kills the collector before the
limiter ever engages.

This needs a number the config cannot carry, so it runs **only for files the
[`kubernetes` block](../configuration.md#the-deployment-environment)
describes**. A run that configures nothing sees nothing new.

## What it reports

The hard limit is `limit_mib`, or `limit_percentage` resolved against the
container's memory limit. Only the most serious clause applies, so a limit that
is over the container's is not also reported as being over 80% of it.

| Config | Severity |
| --- | --- |
| the hard limit is at or above the container memory limit | `error` |
| less than ~50MiB is left over it — the process runs about that far above what the limiter counts | `warning` |
| the hard limit is above 80% of the container memory limit | `warning` |
| `limit_percentage` with no container memory limit to be a percentage of | `warning` |
| the limiter may use more than the container's memory *request* | `info` |

## Example

```yaml
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512                 # 512Mi enforced in a 256Mi container
    spike_limit_mib: 128
```

```console
$ otelcol-config-lint run --memory-request 256Mi --memory-limit 256Mi config.yaml
config.yaml:13:16: error: processor "memory_limiter" enforces 512Mi, at or above the container memory limit of 256Mi; the kernel kills the collector before the limiter engages [memory-limiter-sizing]
    hint: keep the hard limit near 80% of the container memory limit, and set GOMEMLIMIT to about 80% of the hard limit so the collector collects garbage before the limiter has to refuse data
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md
config.yaml:13:16: info: processor "memory_limiter" may use 512Mi, above the container memory request of 256Mi [memory-limiter-sizing]
    hint: the pod is Burstable and is evicted first under node pressure; collectors usually set the memory request equal to the limit
    docs: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
```

## Telling the linter about the container

A run is not one deployment — an agent DaemonSet at `256Mi` and a gateway
Deployment at `4Gi` sit in the same directory — so the container is resolved
**per file**. See [The deployment
environment](../configuration.md#the-deployment-environment) for the `overrides`
list; `--kubernetes`, `--memory-request` and `--memory-limit` are the
single-file convenience.

A file that matches no override and has no defaults to fall back on simply skips
this rule. That is per file: the rest of the run is unaffected.

## Docs

- [`memorylimiterprocessor`](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/memorylimiterprocessor/README.md)
- [Kubernetes: managing container resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)

## See also

- [`../../testdata/rules/memory-limiter-sizing.yaml`](../../testdata/rules/memory-limiter-sizing.yaml)
