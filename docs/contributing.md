# Contributing

```sh
make build           # bin/otelcol-config-lint
make test            # race tests and coverage
make lint            # golangci-lint
make config-schema   # update settings JSON Schema after changing rules/settings
```

## Adding a rule

1. Add a package under `pkg/rule/` implementing `rule.Rule`, with tests using
   `pkg/rule/ruletest`. Register its constructor in `pkg/ruleset/ruleset.go`.
2. Add a failing config and settings file under `testdata/rules/`; see the
   [fixture guide](../testdata/rules/README.md). CLI tests require a fixture for
   every rule. Tests involving multiple rules belong in `pkg/ruleset`.
3. Run `make config-schema`. Document policy settings or non-obvious limitations
   in [Rule behavior](rules.md); descriptions belong in the rule's CLI metadata.

Keep CLI flags, settings discovery and process exit codes in `pkg/cmd/`.
Schema preparation and the reusable engine belong in `pkg/lint`; see the
[Go library guide](library.md) for concurrency and result contracts.

## Optional integration checks

Ordinary `make test` needs no Collector binary. To compare against an official
Collector, download and verify its release archive, then run:

```sh
OTELCOL_BINARY=/absolute/path/to/otelcol-contrib \
OTELCOL_VERSION=v0.157.0 OTELCOL_DISTRIBUTION=contrib \
  go test -race -tags=integration ./pkg/cmd/otelcol-config-lint \
  -run CollectorCompatibility -count=1 -v
```

The CI matrix is defined in [test.yaml](../.github/workflows/test.yaml).
To run native CLI smoke checks:

```sh
go build -o /tmp/otelcol-config-lint ./cmd/otelcol-config-lint
OTELCOL_LINT_BINARY=/tmp/otelcol-config-lint \
  go test -tags=integration ./test/smoke -count=1 -v
```

The manual [rollout observation workflow](../.github/workflows/rollout-observation.yaml)
compares the synthetic `testdata/rollout` corpus with a pinned candidate and
Collector. Its pins and reproduction command live in the workflow; it is not a
production adoption record or a required deployment check.

## Releasing

Open **Actions → release → Run workflow**, select `main`, and enter a new
`vMAJOR.MINOR.PATCH` version, for example `v0.2.1`.

The workflow checks module files, pins the Action image, and pushes the release
commit and tag together. GoReleaser writes release notes from the commit history,
publishes Linux/macOS/Windows binaries and amd64/arm64 container images, then the
workflow checks the published image and advances the major Action tag (`@v0` for
0.x releases). If a publishing job fails, rerun failed jobs to reuse the tag.

To tag manually instead, pin the Action image before tagging:

```sh
make release-pin RELEASE=v1.2.3
git commit -am 'chore(release): pin the action at v1.2.3'
git tag v1.2.3
git push origin main v1.2.3
```

The release workflow validates the pin, publishes assets, checks the published
image, then advances the major Action tag. See
[release.yaml](../.github/workflows/release.yaml) for the checks.
