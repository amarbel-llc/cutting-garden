package fastmail

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// init registers the fastmail plugin for discovery. It registers ONLY via
// MustRegisterScheme — the discovery path for a plugin that implements
// none of capture/restore/diff (scheme_registry.go). command_components'
// root aggregation then type-asserts RootProvider on the registered
// plugins to surface fastmail's accounts as no-argument roots, and every
// other read capability (RootLister, LeafReader, the facet interfaces) is
// likewise reached by type assertion. This is the first in-tree
// scheme-only (read-only) plugin, so there is no MustRegisterCapture /
// -Restore / -Diff call here.
func init() {
	cutting_garden_plugins.MustRegisterScheme(Plugin{})
}
