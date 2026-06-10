// Package cutting_garden_plugin_googlephotos is the Google Photos
// capture/diff backend for cutting-garden. It shells out to
// `gallery-dl` to download the media (and metadata sidecars) behind a
// Google Photos share URL into a tempdir, then streams every produced
// artifact into the destination blob store as a regular file entry —
// the same exec-a-downloader shape as the yt-dlp plugin.
//
// Registered for the `gphotos` scheme in two forms:
//
//   - opaque        gphotos:<share-url>     (`gphotos:https://photos.app.goo.gl/X`
//     or the bare-host `gphotos:photos.app.goo.gl/X`)
//   - hierarchical  gphotos://<host>/<path>
//
// Unlike the yt-dlp plugin it does NOT claim the bare `https` scheme
// (which yt-dlp already owns exclusively); a Google Photos capture is
// always opt-in via the `gphotos:` prefix. The resolved host must still
// be a Google Photos host (see googlePhotosHosts in url.go).
//
// Restore is intentionally not implemented; captured artifacts are
// regular files that the filesystem plugin can materialize. See
// `docs/features/0017-google-photos-plugin.md` for the deferral
// rationale.
package cutting_garden_plugin_googlephotos

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// Plugin is the Google Photos capture/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns the single URI scheme this plugin claims. It does not
// claim a bare transport scheme (`https`): a Google Photos capture is
// always opt-in via the `gphotos:` prefix, so there is no risk of
// silently grabbing arbitrary https arguments and no collision with the
// yt-dlp plugin's exclusive https claim.
func (Plugin) Schemes() []string { return []string{"gphotos"} }

// TypeTag reuses capture_receipt.TypeTagV1 because Google Photos
// artifacts are captured as regular file entries — byte-identical
// EntryV1 shape to fs captures. A receipt mixing fs and gphotos roots
// carries one type-tag and restores cleanly through the file plugin.
// Same rationale as the yt-dlp and git plugins.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource accepts the argument forms documented on
// sourceURLFromArg. It is structural only — no network — so it is safe
// to call during arg classification. raw is preserved for diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := sourceURLFromArg(u)
	return err
}

// ValidateDiffDir reuses the same URL acceptance rules as
// ValidateSource: diffing against a Google Photos URL is symmetric with
// capturing from it.
func (Plugin) ValidateDiffDir(u *url.URL, raw string) error {
	_, err := sourceURLFromArg(u)
	return err
}
