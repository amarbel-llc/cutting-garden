package cutting_garden_plugins

import (
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// schemeRegistry is the base-Plugin index keyed by URI scheme (RFC 0005).
// Unlike the capture/restore/diff registries it is direction-agnostic: a
// plugin registers here once to be *discoverable* (enumerated by
// RegisteredPlugins, resolvable by ResolveScheme) regardless of which
// capability interfaces it implements. This is the only registration path
// for a plugin that implements neither capture, restore, nor diff — e.g. a
// RootProvider-only traversal plugin (FDR 0014) such as an out-of-tree
// nix_store cache (RFC 0009).
type schemeRegistry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

func newSchemeRegistry() *schemeRegistry {
	return &schemeRegistry{plugins: map[string]Plugin{}}
}

func (r *schemeRegistry) register(scheme string, p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[scheme]; ok {
		return errors.Errorf("%w: scheme %q", ErrAlreadyRegistered, scheme)
	}
	r.plugins[scheme] = p
	return nil
}

func (r *schemeRegistry) resolve(scheme string) (Plugin, error) {
	r.mu.RLock()
	p, ok := r.plugins[scheme]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.Errorf("%w: scheme %q", ErrUnknownScheme, scheme)
	}
	return p, nil
}

var defaultSchemeRegistry = newSchemeRegistry()

// MustRegisterScheme installs p in the default scheme registry under every
// scheme p.Schemes() returns. Panics on duplicate registration; intended
// for plugin init() functions where a clash is a programming error.
//
// A plugin that already registers via MustRegisterCapture / -Restore /
// -Diff is enumerated through those registries and need not also register
// here; MustRegisterScheme is the discovery path for a plugin that
// implements none of those directions (RFC 0005, RFC 0009 §3).
func MustRegisterScheme(p Plugin) {
	for _, s := range p.Schemes() {
		if err := defaultSchemeRegistry.register(s, p); err != nil {
			panic(err)
		}
	}
}

// ResolveScheme looks up the base plugin registered under scheme via
// MustRegisterScheme. Returns an error wrapping ErrUnknownScheme on miss.
func ResolveScheme(scheme string) (Plugin, error) {
	return defaultSchemeRegistry.resolve(scheme)
}
