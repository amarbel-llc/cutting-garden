package command_components

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// ResolveRestorePlugin parses destStr as a URL and looks up the
// restore plugin registered for its scheme. Schemeless dests resolve
// to the file plugin's `""` registration.
func ResolveRestorePlugin(
	destStr string,
) (*url.URL, cutting_garden_plugins.RestorePlugin, error) {
	u, err := url.Parse(destStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse dest %q", destStr)
	}
	plugin, err := cutting_garden_plugins.ResolveRestore(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	return u, plugin, nil
}

// ReadReceiptBlob fetches and parses the receipt blob.
//
// With storeOverride non-empty: resolve that store, read directly.
// With storeOverride empty: walk GetBlobStoresSorted in deterministic
// order, probing HasBlob until a store carrying the receipt is found.
// The deterministic order ensures two stores holding receipts with
// colliding ids resolve the same way every time.
//
// Used by both `restore` (Phase 3) and `diff` (Phase 4) for the
// receipt blob itself. ResolveMaterializationStore handles the
// downstream FDR §Store-Hint Resolution decision tree.
func ReadReceiptBlob(
	envBlobStore blob_store_env.BlobStoreEnv,
	receiptID *markl.Id,
	storeOverride string,
) (capture_receipt.Blob, ids.TypeStruct, error) {
	if storeOverride != "" {
		store, err := ResolveStoreByID(envBlobStore, storeOverride)
		if err != nil {
			return nil, ids.TypeStruct{}, err
		}
		blob, tt, err := capture_receipt.Read(store, receiptID)
		if err != nil {
			return nil, tt, errors.Wrapf(err, "read receipt %s", receiptID)
		}
		return blob, tt, nil
	}

	for _, store := range envBlobStore.GetBlobStoresSorted() {
		if !store.HasBlob(receiptID) {
			continue
		}
		blob, tt, err := capture_receipt.Read(store, receiptID)
		if err != nil {
			return nil, tt, errors.Wrapf(err, "read receipt %s", receiptID)
		}
		return blob, tt, nil
	}

	return nil, ids.TypeStruct{}, errors.ErrorWithStackf(
		"receipt %s not found in any configured store", receiptID)
}

// CheckReceiptTypeTag refuses a receipt whose wire-format type-tag
// does not match the dest/dir plugin's TypeTag(). The file plugin
// accepts only `cutting_garden-capture_receipt-fs-v1`; an s3 or
// sftp plugin would accept its own segment.
//
// `operation` names the action being attempted ("restore", "diff") so
// the diagnostic reads naturally; `plugin` is widened to the parent
// Plugin interface so both RestorePlugin and DiffPlugin call sites
// satisfy it without conversion.
//
// Cross-scheme operation (e.g. fs receipt → s3 dest) is a real
// future case (mirror a captured tree without local materialization),
// but the v1 strict guard is the safe default until the policy
// lands. Decision tracked at cutting-garden#18 — when it resolves,
// this function becomes the single dispatch point for whatever
// policy is chosen (-allow-cross-scheme flag, per-plugin
// AcceptsReceiptTag, or relax-entirely). Both restore and diff
// pick up the new behavior through this helper.
func CheckReceiptTypeTag(
	receiptID *markl.Id,
	receiptTypeTag ids.TypeStruct,
	plugin cutting_garden_plugins.Plugin,
	destURL *url.URL,
	operation string,
) error {
	if receiptTypeTag.StringSansOp() == plugin.TypeTag() {
		return nil
	}
	return errors.ErrorWithStackf(
		"receipt %s: type-tag %q does not match plugin tag %q "+
			"for scheme %q; cross-scheme %s is not supported "+
			"(cutting-garden#18)",
		receiptID, receiptTypeTag.StringSansOp(),
		plugin.TypeTag(), destURL.Scheme, operation)
}
