package cutting_garden_plugin_git

import (
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ScanForDiff is a vestigial EntryV1 entry point. Git diff always routes
// through the RFC 0002 protocol path (DiffProtocol, diff_protocol.go),
// keyed by receipt kind; this method exists only to satisfy the EntryV1
// DiffPlugin registration (which keeps the `git` scheme resolvable) and is
// never reached for a git receipt. Its removal depends on the same
// protocol-only plugin-resolution follow-up as CaptureRoot.
func (Plugin) ScanForDiff(
	cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	return nil, errors.ErrorWithStackf(
		"git plugin: diff uses the RFC 0002 protocol path, not the EntryV1 path",
	)
}
