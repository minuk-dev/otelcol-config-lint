# undefined-extension-reference

**Default severity:** `error` · **Group:** [Wiring](README.md#wiring)

An extension that a component's *own settings* name — an exporter's
`sending_queue.storage`, an `auth.authenticator` — that nothing declares, or
that is declared and then left out of `service.extensions`.

[`undefined-reference`](undefined-reference.md) never sees these: that rule
walks the `service` block, and these sit several levels down inside a component.
Getting one wrong is a startup failure, and the collector's own error does not
say which of the three places is missing the name.

## What it reports

Two cases, with different fixes:

| Config | Finding |
| --- | --- |
| nothing declares the extension | `... which is not declared under extensions` |
| declared, but not in `service.extensions` | `... which is declared but missing from service.extensions, so the collector never starts it` |

## Example

```yaml
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage        # nothing declares "file_storage"
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:17:16: error: exporter "otlp" references storage extension "file_storage" which is not declared under extensions [undefined-extension-reference]
    hint: no extensions are declared in this config
    docs: https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporterhelper/README.md
```

## Which settings hold an extension

The [field schema](../schemas.md#field-schemas) says so. A setting that decodes
into a `component.ID` is marked when the schema is generated, since nothing in
the value itself says so — a storage id and a directory path are both strings.
So a setting upstream adds is checked as soon as its schema lands, rather than
waiting for a list in the linter to be updated.

The role the marker carries — `storage`, `auth`, or a plain `extension` for a
key the generator has no name for — is only what the diagnostic calls it.
Schemas generated before the marker existed keep working: the rule knows
`sending_queue.storage` and `auth.authenticator` on its own, and that fallback
falls away as the registry is regenerated.

## See also

- [`no-persistent-queue`](no-persistent-queue.md), which asks for the storage
  extension this rule then checks.
- [`../../testdata/rules/undefined-extension-reference.yaml`](../../testdata/rules/undefined-extension-reference.yaml)
