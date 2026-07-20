package capture_plugin

import (
	"io"
	"os"
	"path/filepath"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// PayloadRefOfReceipt walks a protocol receipt to its single "payload"
// reference — the generic shape RestorePayload materializes — verifying
// the ref's type lock along the way. It is the read-only half of that
// same generic convention (cutting-garden#146 decision 3): reused by
// RestorePayload itself and by the generic protocol-diff fallback
// (internal/diff's runProtocolDiff, and the config-declared capture
// wire plugin's own DiffProtocol) to compare two receipts' payload
// digests without any kind-specific plugin code. Does not verify the
// receipt's kind — callers reach a specific receipt only through
// kind-keyed dispatch already (ResolveProtocolRestore/ResolveProtocolDiff),
// so a redundant kind check here would only duplicate that guarantee.
func PayloadRefOfReceipt(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (Ref, error) {
	receipt, err := ReadNode(store, receiptDigest)
	if err != nil {
		return Ref{}, err
	}

	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return Ref{}, errors.ErrorWithStackf(
			"capture_plugin: receipt %s has no \"payload\" reference; "+
				"generic restore/diff only supports single-payload "+
				"receipts (a kind-specific plugin is required for this "+
				"receipt's shape)",
			receiptDigest,
		)
	}
	if err := VerifyRef(payloadRef); err != nil {
		return Ref{}, err
	}

	return payloadRef, nil
}

// RestorePayload materializes a protocol receipt's single "payload" node
// body to dest, without any receipt-kind-specific plugin code. It is the
// GENERIC restore path (cutting-garden#146 decision 3; #116's "restore
// natively" principle): a receipt whose capture shape is one payload blob
// captured verbatim (a config-declared capture plugin's PDF/PNG/text, or
// any future single-artifact protocol plugin) needs no browser, no
// plugin subprocess, and no kind-specific knowledge beyond "the receipt
// has a ref aliased 'payload'" — restore is a pure
// read-payload-stream-to-disk operation.
//
// internal/restore's dispatch keys this by receipt *kind*: it is the
// fallback tried when a receipt's kind has no registered
// cutting_garden_plugins.ProtocolRestorePlugin (ResolveProtocolRestore
// misses). Receipts whose capture shape is NOT a single payload blob
// (git's per-object tree, caldav's structured collection) do not carry a
// "payload" ref and MUST instead be restored by a kind-specific plugin
// registered via MustRegisterProtocolRestore; RestorePayload returns an
// error for those rather than guessing.
//
// dest must not already exist; missing parent directories are created.
// Mirrors the precondition/streaming shape the now-retired plugins/web
// binding (cutting-garden#146) used to implement inline; a config-
// declared capture plugin (internal/capture_wire) needs no such code at
// all — it delegates here via the pkgs/capture_plugin facade — RFC 0009
// plugins may not import internal/ directly).
func RestorePayload(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
	dest string,
) (err error) {
	payloadRef, err := PayloadRefOfReceipt(store, receiptDigest)
	if err != nil {
		return err
	}

	if dest == "" {
		return errors.BadRequestf(
			"capture_plugin: restore requires a destination path",
		)
	}

	// Refuse an existing destination and create any missing parent dirs
	// so restore never silently clobbers an existing file and works when
	// the parent directory does not yet exist.
	if _, statErr := os.Lstat(dest); statErr == nil {
		return errors.BadRequestf(
			"capture_plugin: destination %s already exists\n"+
				"hint: choose a destination that does not exist", dest,
		)
	} else if !os.IsNotExist(statErr) {
		return errors.Wrapf(statErr, "capture_plugin: stat destination %s", dest)
	}
	if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
		return errors.Wrapf(mkErr, "capture_plugin: create destination parent for %s", dest)
	}

	// Stream the payload body straight to disk rather than buffering it:
	// the payload node body IS the captured artifact and can be many MB.
	_, body, err := OpenNodeBody(store, payloadRef.Digest)
	if err != nil {
		return err
	}
	defer errors.DeferredCloser(&err, body)

	f, err := os.Create(dest)
	if err != nil {
		return errors.Wrapf(err, "capture_plugin: create %s", dest)
	}
	defer errors.DeferredCloser(&err, f)

	if _, err = io.Copy(f, body); err != nil {
		return errors.Wrapf(err, "capture_plugin: write payload to %s", dest)
	}
	return nil
}
