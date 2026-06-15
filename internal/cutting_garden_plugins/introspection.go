package cutting_garden_plugins

import (
	"sort"
	"strings"
)

// RegisteredPlugins returns every plugin registered in the scheme,
// capture, restore, or diff registry, deduplicated and sorted by scheme
// set. It is the read surface the `health`, `list`, and `mcp` commands use
// to enumerate plugins and probe their capabilities; the registries are
// otherwise resolve-only.
//
// The protocol registries (protocol_registry.go) contribute no new
// plugins — a protocol plugin is also registered as a capture/diff
// plugin — so protocol capabilities are detected by type-asserting the
// returned Plugin, not by enumerating those registries. The scheme
// registry (scheme_registry.go) is unioned in so a plugin registered via
// MustRegisterScheme alone — implementing none of capture/restore/diff,
// e.g. a RootProvider-only traversal plugin (RFC 0005, RFC 0009 §3) — is
// still enumerated. Dedup by scheme set means a plugin registered both via
// a direction and via MustRegisterScheme appears exactly once.
func RegisteredPlugins() []Plugin {
	byKey := map[string]Plugin{}
	collect := func(p Plugin) { byKey[pluginKey(p)] = p }

	defaultSchemeRegistry.mu.RLock()
	for _, p := range defaultSchemeRegistry.plugins {
		collect(p)
	}
	defaultSchemeRegistry.mu.RUnlock()

	defaultCaptureRegistry.mu.RLock()
	for _, p := range defaultCaptureRegistry.plugins {
		collect(p)
	}
	defaultCaptureRegistry.mu.RUnlock()

	defaultRestoreRegistry.mu.RLock()
	for _, p := range defaultRestoreRegistry.plugins {
		collect(p)
	}
	defaultRestoreRegistry.mu.RUnlock()

	defaultDiffRegistry.mu.RLock()
	for _, p := range defaultDiffRegistry.plugins {
		collect(p)
	}
	defaultDiffRegistry.mu.RUnlock()

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	plugins := make([]Plugin, 0, len(keys))
	for _, k := range keys {
		plugins = append(plugins, byKey[k])
	}
	return plugins
}

// pluginKey is a plugin's stable identity for deduplication: its scheme
// set, sorted and joined. TypeTag cannot serve — every plugin currently
// returns the shared fs-v1 tag — but the scheme set is unique per plugin.
func pluginKey(p Plugin) string {
	schemes := append([]string(nil), p.Schemes()...)
	sort.Strings(schemes)
	return strings.Join(schemes, ",")
}
