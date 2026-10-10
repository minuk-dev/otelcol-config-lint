package lint_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// benchmarkConfig varies written size; alias variants share one component body.
func benchmarkConfig(components int, aliases bool) []byte {
	var src strings.Builder

	src.WriteString("receivers:\n  otlp: {protocols: {grpc: {endpoint: 'localhost:4317'}}}\nexporters:\n")

	for i := range components {
		switch {
		case !aliases:
			fmt.Fprintf(&src, "  debug/e%d: {verbosity: normal}\n", i)
		case i == 0:
			fmt.Fprintf(&src, "  debug/e%d: &shared {verbosity: normal}\n", i)
		default:
			fmt.Fprintf(&src, "  debug/e%d: *shared\n", i)
		}
	}

	src.WriteString("service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [")

	for i := range components {
		fmt.Fprintf(&src, "debug/e%d,", i)
	}

	src.WriteString("]\n")

	return []byte(src.String())
}

func BenchmarkConfigParse(b *testing.B) {
	for _, aliases := range []bool{false, true} {
		for _, size := range []int{1, 10, 100, 1000} {
			b.Run(fmt.Sprintf("aliases=%t/components=%d", aliases, size), func(b *testing.B) {
				src := benchmarkConfig(size, aliases)
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()

				for b.Loop() {
					_, err := config.Parse("bench.yaml", src)
					require.NoError(b, err)
				}
			})
		}
	}
}

func BenchmarkLint(b *testing.B) {
	cat, err := schema.ReadFile(repoSchemas + "/core/v0.157.0.json")
	require.NoError(b, err)

	l := lint.New(lint.Options{Schema: cat})

	for _, aliases := range []bool{false, true} {
		for _, size := range []int{1, 10, 100, 1000} {
			b.Run(fmt.Sprintf("aliases=%t/components=%d", aliases, size), func(b *testing.B) {
				src := benchmarkConfig(size, aliases)
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()

				for b.Loop() {
					result := l.Lint(b.Context(), "bench.yaml", src)
					require.Equal(b, lint.Valid, result.Status)
				}
			})
		}
	}
}

func BenchmarkAliasGraph(b *testing.B) {
	fixture, err := os.ReadFile("../../testdata/aliases/amplification.yaml")
	require.NoError(b, err)

	cat, err := schema.ReadFile(repoSchemas + "/core/v0.157.0.json")
	require.NoError(b, err)

	l := lint.New(lint.Options{Schema: cat})
	written := string(fixture)
	end := strings.Index(written, "\nextensions:")
	require.Positive(b, end)

	// Six aliases per level grow the expanded graph while the file stays small.
	for _, depth := range []int{0, 2, 4, 5} {
		start := strings.Index(written, fmt.Sprintf("    nest%d:", depth+1))
		require.Positive(b, start)
		require.Greater(b, end, start)

		src := []byte(written[:start] + written[end:])

		b.Run(fmt.Sprintf("depth=%d/parse", depth), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()

			for b.Loop() {
				_, err := config.Parse("bench.yaml", src)
				require.NoError(b, err)
			}
		})
		b.Run(fmt.Sprintf("depth=%d/lint", depth), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()

			for b.Loop() {
				result := l.Lint(b.Context(), "bench.yaml", src)
				require.Equal(b, lint.Valid, result.Status)
			}
		})
	}
}
