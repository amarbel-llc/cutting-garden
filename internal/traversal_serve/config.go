package traversal_serve

import (
	"regexp"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// PluginStanza is one `[[traversal_plugins]]` stanza of the
// cutting-garden config (RFC 0013 §Host integration): the declaration
// that a wire plugin serves some schemes via a spawned command. The
// aggregator (cgconfig.ConfigV0) embeds a slice of these at the top
// level; tommy's generated codec for that field delegates to this
// type's generated DecodePluginStanzaInto.
//
//go:generate tommy generate
type PluginStanza struct {
	// Name identifies the stanza in diagnostics and is the default
	// config_section. MUST be unique across stanzas.
	Name string `toml:"name"`
	// Command is the argv to spawn (resolved via $PATH when not
	// absolute).
	Command []string `toml:"command"`
	// Schemes is the routing claim, validated against the plugin's
	// initialize echo at first spawn. MUST NOT collide with another
	// stanza or a linked plugin.
	Schemes []string `toml:"schemes"`
	// ConfigSection names the top-level config table passed to the
	// plugin wrapper-stripped as initialize's config_toml; empty means
	// Name.
	ConfigSection string `toml:"config_section,omitempty"`
}

// sectionNamePattern pins config-section (and stanza-name) grammar to
// bare TOML keys, which keeps SectionTOML's header matching exact.
var sectionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Section resolves the stanza's config section name: ConfigSection, or
// Name when unset.
func (s PluginStanza) Section() string {
	if s.ConfigSection != "" {
		return s.ConfigSection
	}
	return s.Name
}

// Validate enforces one stanza's own invariants; cross-stanza rules
// live in ValidateStanzas.
func (s PluginStanza) Validate() error {
	if s.Name == "" {
		return errors.BadRequestf("traversal_plugins: empty name")
	}
	if !sectionNamePattern.MatchString(s.Name) {
		return errors.BadRequestf(
			"traversal_plugins %q: name must be a bare TOML key", s.Name,
		)
	}
	if len(s.Command) == 0 || s.Command[0] == "" {
		return errors.BadRequestf(
			"traversal_plugins %q: empty command", s.Name,
		)
	}
	if len(s.Schemes) == 0 {
		return errors.BadRequestf(
			"traversal_plugins %q: no schemes", s.Name,
		)
	}
	for _, scheme := range s.Schemes {
		if scheme == "" {
			return errors.BadRequestf(
				"traversal_plugins %q: empty scheme", s.Name,
			)
		}
	}
	if s.ConfigSection != "" &&
		!sectionNamePattern.MatchString(s.ConfigSection) {
		return errors.BadRequestf(
			"traversal_plugins %q: config_section must be a bare TOML key",
			s.Name,
		)
	}
	return nil
}

// ValidateStanzas enforces the cross-stanza invariants the aggregated
// config's Validate delegates here: unique names, unique schemes.
// (Scheme clashes against LINKED plugins surface at registration, which
// consults the live registry.)
func ValidateStanzas(stanzas []PluginStanza) error {
	seenName := make(map[string]struct{}, len(stanzas))
	seenScheme := map[string]string{}
	for _, stanza := range stanzas {
		if err := stanza.Validate(); err != nil {
			return err
		}
		if _, dup := seenName[stanza.Name]; dup {
			return errors.BadRequestf(
				"traversal_plugins: duplicate name %q", stanza.Name,
			)
		}
		seenName[stanza.Name] = struct{}{}
		for _, scheme := range stanza.Schemes {
			if owner, dup := seenScheme[scheme]; dup {
				return errors.BadRequestf(
					"traversal_plugins: scheme %q claimed by both %q and %q",
					scheme, owner, stanza.Name,
				)
			}
			seenScheme[scheme] = stanza.Name
		}
	}
	return nil
}

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
