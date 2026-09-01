# component-stability

**Default severity:** `info` · **Group:** [Release compatibility](README.md#release-and-distribution-compatibility)

Components below beta stability can change or break without notice. Upstream
records a stability level per component per signal, and this rule reads it back.

## What it reports

One finding per component, for the first signal it is wired into that has a
level worth mentioning:

| Level | Severity | Message |
| --- | --- | --- |
| development | `warning` | `... is in development and may change without notice` |
| alpha | `info` | `... is alpha; its configuration can change between releases` |
| beta and above | — | nothing |

Extensions have no signal, so they are reported without the `for traces` clause.
The other end — deprecated and unmaintained — belongs to
[`deprecated-component`](deprecated-component.md).

## Example

```yaml
extensions:
  remotetap:                       # in development
    endpoint: localhost:12001
service:
  extensions: [remotetap]
```

```console
$ otelcol-config-lint run config.yaml
config.yaml:19:3: warning: extension "remotetap" is in development and may change without notice [component-stability]
```

## Notes

- Only components a pipeline actually uses are reported. A declaration nothing
  wires up is [`unused-component`](unused-component.md)'s.
- Stability is per signal: a receiver can be stable for traces and alpha for
  profiles, and what you get told is the level for the pipeline you put it in.
- This is the rule most affected by upgrading `--collector-version`, since a
  component crossing the beta line changes the findings an unchanged config
  gets. `schemagen --summary` calls those out for exactly that reason.

## See also

- [`../../testdata/rules/component-stability.yaml`](../../testdata/rules/component-stability.yaml)
