package cutting_garden_plugin_web

import (
	"os"

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

	payload, err := readNode(req.BlobStore, payloadRef.Digest)
	if err != nil {
		return err
	}

	dest := req.RawDest
	if dest == "" {
		return errors.BadRequestf("web plugin: restore requires a destination path")
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
