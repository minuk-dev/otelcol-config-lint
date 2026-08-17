package rule

// The component types more than one rule has something to say about. They are
// matched on the type rather than the whole id, so instances such as
// "memory_limiter/aggressive" and "batch/traces" are recognised too.
const (
	// MemoryLimiterType bounds the collector's memory use, and is the one
	// processor whose position in a pipeline is fixed.
	MemoryLimiterType = "memory_limiter"
	// BatchType groups items before they are exported.
	BatchType = "batch"
	// DebugType writes what it is given to the collector's own log.
	DebugType = "debug"
)

// The settings an exporter's queue is written in. The queue decides both
// whether what is in flight survives a restart and, since the batcher moved
// behind it, whether the exporter batches at all.
const (
	// SendingQueueKey is the exporter setting holding the queue.
	SendingQueueKey = "sending_queue"
	// StorageKey names the storage extension a persistent queue writes through.
	StorageKey = "storage"
	// BatchKey turns on the batcher that sits behind the queue.
	BatchKey = "batch"
)

// EndpointKey is the setting a component that listens says its address in.
const EndpointKey = "endpoint"
