# Rule behavior

Use `otelcol-config-lint list rules` for descriptions and effective severities.
Runnable examples live in [testdata/rules](../testdata/rules/README.md).
Diagnostics include a hint and, where applicable, an upstream documentation link.
This page covers behavior that needs context beyond a single finding.

Quoted numbers and booleans follow the resolved decoder's default decoding:
releases before `v0.105.0` accept values such as `"1024"` and `"true"` for numeric
and boolean fields. Type checks and rules that compare those settings use the
same conversion. From `v0.105.0`, those strings are type errors. Overrides of
`confmap.strictlyTypedInput` through Collector feature gates are not modeled.
Custom schemas use `decoder` provenance rather than the distribution version.
If decoding is unknown, convertible quoted scalars are left unresolved and
coverage reports `unknown_decoder`. Batch sizes and memory limits validate the
original text as uint32, rejecting signed strings such as `"+1024"` and `"-0"`.

## Static analysis limits

YAML anchors, aliases and `<<` merges are resolved before checking. Explicit
keys override merged values, and findings retain their source positions, which
can point to an anchor definition. Cyclic aliases and expansions exceeding
100,000 YAML nodes are rejected.

`${env:...}`, `${file:...}` and other runtime providers are not evaluated.
A partly literal value can still be reported when its known portion proves the
finding, such as `0.0.0.0:${env:PORT}`. Dynamic service references leave usage
unknown: dependent unused-component, connector-wiring and missing-processor
checks can be skipped. Literal references in the same list are still checked.
Validate the resolved config with the Collector before deployment.

Field rules depend on the selected schema. An open or missing field schema can
leave settings unchecked; a passing result does not establish startup validity.
See [validation coverage](schemas.md#validation-coverage-in-reports) for the
reported gaps. Use a matching schema for a private distribution rather than
hiding missing components with `--ignore-missing-schemas`.

## Rule settings and exceptions

Use [configuration](configuration.md) for line suppressions, file-specific rule
exceptions, severity overrides and deployment resources. Explicit severity
overrides replace per-finding levels as well as the rule's default level.

`component-stability` checks used components per signal: development is a warning,
alpha is info, and beta or above is silent. To retain development warnings while
omitting alpha notices:

```yaml
rules:
  settings:
    component-stability:
      report_alpha: false
```

`deprecated-component` handles deprecated and unmaintained components separately.
Both rules follow the targeted release's schema, so upgrading the target can
change findings without changing the config.

## Batching

`missing-batch` accepts either a `batch` processor or exporter
`sending_queue.batch`. With mixed exporter behavior it reports only unbatched
legs. A connector is not an exporter leg out of the Collector. Queue batching
on a sibling exporter does not batch a connector's output.

A downstream pipeline is considered batched when all ingress paths are already
batched through same-signal `forward` connectors with declared processors.
Cycles, unknown references, missing producers and unbatched fan-in are
inconclusive. Other connectors, including `routing`, retain the downstream check.

For a custom processor whose batching behavior you have verified:

```yaml
rules:
  settings:
    missing-batch:
      additional_processors: [resourcebatch]
```

This also applies upstream through `forward`; the schema does not prove that a
custom processor batches. Queue-batcher hints are offered only when the target
schema supports that setting; otherwise the hint recommends the processor.

`batch-size-bounds` accounts for implicit defaults: `send_batch_size` triggers a
send, while `send_batch_max_size` caps it. A nonzero maximum below the default
trigger of 8192 can fail even when no trigger is written. Before Collector
`v0.127.0`, an explicit `null` also retains that default; from `v0.127.0` it
decodes to zero. The rule uses resolved decoder metadata when available, with
a release-version fallback for legacy official schemas. If decoder behavior
is unknown, comparisons depending on that null default are skipped and
coverage reports `unknown_decoder`.

## Processor order and memory

`processor-order` checks `memory_limiter` first, `batch` last, and enrichment
before sampling. The enrichment group includes `k8sattributes`,
`resourcedetection`, `resource` and their renamed types. It excludes `attributes`,
which can legitimately redact after sampling.

`memory-limiter-sizing` needs [deployment resources](configuration.md#the-deployment-environment).
A positive `limit_mib` takes precedence over `limit_percentage`; percentages need
a known container limit. The rule distinguishes a hard limit at or above the
container limit (error), insufficient headroom (warning), and a limit above the
memory request (info). A Collector-valid config can still fail this deployment
policy check.

## Network and secret checks

`receiver-binds-all-interfaces` checks explicit listener `endpoint` and
`listen_address` fields on used receivers and enabled extensions. Exporter and
connector endpoints are destinations. `pprof` and `zpages` are checked by
`debug-extension-exposed`; health-check extensions are excluded so kubelet
probes remain reachable. Neither rule validates DNS resolution or unwritten
default endpoints. The listener rule does not inspect `service.telemetry` readers.

A wildcard listener is info only when the deployment explicitly sets
`run.kubernetes.enabled: true` and `hostNetwork: false`. A missing or true
`hostNetwork` retains warning. Path overrides replace environment defaults,
including `hostNetwork`; see [configuration](configuration.md#the-deployment-environment).
Debug extensions retain their separate exposure checks.

`hardcoded-secret` examines every declared component, including unused ones,
and reports paths without printing credential values. It uses key-name heuristics,
not general secret detection. Runtime expansions, placeholders, booleans and
keys describing credential locations or metadata are excluded. Metadata suffix
exclusions do not apply to HTTP header names.
