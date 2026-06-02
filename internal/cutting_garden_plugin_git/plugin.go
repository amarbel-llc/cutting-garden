// Package cutting_garden_plugin_git is the git capture/diff backend
// for cutting-garden. It captures one branch of a git remote as a
// self-contained git bundle plus a tiny `ref.txt` freshness sidecar,
// streaming both into the destination blob store as regular file
// entries.
//
// Registered for the `git` scheme in two forms:
//
//   - opaque        git:<remote-url>[#<branch>]
//   - hierarchical  git://<host>/<path>[#<branch>]   (native git proto)
//
// The fragment names the branch; when omitted the plugin resolves the
// remote's default branch (HEAD) at capture time. See `url.go` for the
// full acceptance rules and `docs/features/0006-git-plugin.md` for the
// design rationale.
//
// Restore is intentionally not implemented; the captured `repo.bundle`
// is a regular file the filesystem plugin materializes, and a user
// reconstitutes the branch with `git clone repo.bundle`. See FDR 0006
// §Restore Deferral.
package cutting_garden_plugin_git

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// Plugin is the git capture/diff backend.
type Plugin struct{}

var (
	_ cutting_garden_plugins.CapturePlugin = (*Plugin)(nil)
	_ cutting_garden_plugins.DiffPlugin    = (*Plugin)(nil)
)

// Schemes returns the single URI scheme this plugin claims. Unlike the
// yt-dlp plugin it does NOT claim a bare transport scheme (`https`,
// `ssh`): a git capture is always opt-in via the `git:` prefix, so no
// host allowlist is needed to keep cutting-garden from silently
// claiming every URL.
func (Plugin) Schemes() []string { return []string{"git"} }

// TypeTag reuses capture_receipt.TypeTagV1 because git artifacts are
// captured as regular file entries — byte-identical EntryV1 shape to fs
// captures. A receipt mixing fs and git roots carries one type-tag and
// restores cleanly through the file plugin. See FDR 0006 §TypeTag reuse.
func (Plugin) TypeTag() string { return capture_receipt.TypeTagV1 }

// ValidateSource accepts the argument forms documented on
// remoteAndBranchFromArg. It is structural only — no network — so it is
// safe to call during arg classification. raw is preserved for
// diagnostics.
func (Plugin) ValidateSource(u *url.URL, raw string) error {
	_, _, err := remoteAndBranchFromArg(u)
	return err
}

// ValidateDiffDir reuses the same acceptance rules as ValidateSource:
// diffing a captured branch against its remote is symmetric with
// capturing it.
func (Plugin) ValidateDiffDir(u *url.URL, raw string) error {
	_, _, err := remoteAndBranchFromArg(u)
	return err
}
