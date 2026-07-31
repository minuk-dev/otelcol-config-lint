package rule

import (
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// structureRules check the shape of the document itself.
func structureRules() []Rule {
	return []Rule{
		unknownTopLevelKey{base{"unknown-top-level-key",
			"top-level keys other than the component sections and service are rejected by the collector",
			diag.Error}},
		serviceRequired{base{"service-required",
			"a config must declare a service block with at least one pipeline", diag.Error}},
		unknownServiceKey{base{"unknown-service-key",
			"service accepts only extensions, pipelines and telemetry", diag.Error}},
		invalidPipelineKey{base{"invalid-pipeline-key",
			"pipeline keys must be <signal> or <signal>/<name>", diag.Error}},
		emptyPipeline{base{"empty-pipeline",
			"every pipeline needs at least one receiver and one exporter", diag.Error}},
		unknownPipelineKey{base{"unknown-pipeline-key",
			"pipelines accept only receivers, processors and exporters", diag.Error}},
		duplicateKey{base{"duplicate-key",
			"a mapping key declared twice silently discards the first value", diag.Error}},
		wrongNodeType{base{"wrong-node-type",
			"component sections must be mappings and pipeline slots must be lists", diag.Error}},
	}
}

type unknownTopLevelKey struct{ base }

func (r unknownTopLevelKey) Check(ctx *Context) {
	for _, e := range ctx.File.Unknown {
		ctx.Report(Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "unknown top-level key " + quote(e.Key),
			Hint: "valid top-level keys: connectors, exporters, extensions, processors, receivers, service" +
				suggest(e.Key, []string{"connectors", "exporters", "extensions", "processors", "receivers", "service"}),
		})
	}
}

type serviceRequired struct{ base }

func (r serviceRequired) Check(ctx *Context) {
	svc := ctx.File.Service
	if svc.Node == nil {
		ctx.Report(Finding{
			Node: ctx.File.Root, Path: "service",
			Message: "config has no service block, so nothing will run",
			Hint:    "add a service.pipelines section wiring the declared components together",
		})

		return
	}

	if svc.PipelinesNode == nil {
		ctx.Report(Finding{
			Node: svc.KeyNode, Path: "service",
			Message: "service has no pipelines, so no telemetry will be processed",
		})

		return
	}

	if len(svc.Pipelines) == 0 {
		ctx.Report(Finding{
			Node: svc.PipelinesNode, Path: "service.pipelines",
			Message: "service.pipelines is empty, so no telemetry will be processed",
		})
	}
}

type unknownServiceKey struct{ base }

func (r unknownServiceKey) Check(ctx *Context) {
	for _, e := range ctx.File.Service.Unknown {
		ctx.Report(Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "unknown key " + quote(e.Key) + " in service",
			Hint: "service accepts extensions, pipelines and telemetry" +
				suggest(e.Key, []string{"extensions", "pipelines", "telemetry"}),
		})
	}
}

type invalidPipelineKey struct{ base }

func (r invalidPipelineKey) Check(ctx *Context) {
	valid := make([]string, 0, len(config.Signals()))
	for _, s := range config.Signals() {
		valid = append(valid, string(s))
	}

	for _, p := range ctx.File.Service.Pipelines {
		if isSignal(p.Signal) {
			continue
		}

		ctx.Report(Finding{
			Node: p.KeyNode, Path: "service.pipelines." + p.Key,
			Message: "pipeline " + quote(p.Key) + " does not name a known signal",
			Hint: "pipeline keys look like traces, metrics/internal or logs/2" +
				suggest(string(p.Signal), valid),
		})
	}
}

type emptyPipeline struct{ base }

func (r emptyPipeline) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		path := "service.pipelines." + p.Key
		if len(p.Receivers) == 0 {
			ctx.Report(Finding{
				Node: nodeOr(p.ReceiversNode, p.KeyNode), Path: path + ".receivers",
				Message: "pipeline " + quote(p.Key) + " has no receivers",
			})
		}

		if len(p.Exporters) == 0 {
			ctx.Report(Finding{
				Node: nodeOr(p.ExportersNode, p.KeyNode), Path: path + ".exporters",
				Message: "pipeline " + quote(p.Key) + " has no exporters",
			})
		}
	}
}

