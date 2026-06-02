package cutting_garden_plugins

import (
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Protocol restore/diff plugins are indexed by capture *kind* (the
// receipt's `<kind>` tag), not by URI scheme: the receipt — not the
// dest/source argument — determines how a capture is rebuilt or
// compared. Populated at init() by each protocol binding.

type protocolRestoreRegistry struct {
	mu      sync.RWMutex
	plugins map[string]ProtocolRestorePlugin
}

type protocolDiffRegistry struct {
	mu      sync.RWMutex
	plugins map[string]ProtocolDiffPlugin
}

var (
	defaultProtocolRestoreRegistry = &protocolRestoreRegistry{plugins: map[string]ProtocolRestorePlugin{}}
	defaultProtocolDiffRegistry    = &protocolDiffRegistry{plugins: map[string]ProtocolDiffPlugin{}}
)

// MustRegisterProtocolRestore installs p under p.ProtocolKind(). Panics
// on duplicate registration; intended for binding init() functions.
func MustRegisterProtocolRestore(p ProtocolRestorePlugin) {
	r := defaultProtocolRestoreRegistry
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := p.ProtocolKind()
	if _, ok := r.plugins[kind]; ok {
		panic(errors.Errorf("%w: protocol restore %q", ErrAlreadyRegistered, kind))
	}
	r.plugins[kind] = p
}

// ResolveProtocolRestore looks up the protocol restore plugin for a
// capture kind. Returns an error wrapping ErrUnknownScheme on miss.
func ResolveProtocolRestore(kind string) (ProtocolRestorePlugin, error) {
	r := defaultProtocolRestoreRegistry
	r.mu.RLock()
	p, ok := r.plugins[kind]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: protocol restore kind %q", ErrUnknownScheme, kind)
	}
	return p, nil
}

// MustRegisterProtocolDiff is the diff-direction analogue of
// MustRegisterProtocolRestore.
func MustRegisterProtocolDiff(p ProtocolDiffPlugin) {
	r := defaultProtocolDiffRegistry
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := p.ProtocolKind()
	if _, ok := r.plugins[kind]; ok {
		panic(errors.Errorf("%w: protocol diff %q", ErrAlreadyRegistered, kind))
	}
	r.plugins[kind] = p
}

// ResolveProtocolDiff is the diff-direction analogue of
// ResolveProtocolRestore.
func ResolveProtocolDiff(kind string) (ProtocolDiffPlugin, error) {
	r := defaultProtocolDiffRegistry
	r.mu.RLock()
	p, ok := r.plugins[kind]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: protocol diff kind %q", ErrUnknownScheme, kind)
	}
	return p, nil
}
