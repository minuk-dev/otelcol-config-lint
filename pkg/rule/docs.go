package rule

// The upstream documentation a rule can point at. A rule that reports what the
// collector requires or recommends carries the page that says so, so a reader
// can check the claim instead of taking the linter's word for it.
//
// The links are to main rather than to the release being validated: they are
// where upstream states the recommendation, and the wording outlives any one
// version. A component that has been removed since is reported by
// unknown-component long before its README matters.
const (
	// MemoryLimiterDocs states check_interval's 0s default and its recommended
	// value, spike_limit_mib's 20% default, the roughly 50MiB the process runs
	// above limit_mib, that the processor belongs first in a pipeline, and the
	// advice to set GOMEMLIMIT to 80% of the hard limit.
	MemoryLimiterDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/memorylimiterprocessor/README.md"
	// BatchDocs states what batching is for and where it belongs.
	BatchDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/batchprocessor/README.md"
	// TailSamplingDocs states that the processor must be placed after any
	// processor that relies on request context, k8sattributes among them,
	// because it reassembles spans into new batches and the original context
	// is lost. It is also where the policies that read attributes are
	// documented.
	TailSamplingDocs = "https://github.com/open-telemetry/opentelemetry-collector-contrib" +
		"/blob/main/processor/tailsamplingprocessor/README.md"
	// ProbabilisticSamplerDocs states which attributes the sampler can decide
	// on -- from_attribute, sampling_priority, and the resource attribute a
	// hash seed is taken from.
	ProbabilisticSamplerDocs = "https://github.com/open-telemetry/opentelemetry-collector-contrib" +
		"/blob/main/processor/probabilisticsamplerprocessor/README.md"
	// DebugDocs states what each verbosity prints, that detailed writes
	// multiple lines per record, that sampling_initial and sampling_thereafter
	// bound the rate, and that the output format is not stable.
	DebugDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/exporter/debugexporter/README.md"
	// TLSDocs states what the TLS settings mean, insecure among them.
	TLSDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/config/configtls/README.md"
	// ExporterQueueDocs states that sending_queue.storage names the storage
	// extension a persistent queue writes through, and that sending_queue.batch
	// batches behind the queue, with the defaults flush_timeout takes.
	ExporterQueueDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/exporter/exporterhelper/README.md"
	// AuthDocs states that auth.authenticator names an extension, and which
	// extensions implement the interface it needs.
	AuthDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/config/configauth/README.md"
	// SecurityDocs is upstream's security guidance. Its extensions section is
	// what says to avoid exposing health or telemetry data outside the
	// collector by default, and names the extensions that do.
	SecurityDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/docs/security-best-practices.md"
	// ConfigSecurityDocs is the other half of it, for the file rather than the
	// process: sensitive settings belong in a secret store or on an encrypted
	// filesystem, pulled into the config by expansion.
	ConfigSecurityDocs = "https://opentelemetry.io/docs/security/config-best-practices/"
	// InternalTelemetryDocs is upstream's page on the collector's own
	// observability: what service.telemetry configures, that
	// service::telemetry::metrics::address is ignored as of v0.123.0 and what
	// readers replace it with, and which metrics a level turns off.
	InternalTelemetryDocs = "https://opentelemetry.io/docs/collector/internal-telemetry/"
	// KubernetesResourceDocs states what requests and limits do, and the QoS
	// class a pod lands in when they differ.
	KubernetesResourceDocs = "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/"
)
