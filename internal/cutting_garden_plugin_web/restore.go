package cutting_garden_plugin_web

import (
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ProtocolKind is the receipt kind this binding restores and diffs.
func (Plugin) ProtocolKind() string { return captureKind }

// RestoreProtocol materializes a web capture's payload to the destination
// path. The web payload node body IS the captured bytes (the PDF/PNG/text
// produced by chrest), so restore is a pure in-process read: walk the
// receipt to its payload node and write the body out. No browser, no
// chrest subprocess.
func (Plugin) RestoreProtocol(
	req cutting_garden_plugins.ProtocolRestoreRequest,
) error {
	payloadRef, err := receiptPayloadRef(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return err
	}

	payload, err := capture_plugin.ReadNode(req.BlobStore, payloadRef.Digest)
	if err != nil {
		return err
	}

	dest := req.RawDest
	if dest == "" {
		return errors.BadRequestf("web plugin: restore requires a destination path")
	}

	// Refuse an existing destination and create any missing parent dirs,
	// matching the git binding's restore precondition (assertDestAbsent +
	// MkdirAll) so restore never silently clobbers an existing file and
	// works when the parent directory does not yet exist.
	if _, statErr := os.Lstat(dest); statErr == nil {
		return errors.BadRequestf(
			"web plugin: destination %s already exists\n"+
				"hint: choose a destination that does not exist", dest,
		)
	} else if !os.IsNotExist(statErr) {
		return errors.Wrapf(statErr, "web plugin: stat destination %s", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return errors.Wrapf(err, "web plugin: create destination parent for %s", dest)
	}

	if err := os.WriteFile(dest, payload.Body, 0o644); err != nil {
		return errors.Wrapf(err, "web plugin: write payload to %s", dest)
	}
	return nil
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
