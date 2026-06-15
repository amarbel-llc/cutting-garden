package cgapp

// The public facade for this package lives at
// github.com/amarbel-llc/cutting-garden/pkgs/cgapp. It is the plugin
// SDK's binary builder (RFC 0009 §The public surface): an out-of-tree
// main blank-imports its own plugin package (whose init() registers via
// pkgs/cutting_garden_plugins) and calls cgapp.Build().Run(os.Args) to
// get a Utility carrying the standard subcommand set (list/mcp/serve/…).
//
// NOTE: until the RFC 0009 §5 step-3 relocation lands, Build() also
// auto-links the in-tree plugins (this package blank-imports them), so an
// external binary bundles them. That is a binary-size wart, not a
// correctness one; the relocation makes Build() bare.
//
//go:generate dagnabit export
