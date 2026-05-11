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
)
