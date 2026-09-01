# duplicate-key

**Default severity:** `error` · **Group:** [Structure](README.md#structure)

The same mapping key written twice. YAML keeps the last value and discards the
first, without complaining.

## What it reports

A repeated key anywhere in the document, at the position of the second
declaration.

## Example

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
  otlp:                          # the declaration above is silently discarded
    protocols:
      http:
        endpoint: localhost:4318
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:7:3: error: duplicate key "otlp"; the earlier value is discarded [duplicate-key]
```

The collector starts, listens on 4318 only, and nothing in its log mentions the
gRPC endpoint that was asked for.

## Notes

A `<<` merge key is not a duplicate. Merges are resolved when the file is
parsed, the way the collector resolves them, and a key written in place is
*meant* to win over the one its merge supplies — so a document overriding an
anchor's setting reads as written and gets no finding.

## See also

- [`duplicate-reference`](duplicate-reference.md) for the same component listed
  twice in one pipeline slot, which is a list rather than a mapping.
- [`../../testdata/rules/duplicate-key.yaml`](../../testdata/rules/duplicate-key.yaml)
