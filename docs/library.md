# Using the linter from Go

Import `github.com/minuk-dev/otelcol-config-lint/pkg/lint` to check collector
configuration in a server or another Go program. The engine takes YAML bytes
and returns structured diagnostics; it does not require Cobra, temporary
files, stdout capture, or a subprocess.

## Prepare once, check many inputs

`Prepare` validates built-in rule selection and severities, loads a nonempty
schema, and returns an engine and the selected target. Pin the collector
version and distribution to the binary whose configuration you manage.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/minuk-dev/otelcol-config-lint/pkg/lint"
    "github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
    "github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func main() {
    engine, target, err := lint.Prepare(context.Background(), lint.PrepareOptions{
        Store: schema.Store{Distribution: "contrib"},
        CollectorVersion: "v0.157.0",
        Rules: ruleset.Selection{
            Default: ruleset.DefaultNone,
            Enable: []string{"service-required"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    result := engine.Lint(context.Background(), "collector.yaml", []byte("{}"))
    fmt.Println(target.CollectorVersion, target.Distribution)
    fmt.Println(result.Status, result.Diagnostics[0].Rule)
}
```

This prints `v0.157.0 contrib` and `invalid service-required`. Omit `Rules` to
run all built-in rules. `Enable`, `Disable`, `Severity` (`rule=level` pairs),
and per-rule `Settings` use the same resolver as the CLI. An executable,
network-free version is in `pkg/lint/prepare_test.go` as `ExamplePrepare`.

The zero-value options use the latest contrib schema from the published
registry, all rules, `MinSeverity: diag.Info`, and `FailOn: diag.Error`.
`Strict`, `Embedded`, `IgnoreMissingSchemas`, and `Environment` control the
same checks as their CLI counterparts. `MinSeverity` filters diagnostics;
`FailOn` decides validity independently, so an invalid result can have no
visible diagnostics.

## Schema selection and failures

Configure `PrepareOptions.Store` with the existing `schema.Store` fields:

- `Locations`: a local registry, a template, or a registry URL. A local
  location lets initialization run without network access.
- `Distribution`: `core`, `contrib`, `k8s`, or `otlp` for the published registry.
- `HTTPClient`: the client's transport and timeout policy.
- `CacheDir` / `NoCache`: the on-disk download cache policy.
- `Fs`: the filesystem used for local schemas and that cache.

`PrepareOptions.Fs` is separate: it is only used by `LintFile` and `LintAll`.
`Lint` takes bytes directly and never reads the input name as a file.

Initialization fails when a requested version is unavailable. Use
`errors.As(err, &unknown)` with `var unknown *schema.UnknownVersionError` to
inspect available versions and `unknown.Nearest()`. `errors.Is(err,
lint.ErrEmptySchema)` identifies a schema with no components. Wrapped context
cancellation remains accessible through `errors.Is`.

`AllowNearestFallback: true` explicitly allows the newest available release
that is not newer than the requested release. `Target` reports:

| Field | Meaning |
| --- | --- |
| `RequestedVersion` | normalized requested version, or `latest` |
| `CollectorVersion` | selected schema version |
| `Distribution` | selected distribution, including the default |
| `Fallback` | a nearest older release was selected |

Only use the engine and target as a successful initialization when `err == nil`.
On a schema load failure, target metadata can describe an attempted fallback;
on policy validation failure it is empty. The library prints no warnings.
Include the successful target in server responses or logs so users know which
collector release was actually checked.

## Calling from an API server

Initialize the engine with the server's startup context, then pass the HTTP
request's context to each `Lint` call. The source name is a diagnostic label
and an input to `Environment`; it can be a configuration name instead of a
filesystem path.

A consumer can declare the interface it needs. `*lint.Linter` already implements
this interface, so tests can supply a fake without a library-wide interface:

```go
type ConfigLinter interface {
    Lint(context.Context, string, []byte) lint.Result
}

func validateConfig(
    ctx context.Context,
    engine ConfigLinter,
    name string,
    source []byte,
) (lint.Result, error) {
    result := engine.Lint(ctx, name, source)
    if result.Status == lint.Error {
        return result, result.Err
    }
    return result, nil
}
```

Keep HTTP status codes, authorization and response envelopes in the server.
Bound the request body before reading it, for example with
`http.MaxBytesReader`. `LintReader` uses `io.ReadAll` without a size limit.
Cancellation is checked around parsing and between rules; it does not interrupt
an individual YAML parse or rule already executing.

| Result status | Server interpretation |
| --- | --- |
| `Valid` | no findings at or above `FailOn`; warnings may still exist |
| `Invalid` | configuration findings, including YAML syntax errors |
| `Error` | execution failure, such as cancellation or an input read failure |
| `Skipped` | embedded mode found no collector configuration to check |

Inspect `Status` before interpreting `Err`: a YAML syntax failure is `Invalid`
and can also carry `Err`. Diagnostics retain rule, severity, YAML path, line,
column, hint and documentation link. `Result.Err` is excluded from JSON; use
`Result.Message()` if your response envelope needs an error message. The CLI
JSON formatter is a report format and may omit passing inputs; an API can
serialize results directly instead.

## Sharing engines and deployment information

An engine can serve concurrent calls when its dependencies are safe to share.
Each call parses its own document and builds its own rule context and index.
`New` copies the severity map and rule slice, and `Rules()` returns a slice
copy. These are shallow copies: schemas, rule instances, rule settings and
callbacks must remain immutable during linting. User-supplied filesystem and
lookup implementations must support concurrent access when shared.

`Environment` receives the input name and returns `rule.Environment`. A fixed
deployment can use a callback returning fixed values. `lint.EnvironmentPolicy`
provides path-based defaults and overrides; call `Validate` before using
`policy.Resolve`. Do not change a captured environment or a shared policy for
each HTTP request. For different per-request deployment values, create a
lightweight `New` engine with the already loaded schema, resolved rules and a
callback capturing that request's values.

Prepared engines lazily fetch additional release and distribution information
when an unknown component needs a hint. Both indexes serialize initialization
and retry a build cancelled by its requesting context. Completed best-effort
answers remain cached for the lifetime of the index, including answers affected
by non-cancellation registry failures. Rebuild the engine when refreshing
registry information or changing policy. For a request path that must do no
network I/O, use `New` with a preloaded schema and omit `Availability` and
`Distributions`, or use a local registry with `Prepare`.

## Lower-level construction and settings

Use `New(Options)` when your application already holds a `*schema.Schema`,
configured `[]rule.Rule`, and a severity map, or needs custom rules. `New`
performs no I/O or policy validation. A nil or empty schema deliberately runs
structural checks only; `Prepare` rejects empty schemas instead of silently
reducing validation coverage.

To read a linter settings file explicitly, use `settings.Parse`, call
`File.Normalize()` for legacy keys, and pass
`file.RuleSelection(ruleset.Selection{})` to `PrepareOptions.Rules`. Map the
desired `run` and `issues` values into typed preparation options explicitly.
`Prepare` does not search the working directory or inherit output settings.
The CLI owns settings-file discovery and flag precedence.

`LintFile`, `LintReader`, and `LintAll` remain available for non-HTTP callers.
`LintAll(ctx, paths, workers)` returns a completed `[]Result` in input order.
Cancellation discards the batch and returns an error wrapping `ctx.Err()`.
Formatting and process exit codes are separate from validation.
