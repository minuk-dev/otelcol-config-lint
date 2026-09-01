# unknown-component

**Default severity:** `error` · **Group:** [Release compatibility](README.md#release-and-distribution-compatibility)

A component type that does not exist in the collector release being targeted.
This is the rule the whole release-pinning idea is for: the same config is valid
on v0.110.0 and broken on v0.157.0.

## What it reports

Any declared component whose type the targeted release's
[schema](../schemas.md) does not carry. The hint is what makes the finding
useful, and it answers whichever question applies:

| Situation | Hint |
| --- | --- |
| declared under the wrong section | `"remotetap" is a processor; declare it under processors` |
| the binary does not carry it | `"k8sattributes" is not in the core distribution; it ships in contrib, k8s` |
| it existed once | `"logging" exists in v0.110.0 but not in v0.157.0` |
| none of the above | `checked against collector v0.157.0 (contrib); did you mean "prometheus"?` |

The third is answered from `components.json`, one document read in one request —
see [Which releases have a component](../schemas.md#which-releases-have-a-component).

## Example

```yaml
receivers:
  nosuchreceiver:                  # no receiver of this type exists in the release
```

```console
$ otelcol-config-lint run --collector-version v0.157.0 config.yaml
config.yaml:3:3: error: unknown receiver type "nosuchreceiver" in collector v0.157.0 (contrib) [unknown-component]
    hint: checked against collector v0.157.0 (contrib)
```

## Notes

- It reports nothing when no schema could be resolved, and
  `--ignore-missing-schemas` turns it off for a custom distribution the registry
  does not describe.
- The distribution matters as much as the release: `--distribution core` is 32
  components, `contrib` is 323. A config that lints clean against contrib can
  name a component the binary you ship does not have.
- Renames are carried through. From v0.157.0 the OTLP gRPC exporter is
  `otlp_grpc`, with `otlp` kept as a deprecated alias; both resolve here, and
  the old name is [`deprecated-component`](deprecated-component.md)'s to report.

## See also

- [`../../testdata/rules/unknown-component.yaml`](../../testdata/rules/unknown-component.yaml)
