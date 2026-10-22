package rule_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/batchsizebounds"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugexporterverbosity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/insecuretls"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidvalue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimiterconfig"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimitersizing"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/nopersistentqueue"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestLegacyQuotedSettings(t *testing.T) {
	t.Parallel()

	const src = `
receivers: {otlp: {}}
processors:
  batch: {send_batch_size: "1024", send_batch_max_size: "512"}
  memory_limiter: {check_interval: 1s, limit_mib: "512", spike_limit_mib: "1024"}
exporters:
  otlp: {endpoint: localhost:4317, tls: {insecure: "true"}}
  otlphttp: {endpoint: "http://localhost:4318", sending_queue: {enabled: "false"}}
  debug: {verbosity: detailed, sampling_initial: "2", sampling_thereafter: "10"}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp, otlphttp, debug]
`

	for _, version := range []string{"v0.101.0", "v0.104.0", "v0.105.0", "v0.153.0", "", "latest"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			sch := ruletest.Schema()
			sch.CollectorVersion = version
			sch.Components[config.KindProcessor]["batch"].Fields = &schema.Field{Type: "map", Children: map[string]*schema.Field{
				"send_batch_size": {Type: "int"}, "send_batch_max_size": {Type: "int"},
			}}
			legacy := version == "v0.101.0" || version == "v0.104.0"
			unknown := version == "" || version == "latest"
			opts := ruletest.Options{Schema: sch, Env: rule.Environment{Kubernetes: true, MemoryLimit: 256 * 1024 * 1024}}

			for _, tt := range []struct {
				check                    rule.Rule
				legacyCount, modernCount int
			}{
				{invalidvalue.New(), 0, 5},
				{batchsizebounds.New(), 1, 0},
				{insecuretls.New(), 1, 0},
				{nopersistentqueue.New(), 0, 1},
				{memorylimiterconfig.New(), 1, 0},
				{memorylimitersizing.New(), 1, 0},
			} {
				t.Run(tt.check.Name(), func(t *testing.T) {
					t.Parallel()

					found, err := ruletest.RunWith(tt.check, src, opts)
					require.NoError(t, err)

					want := tt.modernCount
					if legacy {
						want = tt.legacyCount
					}

					if unknown && (tt.check.Name() == "invalid-value" || tt.check.Name() == "no-persistent-queue") {
						want = 0
					}

					assert.Len(t, found, want)
				})
			}

			found, err := ruletest.RunWith(debugexporterverbosity.New(), src, opts)
			require.NoError(t, err)
			require.Len(t, found, 1)

			want := diag.Warning
			if legacy {
				want = diag.Info
			}

			assert.Equal(t, want, found[0].Severity)
		})
	}
}

func TestDecodeLegacyScalar(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		value, typ, tag, decoded string
	}{
		{`"1024"`, "int", "!!int", "1024"},
		{`"0100"`, "int", "!!int", "64"},
		{`"0x400"`, "int", "!!int", "1024"},
		{`""`, "int", "!!int", "0"},
		{`"-1"`, "int", "!!int", "-1"},
		{`"1.5"`, "int", "!!str", "1.5"},
		{`"0128"`, "int", "!!str", "0128"},
		{`"9223372036854775808"`, "int", "!!str", "9223372036854775808"},
		{`"1.5"`, "float", "!!float", "1.5"},
		{`""`, "float", "!!float", "0"},
		{`"bad"`, "float", "!!str", "bad"},
		{`"true"`, "bool", "!!bool", "true"},
		{`"TRUE"`, "bool", "!!bool", "true"},
		{`"1"`, "bool", "!!bool", "true"},
		{`"false"`, "bool", "!!bool", "false"},
		{`""`, "bool", "!!bool", "false"},
		{`"yes"`, "bool", "!!str", "yes"},
		{`"${env:VALUE}"`, "int", "!!str", "${env:VALUE}"},
		{`"$${env:VALUE}"`, "bool", "!!str", "$${env:VALUE}"},
		{`"1024"`, "duration", "!!str", "1024"},
		{`"true"`, "string", "!!str", "true"},
		{`1024`, "int", "!!int", "1024"},
		{`null`, "int", "!!null", "null"},
	} {
		t.Run(tt.typ+"/"+tt.value, func(t *testing.T) {
			t.Parallel()

			var doc yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tt.value), &doc))
			node := doc.Content[0]
			before := *node
			ctx := rule.Context{Schema: &schema.Schema{CollectorVersion: "v0.104.0"}}
			got := ctx.DecodeScalar(node, tt.typ)
			assert.Equal(t, tt.tag, got.Tag)
			assert.Equal(t, tt.decoded, got.Value)
			assert.Equal(t, before, *node, "must not mutate the parsed file shared by rules")
			assert.Equal(t, node.Line, got.Line)
			assert.Equal(t, node.Column, got.Column)

			for _, version := range []string{"v0.105.0", "v0.153.0", "", "latest"} {
				ctx.Schema.CollectorVersion = version
				assert.Same(t, node, ctx.DecodeScalar(node, tt.typ))
			}

			ctx.Schema = nil
			assert.Same(t, node, ctx.DecodeScalar(node, tt.typ))
			assert.Nil(t, ctx.DecodeScalar(nil, tt.typ))
		})
	}
}

