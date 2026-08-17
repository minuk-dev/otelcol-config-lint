// Package processororder reports a processor standing in the wrong place in a
// pipeline.
package processororder

import (
	"slices"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// The two groups this rule has something to say about between the ends of the
// chain: enrichment adds the attributes a sampling decision is written against,
// and sampling decides what is kept. These are matched on type too, so
// k8sattributes/pods and tail_sampling/errors are covered.
//
// Two of the enrichment processors have been renamed upstream, and both
// spellings still resolve, so the group carries both of each: a config written
// against a recent release uses the new name, one written before v0.157.0 the
// old one, and which of them a file should say is deprecated-component's
// business rather than this rule's.
const (
	k8sattributesType            = "k8sattributes"
	k8sAttributesRenamedType     = "k8s_attributes"
	resourcedetectionType        = "resourcedetection"
	resourcedetectionRenamedType = "resource_detection"
	resourceType                 = "resource"
	tailSamplingType             = "tail_sampling"
	probabilisticSamplerType     = "probabilistic_sampler"
)

// New builds the rule.
//
// Its three clauses are the same finding in three positions: the config loads,
// the collector runs, and what comes out the far end is not what the author
// asked for.
//
// Two of them are about the ends of the chain -- memory_limiter first so
// backpressure is applied before any work is done, batch last so the
// processors ahead of it see individual items. The third is about the middle,
// where enrichment and sampling can be written in either order and only one of
// them is the order that works.
func New() rule.Rule {
	return processorOrder{rule.NewBase("processor-order",
		"processors run in the order they are listed, and some of them have one place they work",
		diag.Warning)}
}

type processorOrder struct{ rule.Base }

func (r processorOrder) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for i, ref := range p.Processors {
			r.checkLimiterFirst(ctx, p, i, ref)
			r.checkBatchLast(ctx, p, i, ref)
			r.checkSampling(ctx, p, i, ref)
		}
	}
}

// checkLimiterFirst reports a memory_limiter anywhere but at the front, where
// backpressure is applied before any work is done.
func (r processorOrder) checkLimiterFirst(ctx *rule.Context, p config.Pipeline, i int, ref config.Ref) {
	if ref.ID.Type != rule.MemoryLimiterType || i == 0 {
		return
	}

	ctx.Report(rule.Finding{
		Node: ref.Node, Path: ref.Path,
		Message: "memory_limiter is processor " + rule.Itoa(i+1) + " in pipeline " + rule.Quote(p.Key) +
			"; it must be first",
		Hint: "move " + rule.Quote(ref.ID.String()) + " to the front of " + processorsPath(p),
		Docs: rule.MemoryLimiterDocs,
	})
}

// checkBatchLast reports a batch with other processors behind it, which then
// see whole batches rather than individual items.
func (r processorOrder) checkBatchLast(ctx *rule.Context, p config.Pipeline, i int, ref config.Ref) {
	if ref.ID.Type != rule.BatchType || i >= len(p.Processors)-1 {
		return
	}

	if next := p.Processors[i+1]; next.ID.Type == rule.MemoryLimiterType {
		return // reported by checkLimiterFirst
	}

	ctx.Report(rule.Finding{
		Node: ref.Node, Path: ref.Path,
		Message:  "batch runs before other processors in pipeline " + rule.Quote(p.Key),
		Hint:     "put batch last so upstream processors see individual items and cannot drop whole batches",
		Docs:     rule.BatchDocs,
		Severity: diag.Info,
	})
}

// checkSampling reports enrichment that runs after a sampler, whose policies
// then match attributes that are not there yet.
func (r processorOrder) checkSampling(ctx *rule.Context, p config.Pipeline, i int, ref config.Ref) {
	sampler, docs, decided := samplerBefore(p.Processors, i)
	if !decided {
		return
	}

	ctx.Report(rule.Finding{
		Node: ref.Node, Path: ref.Path,
		Message: rule.Quote(ref.ID.String()) + " runs after " + rule.Quote(sampler.ID.String()) +
			" in pipeline " + rule.Quote(p.Key) +
			", so sampling policies cannot match the attributes it adds",
		Hint: "move " + rule.Quote(ref.ID.String()) + " ahead of " + rule.Quote(sampler.ID.String()) +
			" in " + processorsPath(p) + " so the decision is made against enriched telemetry",
		Docs: docs,
	})
}

func processorsPath(p config.Pipeline) string {
	return "service.pipelines." + p.Key + ".processors"
}

// samplerBefore returns the first sampling processor standing ahead of the
// processor at i, when that processor is one that enriches.
//
// A sampler decides what to keep from what it can see. k8sattributes,
// resourcedetection and resource each add attributes -- the pod and namespace,
// the cloud region and host, whatever the config writes -- and a policy
// matching on one of them behind the sampler matches nothing at all. Nothing
// errors: the policy is valid, the attribute is simply not there yet, and the
// spans it was meant to keep are dropped. tail_sampling has a second reason,
// which its README states outright: it reassembles spans into new batches, so
// anything reading the request context has to have run already.
//
// attributes is deliberately not in the group. It enriches and it redacts, and
// redaction after sampling is the sensible order -- sample first, then strip
// what is kept -- so including it would report a correct config as often as a
// wrong one.
//
// The first sampler is the one named because it is the one to move past;
// getting ahead of it clears any that follow.
func samplerBefore(refs []config.Ref, i int) (config.Ref, string, bool) {
	if !enriches(refs[i].ID.Type) {
		return config.Ref{}, "", false
	}

	for _, before := range refs[:i] {
		if docs, decides := samplerDocs(before.ID.Type); decides {
			return before, docs, true
		}
	}

	return config.Ref{}, "", false
}

// enriches reports whether the processor type adds attributes a sampling
// decision could be written against.
//
// resource is in the group for the attributes it writes rather than for where
// it reads them from: service.name, deployment.environment and the cluster are
// what a policy selecting one environment's traces matches on, and a resource
// processor behind the sampler leaves that policy matching nothing. It can
// delete as well as write, which is the argument that keeps attributes out, but
// deleting a resource-level attribute after sampling is rare enough that the
// grouping is worth the occasional false positive; attributes, whose whole
// second job is stripping fields off what was kept, is not.
func enriches(typ string) bool {
	return slices.Contains([]string{
		k8sattributesType, k8sAttributesRenamedType,
		resourcedetectionType, resourcedetectionRenamedType,
		resourceType,
	}, typ)
}

// samplerDocs reports whether the processor type decides what is kept, and
// returns the page documenting what that decision is made of -- which is also
// the page that says why the enrichment has to come first.
//
// Both answers come out of one switch on purpose: a sampler added to the group
// has to bring its own page with it rather than inheriting the one next to it,
// so a third type cannot quietly cite tail_sampling's README.
//
// dynamic_sampling is deliberately absent. It is at development stability in
// contrib, and reporting the ordering of a component whose settings are still
// moving would be guessing.
func samplerDocs(typ string) (string, bool) {
	switch typ {
	case tailSamplingType:
		return rule.TailSamplingDocs, true
	case probabilisticSamplerType:
		return rule.ProbabilisticSamplerDocs, true
	default:
		return "", false
	}
}
