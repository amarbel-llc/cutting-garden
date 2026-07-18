package traversal_serve

import (
	"regexp"
	"slices"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Protocol tokens for PluginStanza.Protocols (cutting-garden#146 slice
// 2, generalizing RFC 0013 §Host integration).
const (
	// ProtocolCapture declares the plugin speaks the RFC 0008 capture
	// transport (a capture-serve session, with the RFC 0008 §Migration
	// v1 capture-batch fallback).
	ProtocolCapture = "capture"
	// ProtocolTraversal declares the plugin speaks the RFC 0013
	// traversal transport (a traversal-serve session).
	ProtocolTraversal = "traversal"
)

// PluginStanza is one `[[plugins]]` stanza of the cutting-garden config:
// the declaration that a wire plugin binary serves some schemes via a
// spawned command, and which wire protocol(s) it speaks (Protocols).
// This is the cutting-garden#146 slice 2 generalization of the
// RFC 0013-only `[[traversal_plugins]]` stanza into ONE stanza shape
// covering both RFC 0008 (capture) and RFC 0013 (traversal) — a single
// config entry per plugin binary, from which the host launches
// capture-serve and/or traversal-serve sessions as needed.
// `[[traversal_plugins]]` remains a compatibility alias decoded into
// this same type (cgconfig.ConfigV0.TraversalPlugins): an entry there
// is always treated as Protocols = [ProtocolTraversal] regardless of
// any protocols key present (EffectiveProtocols), so existing configs
// keep working unmodified.
//
// Command's interpretation depends on which table decoded the stanza:
// a [[traversal_plugins]] (legacy alias) entry's Command is the full
// argv verbatim, unchanged from RFC 0013's original convention (e.g.
// ["fj-cg", "traversal-serve"]) — the plugin-launching code appends
// nothing. A [[plugins]] (general) entry's Command is instead the BASE
// binary invocation WITHOUT a subcommand (e.g. ["chrest"]); the host
// appends the protocol-specific subcommand itself — "traversal-serve"
// for ProtocolTraversal, "capture-serve" (attempted first) or
// "capture-batch" (the RFC 0008 §Migration v1 fallback) for
// ProtocolCapture. This lets one [[plugins]] Command work for either or
// both protocols without the caller having to know which subcommand
// name to embed.
//
// The aggregator (cgconfig.ConfigV0) embeds slices of these at the top
// level; tommy's generated codec for those fields delegates to this
// type's generated DecodePluginStanzaInto.
//
//go:generate tommy generate
type PluginStanza struct {
	// Name identifies the stanza in diagnostics and is the default
	// config_section. MUST be unique across stanzas.
	Name string `toml:"name"`
	// Command is the argv to spawn (resolved via $PATH when not
	// absolute). See the type doc comment for how its shape depends on
	// which table (general vs legacy alias) decoded this stanza.
	Command []string `toml:"command"`
	// Schemes is the routing claim, validated against the plugin's
	// initialize echo at first spawn. MUST NOT collide with another
	// stanza or a linked plugin.
	Schemes []string `toml:"schemes"`
	// ConfigSection names the top-level config table passed to the
	// plugin wrapper-stripped as initialize's config_toml; empty means
	// Name.
	ConfigSection string `toml:"config_section,omitempty"`
	// Protocols declares which wire protocols the plugin binary speaks:
	// ProtocolCapture and/or ProtocolTraversal. Empty defaults to
	// [ProtocolTraversal] via EffectiveProtocols — see the type doc
	// comment for the [[traversal_plugins]] compatibility-alias
	// behavior this default preserves.
	Protocols []string `toml:"protocols,omitempty"`
}

// EffectiveProtocols returns Protocols, or [ProtocolTraversal] when
// empty — the default that keeps a [[traversal_plugins]] stanza (and a
// [[plugins]] stanza that omits protocols) working as a traversal-only
// declaration.
func (s PluginStanza) EffectiveProtocols() []string {
	if len(s.Protocols) == 0 {
		return []string{ProtocolTraversal}
	}
	return s.Protocols
}

// HasProtocol reports whether s declares (or defaults to, via
// EffectiveProtocols) protocol.
func (s PluginStanza) HasProtocol(protocol string) bool {
	return slices.Contains(s.EffectiveProtocols(), protocol)
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
	for _, protocol := range s.Protocols {
		switch protocol {
		case ProtocolCapture, ProtocolTraversal:
		default:
			return errors.BadRequestf(
				"plugins %q: unknown protocol %q (want %q or %q)",
				s.Name, protocol, ProtocolCapture, ProtocolTraversal,
			)
		}
	}
	return nil
}

// ValidateStanzas enforces the cross-stanza invariants the aggregated
// config's Validate delegates here: unique names, unique schemes —
// across BOTH the general []PluginStanza slice (the `[[plugins]]`
// table) and the legacy slice (the `[[traversal_plugins]]`
// compatibility alias, cutting-garden#146 decision 2). A name or scheme
// may not be claimed twice regardless of which table declared it.
// (Scheme clashes against LINKED plugins surface at registration, which
// consults the live registry.)
func ValidateStanzas(general, legacy []PluginStanza) error {
	seenName := make(map[string]struct{}, len(general)+len(legacy))
	seenScheme := map[string]string{}

	validateOne := func(stanza PluginStanza) error {
		if err := stanza.Validate(); err != nil {
			return err
		}
		if _, dup := seenName[stanza.Name]; dup {
			return errors.BadRequestf(
				"plugins: duplicate name %q", stanza.Name,
			)
		}
		seenName[stanza.Name] = struct{}{}
		for _, scheme := range stanza.Schemes {
			if owner, dup := seenScheme[scheme]; dup {
				return errors.BadRequestf(
					"plugins: scheme %q claimed by both %q and %q",
					scheme, owner, stanza.Name,
				)
			}
			seenScheme[scheme] = stanza.Name
		}
		return nil
	}

	for _, stanza := range legacy {
		if err := validateOne(stanza); err != nil {
			return err
		}
	}
	for _, stanza := range general {
		if err := validateOne(stanza); err != nil {
			return err
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
