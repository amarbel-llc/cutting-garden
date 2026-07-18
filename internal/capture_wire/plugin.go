// Package capture_wire is the config-declared CAPTURE-side counterpart
// to internal/traversal_serve: a `[[plugins]]` stanza whose Protocols
// include traversal_serve.ProtocolCapture registers one Plugin per
// configured scheme, relocating plugins/web's RFC 0008 v2-first/v1-
// fallback orchestration generically (cutting-garden#146 slice 2 phase
// 2) — parameterized by the stanza's Command instead of a hardcoded
// chrest argv, and by the stanza's Schemes instead of a hardcoded
// "web" scheme.
//
// Restore is NOT implemented here: cutting-garden#146 decision 3 lifts
// restore into the GENERIC single-payload restorer
// (capture_plugin.RestorePayload), which internal/restore already
// falls back to for any receipt kind with no registered
// cutting_garden_plugins.ProtocolRestorePlugin — a config-declared
// capture plugin needs no restore-specific code at all.
//
// Plugin registers ONLY via cutting_garden_plugins.RegisterScheme (the
// RFC 0005 protocol-only path) — it carries no EntryV1 CapturePlugin/
// DiffPlugin stubs, unlike plugins/web, which kept vestigial
// CaptureRoot/ScanForDiff methods only to satisfy the now-superseded
// MustRegisterCapture/MustRegisterDiff registration. This is exactly
// RFC 0005's "target end state" example: a plugin implementing only
// ProtocolCapturePlugin/ProtocolDiffPlugin.
package capture_wire

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// Spec is what New needs to build a capture-side wire plugin: the
// runtime projection of a `[[plugins]]` stanza declaring
// traversal_serve.ProtocolCapture.
type Spec struct {
	// Name identifies the plugin in diagnostics AND is the protocol
	// kind (ProtocolKind) the receipts it captures dispatch diff
	// under — matching plugins/web's convention where the "web"
	// scheme, the stanza name, and the receipt kind all coincided. A
	// config declaring this plugin SHOULD name the stanza to match the
	// receipt kind the binary actually emits (e.g. name = "web" for
	// chrest).
	Name string
	// Command is the base binary invocation (argv WITHOUT a
	// subcommand); the launcher appends "capture-serve" (attempted
	// first) or "capture-batch" (the RFC 0008 §Migration v1
	// fallback).
	Command []string
	// Schemes is the routing claim: URI args of the form
	// "<scheme>:<http(s)-url>" for any scheme in this set are
	// captured by this plugin (mirrors plugins/web's `web:` opaque-
	// form convention, generalized to whatever scheme(s) the config
	// claims).
	Schemes []string
}

// Plugin is the capture-side wire adapter.
type Plugin struct {
	spec Spec
}

var (
	_ cutting_garden_plugins.Plugin                = (*Plugin)(nil)
	_ cutting_garden_plugins.ProtocolCapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.ProtocolDiffPlugin    = (*Plugin)(nil)
	_ cutting_garden_plugins.SourceValidator       = (*Plugin)(nil)
)

// New returns the capture-side wire plugin for spec.
func New(spec Spec) *Plugin { return &Plugin{spec: spec} }

// Schemes returns the configured routing claim.
func (p *Plugin) Schemes() []string { return p.spec.Schemes }

// TypeTag reuses capture_receipt.TypeTagV1 as a registration formality
// only — a config-declared capture plugin never produces EntryV1
// receipts (the RFC 0002 protocol path always applies), matching
// plugins/web's and the git binding's precedent.
func (p *Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ProtocolKind is the receipt kind this plugin's captures dispatch
// diff under: the stanza's Name (see Spec.Name).
func (p *Plugin) ProtocolKind() string { return p.spec.Name }

// ValidateSource is structural only (no network): confirms the
// argument names one of Schemes with a non-empty http(s) target. Safe
// during arg classification.
func (p *Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := p.captureTarget(u, raw)
	return err
}

// ValidateDiffDir reuses ValidateSource: diffing a captured source
// against its live location is symmetric with capturing it.
func (p *Plugin) ValidateDiffDir(u *url.URL, raw string) error {
	return p.ValidateSource(u, raw)
}
