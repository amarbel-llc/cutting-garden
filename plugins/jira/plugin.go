// Package jira is the Jira Cloud capture/diff backend for cutting-garden.
// Registered for the `jira` URI scheme (both hierarchical
// `jira://host/PROJECT` and opaque `jira:<http(s)-url>` forms; see url.go).
//
// Each Jira issue is captured as its canonical JSON body (the
// `GET /rest/api/3/issue/KEY?fields=*all` resource, key-sorted so the
// bytes are stable across fetches), content-addressed into the destination
// blob store as a regular file entry — byte-identical EntryV1 shape to the
// filesystem plugin. EntryV1.Path is the issue's project-relative path
// (e.g. `PROJ/PROJ-42.json`) so the captured JSON files also materialize
// unchanged through the filesystem plugin when restored to a local
// directory.
//
// This package carries no Jira write surface — it is a pure capture/diff
// plugin. Restore is intentionally NOT implemented: re-creating or
// updating issues on a live tracker is a lossy, destructive mutation
// (read-only rendered fields, ADF bodies, issue-creation semantics), and a
// captured snapshot is for archival/backup, not round-trip mutation. The
// captured JSON files restore to disk through the filesystem plugin. See
// `docs/features/0019-jira-plugin.md` §Restore Deferral — same posture as
// the google-photos and yt-dlp plugins.
//
// The Jira REST surface this plugin speaks (search-by-JQL, issue GET,
// project enumeration, JIRA_URL/JIRA_USERNAME/JIRA_API_TOKEN auth) mirrors
// the `sisyphus` moxin in amarbel-llc/moxy, which exposes the same Jira
// Cloud API as interactive MCP tools. This is the capture-shaped analogue:
// it snapshots issue state rather than offering interactive mutation.
package jira

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// schemeJira is the single URI scheme this plugin claims.
const schemeJira = "jira"

// Plugin is the Jira Cloud capture/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns the single `jira` scheme. Like the caldav plugin it
// claims no bare transport scheme: a Jira endpoint is not distinguishable
// from any other https URL by host, so it must be opted into explicitly
// with the `jira` scheme.
func (Plugin) Schemes() []string { return []string{schemeJira} }

// TypeTag reuses capture_receipt.TypeTagV1 because Jira issues are
// captured as regular file entries — byte-identical EntryV1 shape to fs
// captures. A receipt mixing fs and jira roots carries one type-tag, and
// the captured `.json` blobs restore cleanly through the filesystem plugin
// (to a directory). Same rationale as the caldav, yt-dlp, and git plugins.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource accepts the argument forms documented on baseURLFromArg.
// It is structural only — no network — so it is safe to call during arg
// classification. raw is preserved for diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, err := baseURLFromArg(u)
	return err
}

// ValidateDiffDir reuses the source acceptance rules: diffing against a
// Jira endpoint is symmetric with capturing from it.
func (Plugin) ValidateDiffDir(dir *url.URL, raw string) error {
	_, err := baseURLFromArg(dir)
	return err
}
