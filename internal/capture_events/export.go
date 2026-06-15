package capture_events

// The public facade for this package lives at
// github.com/amarbel-llc/cutting-garden/pkgs/capture_events. Part of the
// plugin SDK surface (RFC 0009): relocated in-repo plugins and out-of-tree
// plugins consume it instead of importing internal/capture_events
// directly. Exported demand-driven, when a migrating plugin needs it
// (RFC 0009 §5).
//
//go:generate dagnabit export
