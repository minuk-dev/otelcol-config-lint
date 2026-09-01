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
pkg/lint/                     the engine and the output formatters
pkg/diag/                     diagnostics, severities and positions
pkg/quantity/                 Kubernetes memory quantities, parsed and printed back
pkg/version/                  the linter's own version, stamped at build time
docs/rules/                   one page per rule
testdata/rules/               one invalid config per rule, with the run that shows it
action.yml                    the GitHub Action; Dockerfile wraps the released image it runs
build/docker/Dockerfile       the distroless linter image releases publish
```

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
