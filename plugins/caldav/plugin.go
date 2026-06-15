// Package cutting_garden_plugin_caldav is the CalDAV capture/restore/
// diff backend for cutting-garden. Registered for the `caldav` URI
// scheme (both hierarchical `caldav://host/path` and opaque
// `caldav:<http-url>` forms; see url.go).
//
// Each calendar resource (a VTODO or VEVENT) is captured as its raw
// text/calendar body, content-addressed into the destination blob store
// as a regular file entry — byte-identical EntryV1 shape to the
// filesystem plugin. EntryV1.Path is the resource's server-absolute
// path (e.g. `dav/user/calendars/personal/abc.ics`) so a receipt
// round-trips faithfully: restore PUTs each body back to the same path
// on the destination host, and the captured `.ics` files also
// materialize unchanged through the filesystem plugin when restored to
// a local directory.
//
// This package carries no iCalendar parser and no MCP surface — it is a
// pure capture/restore/diff plugin. It is the cutting-garden home of the
// CalDAV tool that previously lived as an MCP server in
// amarbel-llc/bob.
package caldav

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// schemeCalDAV is the single URI scheme this plugin claims.
const schemeCalDAV = "caldav"

// Plugin is the CalDAV capture/restore/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.RestorePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns the single `caldav` scheme. Unlike the yt-dlp plugin
// it claims no bare `https` hosts: a CalDAV endpoint is not
// distinguishable from any other https URL by host, so it must be opted
// into explicitly with the `caldav` scheme.
func (Plugin) Schemes() []string { return []string{schemeCalDAV} }

// TypeTag reuses capture_receipt.TypeTagV1 because CalDAV resources are
// captured as regular file entries — byte-identical EntryV1 shape to fs
// captures. A receipt mixing fs and caldav roots carries one type-tag,
// and the captured `.ics` blobs restore cleanly through either this
// plugin (to a server) or the file plugin (to a directory).
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource accepts the argument forms documented on
// baseURLFromArg. raw is preserved for diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := baseURLFromArg(u)
	return err
}

// ValidateDest accepts the same argument forms as ValidateSource:
// restoring to a CalDAV endpoint is symmetric with capturing from one.
func (Plugin) ValidateDest(dest *url.URL, raw string) error {
	_, err := baseURLFromArg(dest)
	return err
}

// ValidateDiffDir reuses the source acceptance rules: diffing against a
// CalDAV endpoint is symmetric with capturing from it.
func (Plugin) ValidateDiffDir(dir *url.URL, raw string) error {
	_, err := baseURLFromArg(dir)
	return err
}
