package ynab

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// init registers the ynab plugin for discovery. It registers ONLY via
// MustRegisterScheme — the discovery path for a plugin that implements none
// of capture/restore/diff. Root aggregation then type-asserts RootProvider
// on the registered plugins to surface ynab's accounts as no-argument roots.
func init() {
	cutting_garden_plugins.MustRegisterScheme(Plugin{})
}
