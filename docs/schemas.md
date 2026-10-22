# Schemas

The [published registry](https://github.com/minuk-dev/otelcol-config-schemas)
provides schemas per Collector release and distribution: `contrib` (default),
`core`, `k8s` and `otlp`. Pin the release and distribution to the binary you deploy.

## Where they are read from

For offline or reproducible checks, vendor a registry checkout or schema file:

```sh
otelcol-config-lint run --collector-version v0.157.0 \
  --schema-location ./schemas config.yaml
```

Locations can be registry directories or URLs, direct schema files,
`{{.Version}}`/`{{.Distribution}}` templates, or `default` for the public registry.
Repeat `--schema-location` to search in order. Registry directories contain
`index.json` and `<distribution>/<version>.<ext>` files; an optional
`components.json` supplies cross-release component hints.

Schemas must contain components and declare a matching `collectorVersion` and
`distribution`. Legacy schemas without distribution metadata mean `contrib`.
Metadata mismatches fail the run rather than trying a later location.
`latest` selects the newest listed release. An unavailable requested version
fails unless `--allow-nearest-fallback` permits the nearest older release.

Remote locations require HTTPS; `--insecure-schema-location` permits HTTP for a
local registry. Downloads are cached under `$XDG_CACHE_HOME/otelcol-config-lint`
or the platform cache directory. Use `--no-cache` after registry corrections to
avoid reading a previously cached schema.

## Validation coverage in reports

A `valid` result means no enabled rule found a problem at the configured failure
threshold. It does not guarantee Collector startup. Field validation can be
incomplete when a component has no field schema, a schema is open or
unconstrained, or a value is resolved at runtime by a confmap provider.

JSON file results include an optional `coverage` object:

```json
{
  "filename": "config.yaml",
  "status": "valid",
  "coverage": {
    "status": "partial",
    "collectorVersion": "v0.157.0",
    "distribution": "contrib",
    "fieldRules": ["invalid-value"],
    "skipped": [
      {"path": "exporters.otlp.endpoint", "reason": "runtime_value"}
    ]
  }
}
```

The version and distribution describe the schema actually used, including
`latest` resolution and an opted-in nearest-version fallback. Legacy schemas
without a distribution report `contrib`. `fieldRules` lists the enabled built-in
field rules after severity overrides; rules set to `off` are excluded.

Coverage status is `complete` when no coverage gaps were found for those field
rules, `partial` when some component/field coverage is unavailable, and
`not_checked` when no field rules are enabled or a library caller uses
`lint.New` without a component inventory. It describes component field-schema
coverage and unresolved runtime values, not every Collector validation or
whether a rule's findings passed. Disabling a rule narrows what `complete` means.
A detected type/enum error remains a diagnostic, not a coverage gap.

Each skipped path identifies a component, field, or container; a container gap
also covers its descendants. Reasons are `missing_schema` (including list item
schemas), `open_schema` (including unconstrained fields), `runtime_value`, or
`unresolved_structure` (a cyclic alias or a runtime-value scan beyond 16 nesting
levels). Runtime values in service references are included too. Values and
provider expressions are never copied into coverage metadata. Embedded
ConfigMap gaps include `block`, the ConfigMap name/data key. An empty path means
the whole config has no schema coverage. Paths outside skipped containers can
be interpreted against the listed field rules; `complete` is not a promise of
successful Collector startup.

Text output emits one coverage line per affected file, grouping repeated reasons
by count and pointing to JSON for paths. `--summary` adds the number of files
with incomplete coverage. Coverage is independent of diagnostic filtering,
`--ignore-missing-schemas`, validity, and exit codes. Empty inventories still
fail in the CLI and `lint.Prepare` before any report is emitted.

Compatibility: `coverage` and the nonzero `summary.incomplete` count are JSON
additions. Existing status, diagnostic and summary fields keep their meaning.
Parsed lint results appear in JSON `files` even when valid, without needing
`--verbose`, so consumers can inspect complete coverage and the resolved target.
Consumers must tolerate additional fields and must not treat membership in
`files` as a failure; inspect `status`. Read/parse failures and skipped inputs
have no coverage object. Older reports can omit coverage; absence means coverage
was not reported, not that validation was complete.

## Generating a custom distribution schema

Use the OCB builder manifest that builds your Collector:

```sh
go run ./cmd/schemagen generate --builder acme=./builder.yaml --registry ./schemas
otelcol-config-lint run --distribution acme --collector-version v0.157.0 \
  --schema-location ./schemas config.yaml
```

The version comes from the manifest's `dist.otelcol_version` or `dist.version`;
use that value when linting. `acme=` overrides the manifest's distribution name.
Module downloads use the Go toolchain's `GOPROXY`, `GOPRIVATE` and credentials.
Local `replaces` entries are read from their checkouts.

Generated schemas record the resolved `decoder.confmapVersion`, independently
of the distribution version. Generate the Collector module with OCB first and
set `dist.output_path` to its directory. Schemas read that module with
`go list -mod=readonly -m -e -json all`; they do not modify its module files.
If that module is absent or cannot be fully resolved, `decoder.incomplete: true`
marks the component-only graph as insufficient to determine decoder behavior. A replacement pointing to another official
confmap version records that effective version. Local or fork replacements of
confmap or mapstructure set `decoder.modified: true`; unresolved decoder modules
produce an empty `decoder` object. These cases, including incomplete graphs, leave scalar decoding unknown
instead of guessing from the distribution label. Coverage reports
`unknown_decoder` when version-dependent checks cannot be completed.

Legacy schemas without `decoder` use plain upstream release tags only for the
core, contrib, k8s and otlp distributions (or an unspecified distribution).
Prerelease/custom labels and custom distributions need decoder provenance.
Feature-gate overrides and arbitrary fork behavior still require separate
Collector validation.
Use `go run ./cmd/schemagen generate --help` for generator options.

To generate official distributions from a releases checkout at the desired tag:

```sh
make schemas RELEASES=/path/to/opentelemetry-collector-releases \
             SCHEMAS=/path/to/otelcol-config-schemas
```

Public registry generation and backfills are managed in the
[schema registry repository](https://github.com/minuk-dev/otelcol-config-schemas).

## Field schemas

Component inventory comes from upstream `metadata.yaml`. Settings come from Go
`Config` structs and published `config.schema.yaml` files. Unresolved mappings,
including custom unmarshalling that is not fully described, stay open to avoid
rejecting valid settings. `unknown-field` skips unknown-key checks in open maps;
other known fields can still be checked. Runtime values also leave validation
gaps. Inspect the coverage report rather than assuming every field was checked.
