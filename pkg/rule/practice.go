package rule

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

func init() {
	Register(
		processorOrder{base{"processor-order",
			"memory_limiter must run first so backpressure is applied before any work", diag.Warning}},
		missingMemoryLimiter{base{"missing-memory-limiter",
			"a pipeline without memory_limiter can be OOM-killed under load", diag.Info}},
		missingBatch{base{"missing-batch",
			"exporting without batching costs throughput and export requests", diag.Info}},
		insecureTLS{base{"insecure-tls",
			"TLS verification disabled on a component that talks over the network", diag.Warning}},
	)
}

// memoryLimiter and batch are matched on type, so instances such as
// "memory_limiter/aggressive" are recognised too.
const (
	memoryLimiterType = "memory_limiter"
	batchType         = "batch"
)

type processorOrder struct{ base }

func (r processorOrder) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		path := "service.pipelines." + p.Key + ".processors"
		for i, ref := range p.Processors {
			if ref.ID.Type == memoryLimiterType && i != 0 {
				ctx.Report(Finding{
					Node: ref.Node, Path: "service." + ref.Path,
					Message: "memory_limiter is processor " + itoa(i+1) + " in pipeline " + quote(p.Key) +
						"; it must be first",
					Hint: "move " + quote(ref.ID.String()) + " to the front of " + path,
				})
			}
			if ref.ID.Type == batchType && i < len(p.Processors)-1 {
				if next := p.Processors[i+1]; next.ID.Type == memoryLimiterType {
					continue // reported above
				}
				ctx.Report(Finding{
					Node: ref.Node, Path: "service." + ref.Path,
					Message:  "batch runs before other processors in pipeline " + quote(p.Key),
					Hint:     "put batch last so upstream processors see individual items and cannot drop whole batches",
					Severity: diag.Info,
				})
			}
		}
	}
}

type missingMemoryLimiter struct{ base }

func (r missingMemoryLimiter) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if hasProcessorType(p, memoryLimiterType) || len(p.Receivers) == 0 {
			continue
		}
		ctx.Report(Finding{
			Node: nodeOr(p.ProcessorsNode, p.KeyNode), Path: "service.pipelines." + p.Key + ".processors",
			Message: "pipeline " + quote(p.Key) + " has no memory_limiter processor",
			Hint:    "add memory_limiter as the first processor to bound the collector's memory use",
		})
	}
}

type missingBatch struct{ base }

func (r missingBatch) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if hasProcessorType(p, batchType) || len(p.Exporters) == 0 {
			continue
		}
		ctx.Report(Finding{
			Node: nodeOr(p.ProcessorsNode, p.KeyNode), Path: "service.pipelines." + p.Key + ".processors",
			Message: "pipeline " + quote(p.Key) + " has no batch processor",
			Hint:    "add batch before the exporters to reduce the number of outgoing requests",
		})
	}
}

func hasProcessorType(p config.Pipeline, typ string) bool {
	for _, ref := range p.Processors {
		if ref.ID.Type == typ {
			return true
		}
	}
	return false
}

type insecureTLS struct{ base }

func (r insecureTLS) Check(ctx *Context) {
	for _, kind := range config.Kinds {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}
		for _, c := range sec.Components {
			for _, hit := range findInsecure(c.ValueNode, kind.Section()+"."+c.ID.String()) {
				ctx.Report(Finding{
					Node: hit.node, Path: hit.path,
					Message: quote(shortPath(hit.path)) + " disables TLS verification for " +
						string(kind) + " " + quote(c.ID.String()),
					Hint: "supply ca_file/cert_file instead of skipping verification outside local testing",
				})
			}
		}
	}
}

type tlsHit struct {
	node *yaml.Node
	path string
}

// findInsecure walks a component's settings for tls.insecure or
// tls.insecure_skip_verify set to true, at any nesting depth. Exporters bury
// these under sub-blocks such as auth or queue settings.
func findInsecure(n *yaml.Node, path string) []tlsHit {
	if n == nil {
		return nil
	}
	var out []tlsHit
	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range mapEntries(n, path) {
			switch e.key {
			case "insecure", "insecure_skip_verify":
				if e.node.Tag == "!!bool" && e.node.Value == "true" {
					out = append(out, tlsHit{node: e.node, path: e.path})
				}
			default:
				out = append(out, findInsecure(e.node, e.path)...)
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findInsecure(item, indexPath(path, i))...)
		}
	}
	return out
}
