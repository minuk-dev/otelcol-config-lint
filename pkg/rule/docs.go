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
	// memoryLimiterDocs states check_interval's 0s default and its recommended
	// value, spike_limit_mib's 20% default, the roughly 50MiB the process runs
	// above limit_mib, that the processor belongs first in a pipeline, and the
	// advice to set GOMEMLIMIT to 80% of the hard limit.
	memoryLimiterDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/memorylimiterprocessor/README.md"
	// batchDocs states what batching is for and where it belongs.
	batchDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/batchprocessor/README.md"
	// tlsDocs states what the TLS settings mean, insecure among them.
	tlsDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/config/configtls/README.md"
	// exporterQueueDocs states that sending_queue.storage names the storage
	// extension a persistent queue writes through.
	exporterQueueDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/exporter/exporterhelper/README.md"
	// authDocs states that auth.authenticator names an extension, and which
	// extensions implement the interface it needs.
	authDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/config/configauth/README.md"
	// configSecurityDocs states that sensitive settings belong in a secret
	// store or on an encrypted filesystem, pulled into the config by expansion.
	configSecurityDocs = "https://opentelemetry.io/docs/security/config-best-practices/"
	// kubernetesResourceDocs states what requests and limits do, and the QoS
	// class a pod lands in when they differ.
	kubernetesResourceDocs = "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/"
)