type unknownPipelineKey struct{ base }

func (r unknownPipelineKey) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, e := range p.Unknown {
			ctx.Report(Finding{
				Node: e.KeyNode, Path: e.Path,
				Message: "unknown key " + quote(e.Key) + " in pipeline " + quote(p.Key),
				Hint: "pipelines accept receivers, processors and exporters" +
					suggest(e.Key, []string{"receivers", "processors", "exporters"}),
			})
		}
	}
}

type duplicateKey struct{ base }

func (r duplicateKey) Check(ctx *Context) {
	for _, e := range ctx.File.DuplicateKeys {
		ctx.Report(Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "duplicate key " + quote(e.Key) + "; the earlier value is discarded",
		})
	}
}

type wrongNodeType struct{ base }

func (r wrongNodeType) Check(ctx *Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil || isNull(sec.Node) || sec.Node.Kind == yaml.MappingNode {
			continue
		}

		ctx.Report(Finding{
			Node: sec.Node, Path: kind.Section(),
			Message: kind.Section() + " must be a mapping of component id to settings, got " + nodeKind(sec.Node),
		})
	}

	svc := ctx.File.Service
	if svc.ExtensionsNode != nil && !isNull(svc.ExtensionsNode) && svc.ExtensionsNode.Kind != yaml.SequenceNode {
		ctx.Report(Finding{
			Node: svc.ExtensionsNode, Path: "service.extensions",
			Message: "service.extensions must be a list, got " + nodeKind(svc.ExtensionsNode),
		})
	}

	for _, p := range svc.Pipelines {
		for _, slot := range []struct {
			name string
			node *yaml.Node
		}{
			{"receivers", p.ReceiversNode},
			{"processors", p.ProcessorsNode},
			{"exporters", p.ExportersNode},
		} {
			if slot.node == nil || isNull(slot.node) || slot.node.Kind == yaml.SequenceNode {
				continue
			}

			ctx.Report(Finding{
				Node: slot.node, Path: "service.pipelines." + p.Key + "." + slot.name,
				Message: slot.name + " must be a list, got " + nodeKind(slot.node),
				Hint:    "write it as a YAML sequence, e.g. " + slot.name + ": [otlp]",
			})
		}
	}
}

func isSignal(s config.Signal) bool { return slices.Contains(config.Signals(), s) }

func isNull(n *yaml.Node) bool {
	return n == nil || n.Tag == "!!null"
}

func nodeOr(n, fallback *yaml.Node) *yaml.Node {
	if n != nil {
		return n
	}

	return fallback
}

func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "an unexpected node"
	}
}

func quote(s string) string { return "\"" + s + "\"" }

// suggest appends a "did you mean" clause when one candidate is a close match.
func suggest(got string, candidates []string) string {
	if best, ok := bestMatch(got, candidates); ok {
		return "; did you mean " + quote(best) + "?"
	}

	return ""
}

// bestMatch finds the candidate a user most likely meant. Beyond simple typos
// it handles the two mistakes specific to collector configs: writing the Go
// package name instead of the component type ("prometheusreceiver"), and
// missing the underscores upstream added when types were renamed
// ("hostmetrics" for "host_metrics").
func bestMatch(got string, candidates []string) (string, bool) {
	got = strings.ToLower(got)

	trimmed := got
	for _, suffix := range []string{"receiver", "processor", "exporter", "extension", "connector"} {
		if s, ok := strings.CutSuffix(got, suffix); ok && s != "" {
			trimmed = s

			break
		}
	}

	squashed := strings.ReplaceAll(trimmed, "_", "")

	var best string

	bestDist := 3 // only suggest reasonably close matches

	for _, c := range candidates {
		if strings.ReplaceAll(c, "_", "") == squashed {
			return c, true
		}

		if d := editDistance(trimmed, c); d < bestDist {
			best, bestDist = c, d
		}
	}

	return best, best != ""
}

// editDistance is the Levenshtein distance between two short strings.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i

		for j := 1; j <= len(b); j++ { //nolint:varnamelen // j is the inner index of a matrix walk
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}

		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// sortedKeys returns a map's keys in sorted order, for stable messages.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}
