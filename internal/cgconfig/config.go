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
	"fmt"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"code.linenisgreat.com/cutting-garden/plugins/caldav"
	"code.linenisgreat.com/cutting-garden/plugins/fastmail"
	"code.linenisgreat.com/cutting-garden/plugins/jira"
)

// ConfigV0 is the top-level, horizontally-versioned config. Each plugin
// section is an OPTIONAL delegated field keyed by the plugin's scheme; a
// new format version adds a ConfigV1 beside this rather than mutating it
// (RFC 0007 § Top-Level Structure). Plugins and TraversalPlugins are the
// two non-section fields: the top-level `[[plugins]]` / `[[traversal_
// plugins]]` stanzas declaring out-of-process wire plugins (RFC 0013
// §Host integration, generalized by cutting-garden#146 slice 2) — the
// sections THOSE name are consumed raw by SectionTOML, not decoded here.
//
//go:generate tommy generate
type ConfigV0 struct {
	Caldav   caldav.AccountsConfig   `toml:"caldav,omitempty"`
	Fastmail fastmail.AccountsConfig `toml:"fastmail,omitempty"`
	Jira     jira.AccountsConfig     `toml:"jira,omitempty"`

	// Organize configures the framework-side organize command (FDR 0023).
	Organize OrganizeConfig `toml:"organize,omitempty"`

	// Plugins is the generalized `[[plugins]]` stanza (cutting-garden#146
	// slice 2): one entry per plugin binary, declaring which wire
	// protocol(s) it speaks via PluginStanza.Protocols — the host
	// launches a capture-serve and/or traversal-serve session per
	// stanza as needed.
	Plugins []traversal_serve.PluginStanza `toml:"plugins,omitempty"`

	// TraversalPlugins is the pre-generalization `[[traversal_plugins]]`
	// compatibility alias (cutting-garden#146 decision 2): an entry here
	// decodes into the same PluginStanza shape as Plugins. Every
	// existing config omits the (newly-introduced) protocols key, so
	// PluginStanza.EffectiveProtocols defaults it to
	// [traversal_serve.ProtocolTraversal] — existing configs keep
	// working unmodified.
	TraversalPlugins []traversal_serve.PluginStanza `toml:"traversal_plugins,omitempty"`
}

// Validate runs each plugin section's validation. tommy's generated
// DecodeConfigV0 invokes it after decoding, so a malformed account aborts
// the load (surfaced as EX_USAGE by the loader).
func (c ConfigV0) Validate() error {
	if err := c.Caldav.Validate(); err != nil {
		return err
	}
	if err := c.Fastmail.Validate(); err != nil {
		return err
	}
	if err := c.Jira.Validate(); err != nil {
		return err
	}
	if err := c.Organize.Validate(); err != nil {
		return err
	}
	return traversal_serve.ValidateStanzas(c.Plugins, c.TraversalPlugins)
}

// OrganizeConfig configures the organize command (FDR 0023). Not a plugin
// section — organize is framework-side — so it lives here rather than being
// delegated.
//
//go:generate tommy generate
type OrganizeConfig struct {
	// DateGranularity is the default bucket granularity for a bare
	// `--group-by` on a date-kind facet dimension (cutting-garden#230):
	// "year", "month", or "day". Empty means the built-in default (day).
	// A `--group-by dim:granularity` suffix always wins over this.
	DateGranularity string `toml:"date_granularity,omitempty"`
}

func (c OrganizeConfig) Validate() error {
	if c.DateGranularity == "" {
		return nil
	}
	if _, ok := cgp.ParseDateGranularity(c.DateGranularity); !ok {
		return fmt.Errorf(
			"organize.date_granularity %q is not one of year, month, day",
			c.DateGranularity,
		)
	}
	return nil
}
