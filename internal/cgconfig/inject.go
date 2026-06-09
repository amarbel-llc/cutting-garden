package cgconfig

import caldav "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_caldav"

// Inject wires each plugin's config section into that plugin's package
// state (RFC 0007 § Package Layering), so a RootProvider's Roots and a
// plugin's credential resolution reflect the user's config. The
// composition step every root-consuming command runs once, before
// aggregating roots.
//
// This is the single place that maps config sections to plugins; a new
// account-bearing plugin adds one line here. It references no generated
// symbol, so it does not reintroduce the codegen-bootstrap constraint
// that keeps the loader out of this package.
func Inject(cfg *ConfigV0) {
	caldav.SetConfiguredAccounts(cfg.Caldav.Accounts)
}
