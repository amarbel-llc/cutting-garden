package plugin_blob_io

// The public facade for this package lives at
// code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io. Part of the
// plugin SDK surface (RFC 0009): relocated in-repo plugins and out-of-tree
// plugins consume it instead of importing internal/plugin_blob_io
// directly. Exported demand-driven, when a migrating plugin needs it
// (RFC 0009 §5).
//
//go:generate dagnabit export
