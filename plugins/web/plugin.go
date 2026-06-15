// Package cutting_garden_plugin_web is the web-page capture/restore/diff
// backend for cutting-garden, the reference orchestrator side of the
// Capture Plugin Protocol's web-archive binding ([RFC 0003]). Unlike the
// in-process git binding, web capture runs the RFC 0002 *subprocess*
// form: the chrest capturer (resolved on PATH) drives headless
// Firefox/BiDi, assembles the receipt merkle tree itself, and writes
// every node blob through a `writer.cmd` this plugin supplies — the
// hidden `cutting-garden __write-blob` sink (internal/blob_writer), which
// re-resolves the destination store from the same environment. chrest and
// cutting-garden share the exported pkgs/capture_plugin code, so the tree
// is byte-identical to one an in-process binding would emit.
//
// Registered for one opt-in scheme:
//
//	web:<http(s)-url>      e.g. web:https://example.com/article
//
// A bare `https:` scheme is deliberately NOT claimed: the yt-dlp binding
// already claims `https` (host-allowlisted), and a web capture should be
// an explicit `web:` opt-in rather than silently swallowing every URL.
//
// The capture format is the default (pdf) unless CUTTING_GARDEN_WEB_FORMAT
// is set; the capture command has no per-source options surface yet.
//
// [RFC 0003]: docs/rfcs/0003-web-archive-binding.md
package cutting_garden_plugin_web

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// Plugin is the web capture/restore/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin         = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin            = (*Plugin)(nil)
	_ cutting_garden_plugins.ProtocolCapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.ProtocolRestorePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.ProtocolDiffPlugin    = (*Plugin)(nil)
)

// Schemes claims only the explicit `web:` opt-in prefix (see package doc).
func (Plugin) Schemes() []string { return []string{"web"} }

// TypeTag reuses capture_receipt.TypeTagV1 to satisfy the EntryV1
// Plugin contract; web captures never produce EntryV1 receipts (the RFC
// 0002 protocol path emits a self-contained cutting_garden-capture-
// receipt-web-v1 tree), so the value is only a registration formality —
// matching the git binding's reuse.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource is structural only (no network): it confirms the
// argument carries a non-empty http(s) target. Safe during arg
// classification.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := captureTarget(u, raw)
	return err
}

// ValidateDiffDir reuses ValidateSource: diffing a captured page against
// its live URL is symmetric with capturing it.
func (Plugin) ValidateDiffDir(u *url.URL, raw string) error {
	_, err := captureTarget(u, raw)
	return err
}
