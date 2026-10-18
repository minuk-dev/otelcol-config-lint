# otelcol-config-lint

Lint OpenTelemetry Collector YAML against the release and distribution you run.
No Collector binary is needed; component schemas are fetched from the
[published registry](https://github.com/minuk-dev/otelcol-config-schemas).

## Install and run

```sh
go install github.com/minuk-dev/otelcol-config-lint/cmd/otelcol-config-lint@latest
otelcol-config-lint run --collector-version v0.157.0 config.yaml
```

Release binaries are available for Linux, macOS and Windows. To use the container:

```sh
docker run --rm -v "$PWD:/workspace" \
  ghcr.io/minuk-dev/otelcol-config-lint:latest run --summary ./configs
```

```sh
otelcol-config-lint run --help        # flags and examples
otelcol-config-lint list rules        # rule descriptions and effective severities
otelcol-config-lint list versions     # available schema versions
```

Exit codes: `0` passed, `1` failed validation, `2` could not run.
Pin `--collector-version` and `--distribution` to the deployed Collector.
For reproducible offline checks, also pin a local schema with
`--schema-location`; see [Schemas](docs/schemas.md).

## GitHub Action

```yaml
- uses: minuk-dev/otelcol-config-lint@v0
  with:
    files: ./configs
    collector-version: v0.157.0
    strict: true
    file-summary: true
```

Set `embedded: true` to check Collector configs inside Kubernetes ConfigMap
literal `data` blocks. Findings refer to the manifest's lines and data keys.

The Action defaults to inline PR annotations. GitHub limits each step to 10
warning and 10 error annotations; use `output: json` or `output: text` for all
findings. `file-summary: true` adds checked-file results to the job summary.
See [action.yml](action.yml) for all inputs, defaults and outputs.
Omitted or blank policy inputs inherit the settings file; explicit values,
including `false`, override it.

`@v0` follows v0 releases. Pin a full release tag and a schema snapshot to hold
CI to fixed inputs; the Action's linter image is pinned, but the default schema
registry updates independently.

## Settings

Commit `.otelcol-config-lint.yaml` to share policy:

```yaml
version: "1"
run:
  collectorVersion: v0.157.0
  distribution: contrib
  strict: true
rules:
  disable: [missing-batch]
issues:
  failOn: error
```

- [Configuration](docs/configuration.md): discovery, precedence, exceptions and deployment settings.
- [Rule behavior](docs/rules.md): analysis limits and policy choices beyond the diagnostics.
- [Schemas](docs/schemas.md): offline use, validation coverage and custom distributions.
- [Go library](docs/library.md): embed the linter in another program.
- [Contributing](docs/contributing.md): development and releases.
