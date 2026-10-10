package lint

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// ErrEmptySchema means the selected schema contains no components.
var ErrEmptySchema = errors.New("component inventory is empty")

// PrepareOptions configures a schema-backed linter without CLI flags or settings
// discovery. Its zero value uses the latest contrib schema and all rules.
type PrepareOptions struct {
	// Store selects schema locations, distribution, HTTP client and cache policy.
	Store schema.Store
	// CollectorVersion selects a release; empty means latest.
	CollectorVersion string
	// AllowNearestFallback permits the nearest older schema when no exact one exists.
	AllowNearestFallback bool
	// Rules selects and configures built-in rules. Empty means all rules.
	Rules ruleset.Selection
	// Fs is the filesystem LintFile reads from, independent of Store.Fs.
	Fs afero.Fs
	// Environment resolves deployment information for each input name.
	Environment func(path string) rule.Environment
	// Strict reports lenient findings at their strict severity.
	Strict bool
	// IgnoreMissingSchemas silences unknown-component unless explicitly re-levelled.
	IgnoreMissingSchemas bool
	// MinSeverity filters reported findings; empty means info.
	MinSeverity diag.Severity
	// FailOn determines invalid results; empty means error.
	FailOn diag.Severity
	// Embedded checks collector configs in Kubernetes ConfigMap data blocks.
	Embedded bool
}

// Target describes the schema selected by Prepare. On a load error it describes
// the attempted selection, not a successfully prepared linter.
type Target struct {
	RequestedVersion string `json:"requestedVersion"`
	CollectorVersion string `json:"collectorVersion"`
	Distribution     string `json:"distribution"`
	Fallback         bool   `json:"fallback"`
}

// Prepare validates rule policy and severities, loads a nonempty schema and
// builds a reusable linter. It performs no settings discovery or output. Target
// also reports a fallback attempted before a load failure, so callers can explain
// it without putting logging into the library. Use New for structural checks or
// a schema and custom rules already held by the caller.
func Prepare(ctx context.Context, opts PrepareOptions) (*Linter, Target, error) {
	var target Target

	err := ctx.Err()
	if err != nil {
		return nil, target, fmt.Errorf("prepare cancelled: %w", err)
	}

	resolved, err := ruleset.Resolve(opts.Rules)
	if err != nil {
		return nil, target, err
	}

	minSeverity, err := prepareSeverity("minSeverity", opts.MinSeverity, diag.Info)
	if err != nil {
		return nil, target, err
	}

	failOn, err := prepareSeverity("failOn", opts.FailOn, diag.Error)
	if err != nil {
		return nil, target, err
	}

	cat, target, err := loadTarget(ctx, opts)
	if err != nil {
		return nil, target, err
	}

	return New(Options{
		Schema: cat, Fs: opts.Fs,
		Availability:         NewVersionIndex(opts.Store),
		Distributions:        NewDistributionIndex(opts.Store.WithDistribution(target.Distribution), cat.CollectorVersion),
		Rules:                resolved.Rules,
		Severities:           resolved.Severities,
		Environment:          opts.Environment,
		Strict:               opts.Strict,
		IgnoreMissingSchemas: opts.IgnoreMissingSchemas,
		MinSeverity:          minSeverity,
		FailOn:               failOn,
		Embedded:             opts.Embedded,
	}), target, nil
}

func prepareSeverity(name string, value, fallback diag.Severity) (diag.Severity, error) {
	if value == "" {
		return fallback, nil
	}

	severity, err := diag.ParseSeverity(string(value))
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}

	return severity, nil
}

func loadTarget(ctx context.Context, opts PrepareOptions) (*schema.Schema, Target, error) {
	target := Target{
		RequestedVersion: schema.Normalize(opts.CollectorVersion),
		CollectorVersion: "", Distribution: opts.Store.Distribution, Fallback: false,
	}
	if target.RequestedVersion == "" {
		target.RequestedVersion = schema.Latest
	}

	if target.Distribution == "" {
		target.Distribution = schema.DefaultDistribution
	}

	cat, err := opts.Store.Load(ctx, target.RequestedVersion)
	if ctx.Err() != nil {
		return nil, target, fmt.Errorf("prepare cancelled: %w", ctx.Err())
	}

	if err != nil {
		var unknown *schema.UnknownVersionError
		if opts.AllowNearestFallback && errors.As(err, &unknown) {
			if near, ok := unknown.Nearest(); ok {
				target.CollectorVersion, target.Fallback = near, true
				cat, err = opts.Store.Load(ctx, near)
			}
		}
	}

	if ctx.Err() != nil {
		return nil, target, fmt.Errorf("prepare cancelled: %w", ctx.Err())
	}

	if err != nil {
		return nil, target, fmt.Errorf("load schema: %w", err)
	}

	target.CollectorVersion = cat.CollectorVersion
	if cat.Count() == 0 {
		return nil, target, fmt.Errorf("load schema: %w", ErrEmptySchema)
	}

	return cat, target, nil
}
