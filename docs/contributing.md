# Working on the linter

## Layout

```
cmd/otelcol-config-lint/      the linter entry point
cmd/schemagen/                the schema generator entry point
pkg/cmd/otelcol-config-lint/  the cobra command: flags, settings files, reporting
pkg/cmd/schemagen/            the generator: harvests upstream metadata into schemas
pkg/scanner/                  expands the given paths into the files to lint
pkg/sets/                     a set built on a map, in the shape of k8s.io/apimachinery
pkg/config/                   YAML parsing that keeps positions, so findings have line numbers
pkg/schema/                   schema types, version resolution and location lookup
pkg/rule/                     what a rule is: the interface, the context and the shared readers
pkg/rule/<rule-name>/         one rule and its tests, one package each
pkg/rule/ruletest/            the fixtures a rule's tests are written against
pkg/ruleset/                  the registry: every rule collected into one set
pkg/lint/                     reusable preparation, the engine and output formatters
pkg/diag/                     diagnostics, severities and positions
pkg/quantity/                 Kubernetes memory quantities, parsed and printed back
pkg/version/                  the linter's own version, stamped at build time
docs/rules/                   one page per rule
testdata/rules/               one invalid config per rule, with the run that shows it
action.yml                    the GitHub Action; Dockerfile wraps the released image it runs
build/docker/Dockerfile       the distroless linter image releases publish
```

## Library boundary

`pkg/lint.Prepare` owns schema selection, fallback and built-in rule policy
validation. Both the CLI and Go callers use it. Keep Cobra, settings discovery,
flag precedence, logging and process exit codes in `pkg/cmd/`; preparation
returns target metadata instead of printing a fallback warning.

`pkg/lint.New` remains the lower-level constructor for resolved dependencies
and custom rules. Preserve the distinction between invalid configuration and
execution failure when changing result handling. Shared schemas and rule
configuration must remain immutable during concurrent calls. See
[the library guide](library.md) for the public contract and usage examples.

## Development

```sh
make test            # go test -race with coverage
make lint            # golangci-lint
make build           # ./bin/otelcol-config-lint
make build-snapshot  # every release target, the way CI builds them
make schemas         # regenerate the schemas into ../otelcol-config-schemas
```

Nothing injects the version. The Go toolchain records the module version and the
repository state in every binary it builds, and `pkg/version` reads that back,
so a tagged build reports its tag because it was built from that tag:

| built from | `otelcol-config-lint version` |
| --- | --- |
| a tag | `v1.2.3`, or `v1.2.3+dirty` from a modified tree |
| `go install ...@v1.2.3` | `v1.2.3` |
| a commit between tags | `b7dbdd5`, or `b7dbdd5-dirty` |
| no repository, or `go run` | `devel` |

### Fuzzing and regression benchmarks

The four fuzz targets in `pkg/lint/linter_fuzz_test.go` exercise `config.Parse`,
`settings.Parse`, `schema.Read`, and the complete rule set against the committed
core v0.157.0 schema, without network access. Seeds include existing config,
settings and schema fixtures plus malformed references, nulls, extra documents,
merges and cyclic aliases. Inputs above 128 KiB are skipped. Targets check for
panics, repeatable acceptance and decoded values, and deterministic lint results
with valid diagnostic positions when a line is available, including yaml.v3's
virtual next line at EOF for syntax diagnostics. Schema error text is
not compared because map iteration can change which invalid field is found first.

Run the same bounded smoke budget as CI, or increase `-fuzztime` locally:

```sh
for target in FuzzConfigParse FuzzSettingsParse FuzzSchemaRead FuzzLint; do
  go test ./pkg/lint -run '^$' -fuzz "^${target}$" \
    -fuzztime=15s -fuzzminimizetime=5s -parallel=2 -timeout=2m || exit 1
done
```

