package cutting_garden_plugins

import "errors"

var (
	// ErrAlreadyRegistered is returned by registry.Register when a
	// scheme is already registered for the given direction (capture
	// or restore).
	ErrAlreadyRegistered = errors.New("cutting-garden plugin already registered")

	// ErrUnknownScheme is returned by registry.Resolve when the
	// scheme is not registered for the given direction.
	ErrUnknownScheme = errors.New("unknown cutting-garden plugin scheme")

	// ErrBulkAtomicUnsupported is the sentinel a BulkMutator returns when
	// it cannot honor BulkAtomic for a request — because it never supports
	// atomic at all, or because THIS request's ops span something it
	// cannot transact together (RFC 0017 §Atomicity). The plugin MUST
	// reject, never downgrade to best-effort. The wire transport maps this
	// sentinel to RFC 0017's -32003 (atomic-unsupported) code; a plugin
	// returns it (or wraps it) so the reason is distinguishable from an
	// ordinary failure.
	ErrBulkAtomicUnsupported = errors.New(
		"bulk_mutate: atomic completion is not supported for this request",
	)
)
