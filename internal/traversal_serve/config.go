package traversal_serve

// PluginSpec is what the adapter needs to launch its plugin — the
// runtime projection of a [[traversal_plugins]] stanza (RFC 0013 §Host
// integration). The config decode that produces it (tommy codegen, the
// raw config_section extraction) arrives with the registration wiring;
// this struct stays tommy-free so the adapter carries no codegen
// dependency.
type PluginSpec struct {
	// Name identifies the plugin in diagnostics (the stanza's name).
	Name string
	// Command is the argv to spawn, resolved via $PATH when not
	// absolute (e.g. ["fj-cg", "traversal-serve"]).
	Command []string
	// Schemes is the configuration's routing claim: the URI schemes
	// dispatched to this plugin. Validated against the initialize echo
	// at first spawn (RFC 0013 §Host integration).
	Schemes []string
	// ConfigTOML is the raw TOML of the plugin's own config section
	// (RFC 0007 §Plugin-Owned Sections), passed through initialize
	// verbatim; empty when no section is configured.
	ConfigTOML string
}
