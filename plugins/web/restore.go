package cutting_garden_plugin_web

import (
	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ProtocolKind is the receipt kind this binding restores and diffs.
func (Plugin) ProtocolKind() string { return captureKind }

// RestoreProtocol materializes a web capture's payload to the destination
// path. The web payload node body IS the captured bytes (the PDF/PNG/text
// produced by chrest), so restore is a pure in-process read: walk the
// receipt to its payload node and write the body out. No browser, no
// chrest subprocess.
//
// This DELEGATES entirely to capture_plugin.RestorePayload — the GENERIC
// protocol-receipt payload restorer (cutting-garden#146 decision 3) that
// internal/restore also falls back to for any receipt kind with no
// registered plugin. The web binding's registration stays (so nothing
// user-visible changes and web receipts keep dispatching through this
// method), but the restore logic itself is no longer web-specific: it was
// already chrest-free, and the web payload shape (one "payload" ref
// pointing at the captured bytes) is exactly what the generic restorer
// handles.
func (Plugin) RestoreProtocol(
	req cutting_garden_plugins.ProtocolRestoreRequest,
) error {
	return capture_plugin.RestorePayload(req.BlobStore, req.ReceiptDigest, req.RawDest)
}

// ScanForDiff is a vestigial EntryV1 entry point. Web diff routes through
// the RFC 0002 protocol path (DiffProtocol), keyed by receipt kind; this
// exists only to keep the `web` scheme resolvable via the EntryV1
// DiffPlugin registration and is never reached for a web receipt.
func (Plugin) ScanForDiff(
	cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	return nil, errors.ErrorWithStackf(
		"web plugin: diff uses the RFC 0002 protocol path, not the EntryV1 path",
	)
}