Go fuzzing isolates mutations in worker processes. The ordinary cyclic-alias
and amplification regressions also run in subprocesses with a 10-second limit,
so a hang or fatal stack overflow cannot take down the parent test runner.
Ordinary `go test` runs all committed seeds. CI uploads failing corpus files;
download them into `pkg/lint/testdata/fuzz/<target>/`, reproduce with
`go test ./pkg/lint -run '<target>/<hash>'`, and commit them with the fix so they
remain regressions. See the [Go fuzzing guide](https://go.dev/doc/security/fuzz/)
for corpus handling.

Compare parsing and full lint costs for 1, 10, 100 and 1,000 component instances,
using both ordinary mappings and a repeatedly aliased component body. The alias
graph benchmark also reuses the amplification regression fixture at depths
0, 2, 4 and 5 (six references per level, below the expansion limit):

```sh
go test ./pkg/lint -run '^$' -bench 'Benchmark(ConfigParse|Lint|AliasGraph)$' -benchmem -count=5
```

The benchmarks report input throughput, bytes and allocations per operation.
Fixture generation and schema loading are outside the timed loop. Compare
results on the same machine; CI does not enforce wall-clock thresholds.

### Collector compatibility tests

The separate `collector-compatibility` CI job runs official **core (`otelcol`)**
and **contrib (`otelcol-contrib`)** binaries at **v0.110.0** and **v0.157.0**, the
two releases represented by the committed schema fixtures. It verifies the
release archive's published SHA-256 checksum before extraction. Ordinary
`make test` runs stay offline and do not need a Collector binary.

To reproduce one matrix entry, download the archive and checksum file for your
platform from the [upstream releases](https://github.com/open-telemetry/opentelemetry-collector-releases/releases),
verify the checksum, extract the binary, and run from the repository root:

```sh
OTELCOL_BINARY=/absolute/path/to/otelcol-contrib \
OTELCOL_VERSION=v0.110.0 OTELCOL_DISTRIBUTION=contrib \
  go test -race -tags=integration ./pkg/cmd/otelcol-config-lint \
  -run CollectorCompatibility -count=1 -v
```

Repeat with both versions and distributions to reproduce the full matrix.
`OTELCOL_VERSION` and `OTELCOL_DISTRIBUTION` default to `v0.157.0` and `core`.
The test checks the binary's exact version and distribution before validation.

The minimal corpus in `testdata/compatibility/` covers accepted and rejected
configs: null/default handling, named identifiers, reference shapes, duration
and verbosity decoding, a controlled environment endpoint, and Prometheus's
custom configuration decoder (contrib only). Each fixture is checked against
both `Collector validate --config` and the strict linter using the exact
committed schema for that release and distribution. Expected exit codes must
hold independently, so both tools unexpectedly accepting a rejected fixture
also fails the job. Best-practice warnings remain visible but do not count as
Collector rejection; `null-defaults.yaml` explicitly checks that distinction.

The dynamic service reference tests also run for each matrix entry, with only
synthetic environment values. Their intentional difference is asserted:
Collector resolves an undeclared runtime reference and rejects it, while static
linting leaves environment provider references unresolved. Any further
exception needs an explicit, narrowly scoped expectation and an explanation;
do not disable rules globally to make the corpus pass.

Verbose output records binary identity, distribution, schema path and SHA-256,
the minimal YAML, and both validation outputs. CI retains that output as a
`collector-compatibility-<distribution>-<version>` artifact, including failures.
No user credentials or provider network calls are needed by the tests.

### Go batch API

`(*lint.Linter).LintAll` returns `([]lint.Result, error)` and waits for every
worker to exit. Results follow input order, including duplicate paths. File
read, parse and validation failures stay in each `Result`; cancellation discards
the whole batch and returns the context error. Shared schemas, maps and rule
settings must remain unchanged during linting, and a rule's `Check` method must
support concurrent calls on the same instance.

This replaces the exported `<-chan lint.Result` return type and is a source
breaking change for Go callers, including callers outside this repository.
When updating from v0.1.0, replace channel iteration with an error check followed
by slice iteration:

```go
results, err := linter.LintAll(ctx, paths, workers)
if err != nil {
    return err
}
for _, result := range results {
    // Report the completed result.
}
```

### CI

This repository's own CI runs the tests with coverage reported on the pull
request, builds every release target, lints the example configs in `testdata/`,
exercises the action against them, checks that
`otelcol-config-lint.schema.json` has not fallen behind the rules the linter
carries, and publishes binaries and container images from tags.

The weekly job that generates schemas for each new collector release lives in
the [schema registry](https://github.com/minuk-dev/otelcol-config-schemas), not
here — see [Keeping the registry
current](schemas.md#keeping-the-registry-current).

### Releasing

Bump then tag, because the action's image is pinned at the release it ships in
and a tag cannot reference an image built from itself:

```sh
make release-pin RELEASE=v1.2.3
git commit -am 'chore(release): pin the action at v1.2.3'
git tag v1.2.3 && git push origin main v1.2.3
```

The release workflow refuses a tag whose pin names another release, and moves
`v1` onto the release once it has published.

## Adding a rule

A rule is a package. `pkg/rule` defines what one is — the `Rule` interface, the
`Context` a check reads, the `Finding` it reports — along with the YAML readers
and phrasing helpers every rule shares. `pkg/ruleset` is the only place that
knows about all of them, which is what keeps a rule free to import the
vocabulary it is written in.

So a new rule is one directory and one line:

```go
// pkg/rule/mynewrule/rule.go
package mynewrule

// New builds the rule.
func New() rule.Rule {
	return myNewRule{rule.NewBase("my-new-rule",
		"one line saying what this reports", diag.Warning)}
}

type myNewRule struct{ rule.Base }

func (r myNewRule) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		ctx.Report(rule.Finding{
			Node: p.KeyNode, Path: "service.pipelines." + p.Key,
			Message: "pipeline " + rule.Quote(p.Key) + " is doing the thing",
			Hint:    "stop doing the thing",
		})
	}
}
```

Then add `mynewrule.New()` to the list in `pkg/ruleset/ruleset.go`, and write
`pkg/rule/mynewrule/rule_test.go` against `pkg/rule/ruletest`, which carries the
stand-in schema and the clean config every rule's tests start from:

```go
found, err := ruletest.Run(mynewrule.New(), src)
```

A test that is about two rules meeting — one reporting where the other stays
quiet — belongs in `pkg/ruleset` instead, which is where the whole set is run at
once.

Then the rule needs a config in `testdata/rules`: `my-new-rule.yaml`, which
breaks it, with a comment naming the rule on the line that breaks it, and
`my-new-rule.settings.yaml`, which says which rules the run has on in the shape
golangci-lint uses:

```yaml
rules:
  enable: [my-new-rule]  # must report on my-new-rule.yaml
  disable: []            # switched off for this run, and must stay quiet
  settings: {}           # per-rule options, keyed by rule name
```

The tests refuse a rule with no fixture, so this is where a rule is shown
working through the real command line, the published schemas and the severity
gate rather than against the stand-in schema.
[`testdata/rules/README.md`](../testdata/rules/README.md) has the rest of the
schema, including how a fixture selects its own collector release or states the
container it runs in.

Last, add `docs/rules/my-new-rule.md` and a row in
[`docs/rules/README.md`](rules/README.md), following the shape the other pages
use: what it reports, a config that trips it, and what it stays quiet about.
