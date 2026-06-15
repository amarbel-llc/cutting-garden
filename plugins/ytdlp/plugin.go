// Package cutting_garden_plugin_ytdlp is the yt-dlp capture/diff
// backend for cutting-garden. Registered for the `ytdlp` scheme
// (both opaque `ytdlp:<url>` and hierarchical `ytdlp://host/path`
// forms) and for the `https` scheme under a closed host allowlist
// (YouTube, Instagram, …); see `httpsAllowlist` in url.go for the
// full set.
//
// Restore is intentionally not implemented; captured artifacts are
// regular files that the filesystem plugin can materialize. See
// `docs/features/0003-ytdlp-plugin.md` for the deferral rationale.
package cutting_garden_plugin_ytdlp

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// Plugin is the yt-dlp capture/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns the URI schemes this plugin claims. `https` is
// claimed exclusively for the host allowlist enforced in
// ValidateSource; any other plugin that wants the https scheme would
// need to coordinate via a host-routing layer.
func (Plugin) Schemes() []string { return []string{"ytdlp", "https"} }

// TypeTag reuses capture_receipt.TypeTagV1 because yt-dlp artifacts
// are captured as regular file entries — byte-identical EntryV1 shape
// to fs captures. A receipt mixing fs and ytdlp roots carries one
// type-tag and restores cleanly through the file plugin.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource accepts the three argument forms documented on
// sourceURLFromArg. raw is preserved for diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := sourceURLFromArg(u)
	return err
}

// ValidateDiffDir reuses the same URL acceptance rules as
// ValidateSource: diff against a yt-dlp URL is symmetric with
// capturing from it.
func (Plugin) ValidateDiffDir(u *url.URL, raw string) error {
	_, err := sourceURLFromArg(u)
	return err
}