func TestLegacyUnsignedSettingsRejectSigns(t *testing.T) {
	t.Parallel()

	for _, value := range []string{`"+1024"`, `"-0"`, `"+0"`, `"4294967296"`} {
		for _, tt := range []struct {
			name, settings string
			check          rule.Rule
		}{
			{"batch size", "batch: {send_batch_size: " + value + ", send_batch_max_size: 2048}", batchsizebounds.New()},
			{"batch cap", "batch: {send_batch_size: 1, send_batch_max_size: " + value + "}", batchsizebounds.New()},
			{"memory_limiter", "memory_limiter: {check_interval: 1s, limit_mib: " + value + "}", memorylimiterconfig.New()},
		} {
			t.Run(tt.name+"/"+value, func(t *testing.T) {
				t.Parallel()

				sch := ruletest.Schema()
				sch.CollectorVersion = "v0.101.0"
				found, err := ruletest.RunWith(tt.check, "processors: {"+tt.settings+"}", ruletest.Options{Schema: sch})
				require.NoError(t, err)
				require.NotEmpty(t, found)
				assert.True(t, ruletest.Reports(found, "uint32"))
			})
		}
	}
}

func TestLegacyScalarDecoderProvenance(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, version     string
		decoder           *schema.Decoder
		invalid, insecure int
	}{
		{"old decoder in modern distribution", "v1.0.0", &schema.Decoder{ConfmapVersion: "v0.104.0", Modified: false}, 0, 1},
		{"modern decoder in old distribution", "v0.101.0", &schema.Decoder{ConfmapVersion: "v1.33.0", Modified: false}, 1, 0},
		{"custom label without provenance", "v0.105.0-custom", nil, 0, 0},
		{"local fork", "v0.101.0", &schema.Decoder{ConfmapVersion: "v0.101.0", Modified: true}, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sch := ruletest.Schema()
			sch.CollectorVersion, sch.Distribution, sch.Decoder = tt.version, "custom", tt.decoder

			const src = `exporters: {otlp: {endpoint: localhost:4317, tls: {insecure: "true"}}}`

			found, err := ruletest.RunWith(invalidvalue.New(), src, ruletest.Options{Schema: sch})
			require.NoError(t, err)
			assert.Len(t, found, tt.invalid)
			found, err = ruletest.RunWith(insecuretls.New(), src, ruletest.Options{Schema: sch})
			require.NoError(t, err)
			assert.Len(t, found, tt.insecure)
		})
	}
}

func TestLegacySignedSamplingRemainsValid(t *testing.T) {
	t.Parallel()

	sch := ruletest.Schema()
	sch.CollectorVersion = "v0.101.0"

	const src = `
exporters: {debug: {verbosity: detailed, sampling_initial: "+2", sampling_thereafter: "+10"}}
service: {pipelines: {traces: {exporters: [debug]}}}
`

	found, err := ruletest.RunWith(debugexporterverbosity.New(), src, ruletest.Options{Schema: sch})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, diag.Info, found[0].Severity)
}
