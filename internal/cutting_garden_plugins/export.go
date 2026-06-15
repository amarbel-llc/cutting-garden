package cutting_garden_plugins

// The public facade for this package lives at
// github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins. It is
// the plugin SDK (RFC 0009): out-of-tree and relocated in-repo plugins
// import it to implement Plugin / RootLister / RootProvider and register
// via MustRegisterScheme, never importing this internal/ package directly.
//
//go:generate dagnabit export
