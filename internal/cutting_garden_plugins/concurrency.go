package cutting_garden_plugins

// ConcurrentReader is the OPTIONAL capability a plugin implements to let a
// consumer make its read calls concurrently: ListRoots, ListEnriched and
// ReadLeaf for different nodes, at most MaxConcurrentReads at a time
// (cutting-garden#313). The trellis evaluator uses it for per-node predicate
// reads and sibling listings, bounded by its own worker count as well.
//
// It is opt-in because nothing else promises a plugin's reads are safe to
// overlap: a plugin without it is called one read at a time, as before. A
// plugin implements it only once its read paths are safe under concurrent
// callers (its caches and clients locked, a shared freshness check done
// once rather than once per caller) and its source tolerates the load; one
// with a request budget (ynab's) does not implement it. A value below 2
// means sequential. Probed by type assertion like the other capabilities;
// a wire plugin (RFC 0013) does not declare it yet.
type ConcurrentReader interface {
	Plugin

	// MaxConcurrentReads is the most read calls the plugin accepts at once.
	MaxConcurrentReads() int
}
