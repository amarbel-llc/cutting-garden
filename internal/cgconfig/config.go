// Package cgconfig loads cutting-garden's user configuration (RFC 0007):
// $XDG_CONFIG_HOME/cutting-garden/config.toml. ConfigV0 aggregates each
// plugin's delegated config section; the loader decodes the file (or
// yields an empty config when absent), and the composition root injects
// each section into its plugin before any command resolves roots.
//
// cgconfig imports the plugin packages to embed their sections; nothing
// here is imported by a plugin, so the delegated-section layering stays
// acyclic (RFC 0007 § Package Layering).
package cgconfig

import (
	caldav "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_caldav"
)

// ConfigV0 is the top-level, horizontally-versioned config. Each plugin
// section is an OPTIONAL delegated field keyed by the plugin's scheme; a
// new format version adds a ConfigV1 beside this rather than mutating it
// (RFC 0007 § Top-Level Structure).
//
//go:generate tommy generate
type ConfigV0 struct {
	Caldav caldav.AccountsConfig `toml:"caldav,omitempty"`
}

// Validate runs each plugin section's validation. tommy's generated
// DecodeConfigV0 invokes it after decoding, so a malformed account aborts
// the load (surfaced as EX_USAGE by the loader).
func (c ConfigV0) Validate() error {
	return c.Caldav.Validate()
}
