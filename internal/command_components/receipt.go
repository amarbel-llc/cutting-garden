package command_components

import (
	"bufio"
	"io"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/hyphence/go/hyphence"
	"code.linenisgreat.com/madder/go/pkgs/blob_store_env"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/ids"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ResolveRestorePlugin parses destStr as a URL and looks up the
// restore plugin registered for its scheme. Schemeless dests resolve
// to the file plugin's `""` registration. Resolution prefers the typed
// restore registry (resolveRestoreCapablePlugin), falling back to the
// base scheme registry so a plugin registered ONLY via MustRegisterScheme
// whose value implements RestorePlugin is reachable too (RFC 0005
// §Resolution, extended to the EntryV1 restore direction — see the RFC's
// §Compatibility notes for why restore was originally out of scope).
func ResolveRestorePlugin(
	destStr string,
) (*url.URL, cutting_garden_plugins.RestorePlugin, error) {
	u, err := url.Parse(destStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse dest %q", destStr)
	}
	plugin, err := resolveRestoreCapablePlugin(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	return u, plugin, nil
}

// resolveRestoreCapablePlugin resolves scheme's RestorePlugin, preferring
// the typed restore registry (ResolveRestore) — preserving today's exact
// resolution and error semantics for every already-registered plugin —
// and falling back to the base scheme registry when the typed lookup
// misses. Sibling to internal/capture's resolveCapturePlugin and to
// resolveDiffCapablePlugin below.
func resolveRestoreCapablePlugin(
	scheme string,
) (cutting_garden_plugins.RestorePlugin, error) {
	if plugin, err := cutting_garden_plugins.ResolveRestore(scheme); err == nil {
		return plugin, nil
	}
	plugin, err := cutting_garden_plugins.ResolveScheme(scheme)
	if err != nil {
		return nil, err
	}
	rp, ok := plugin.(cutting_garden_plugins.RestorePlugin)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"scheme %q does not support restore (its plugin exposes no "+
				"EntryV1 restore capability)", scheme,
		)
	}
	return rp, nil
}

// ResolveDiffPlugin parses dirStr as a URL and looks up the diff
// plugin registered for its scheme. Schemeless dirs resolve to the
// file plugin's `""` registration. Sibling to ResolveRestorePlugin;
// same typed-registry-first, scheme-registry-fallback resolution.
func ResolveDiffPlugin(
	dirStr string,
) (*url.URL, cutting_garden_plugins.DiffPlugin, error) {
	u, err := url.Parse(dirStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse dir %q", dirStr)
	}
	plugin, err := resolveDiffCapablePlugin(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	return u, plugin, nil
}

// resolveDiffCapablePlugin is the diff-direction analogue of
// resolveRestoreCapablePlugin.
func resolveDiffCapablePlugin(
	scheme string,
) (cutting_garden_plugins.DiffPlugin, error) {
	if plugin, err := cutting_garden_plugins.ResolveDiff(scheme); err == nil {
		return plugin, nil
	}
	plugin, err := cutting_garden_plugins.ResolveScheme(scheme)
	if err != nil {
		return nil, err
	}
	dp, ok := plugin.(cutting_garden_plugins.DiffPlugin)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"scheme %q does not support diff (its plugin exposes no "+
				"EntryV1 diff capability)", scheme,
		)
	}
	return dp, nil
}

// resolvePluginForScheme resolves the plugin registered for scheme,
// preferring the capture registry and falling back to the base scheme
// registry. The fallback is what makes a traversal-only plugin registered
// solely via MustRegisterScheme (no capture/restore/diff — e.g. an
// out-of-tree RootProvider) resolvable by `list`/`mcp`, not merely
// enumerable by `health` via RegisteredPlugins (RFC 0009 §3). Capture-first
// keeps the common path (every in-repo capture plugin) unchanged.
func resolvePluginForScheme(
	scheme string,
) (cutting_garden_plugins.Plugin, error) {
	if plugin, err := cutting_garden_plugins.ResolveCapture(scheme); err == nil {
		return plugin, nil
	}
	return cutting_garden_plugins.ResolveScheme(scheme)
}

// ResolveRootListerPlugin parses uriStr as a URL and returns the
// RootLister registered for its scheme — the read-only traversal
// capability the `list` and `mcp` commands consume. Resolution prefers the
// capture registry, then falls back to the base scheme registry
// (resolvePluginForScheme), so a traversal-only plugin is drivable, not just
// health-visible (RFC 0009 §3). Errors if the scheme is unknown or its
// plugin does not implement RootLister (e.g. the file plugin, which has no
// sub-structure to enumerate). Sibling to ResolveDiffPlugin.
func ResolveRootListerPlugin(
	uriStr string,
) (*url.URL, cutting_garden_plugins.RootLister, error) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse uri %q", uriStr)
	}
	plugin, err := resolvePluginForScheme(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	lister, ok := plugin.(cutting_garden_plugins.RootLister)
	if !ok {
		return nil, nil, errors.ErrorWithStackf(
			"scheme %q does not support listing (its plugin exposes no "+
				"traversal)", u.Scheme,
		)
	}
	return u, lister, nil
}

// ResolveNodeMutatorPlugin parses uriStr as a URL and returns the
// NodeMutator registered for its scheme — the write capability the `mcp`
// server's CUD tools consume (FDR 0020). Like ResolveRootListerPlugin, it
// resolves capture-first then falls back to the base scheme registry
// (resolvePluginForScheme), so a MustRegisterScheme-only plugin's mutation
// surface is reachable too. Errors if the scheme is unknown or its plugin
// does not implement NodeMutator (e.g. the file plugin, which has no
// live-mutation surface).
func ResolveNodeMutatorPlugin(
	uriStr string,
) (*url.URL, cutting_garden_plugins.NodeMutator, error) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse uri %q", uriStr)
	}
	plugin, err := resolvePluginForScheme(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	mutator, ok := plugin.(cutting_garden_plugins.NodeMutator)
	if !ok {
		return nil, nil, errors.ErrorWithStackf(
			"scheme %q does not support mutation (its plugin exposes no "+
				"write capability)", u.Scheme,
		)
	}
	return u, mutator, nil
}

// ResolveContainerCreatorPlugin parses uriStr as a URL and returns the
// ContainerCreator registered for its scheme — the server-assigned-identity
// creation capability (cutting-garden#143) the mcp create_node tool
// dispatches to for types declared ServerAssignedIdentity. Resolution
// mirrors ResolveNodeMutatorPlugin.
func ResolveContainerCreatorPlugin(
	uriStr string,
) (*url.URL, cutting_garden_plugins.ContainerCreator, error) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse uri %q", uriStr)
	}
	plugin, err := resolvePluginForScheme(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	creator, ok := plugin.(cutting_garden_plugins.ContainerCreator)
	if !ok {
		return nil, nil, errors.ErrorWithStackf(
			"scheme %q does not support container-create (its plugin "+
				"assigns no server-side identities)", u.Scheme,
		)
	}
	return u, creator, nil
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
		"receipt %s not found in any configured store", receiptID,
	)
}

// LocateReceiptStore finds the configured store that holds the receipt
// blob. With storeOverride non-empty it resolves that store directly;
// otherwise it walks GetBlobStoresSorted probing HasBlob (the same
// deterministic order ReadReceiptBlob uses). Returns an error if the
// receipt is not found.
//
// Sibling to ReadReceiptBlob, but format-agnostic: it returns the store
// rather than parsing the blob, so callers can peek the receipt's
// type-tag (PeekReceiptType) and route fs-v1 vs RFC 0002 protocol
// receipts before committing to a parser.
func LocateReceiptStore(
	envBlobStore blob_store_env.BlobStoreEnv,
	receiptID *markl.Id,
	storeOverride string,
) (blob_stores.BlobStoreInitialized, error) {
	if storeOverride != "" {
		return ResolveStoreByID(envBlobStore, storeOverride)
	}
	for _, store := range envBlobStore.GetBlobStoresSorted() {
		if store.HasBlob(receiptID) {
			return store, nil
		}
	}
	return blob_stores.BlobStoreInitialized{}, errors.ErrorWithStackf(
		"receipt %s not found in any configured store", receiptID,
	)
}

// PeekReceiptType reads only the `! type` line from a receipt blob's
// hyphence metadata section, without parsing the body. Both the fs-v1
// receipt and the RFC 0002 protocol receipts are hyphence documents, so
// this works for either; the returned type-string discriminates them
// (capture_plugin.KindFromReceiptType matches only protocol receipts).
func PeekReceiptType(
	store blob_stores.BlobStoreInitialized,
	receiptID *markl.Id,
) (typeString string, err error) {
	reader, err := store.MakeBlobReader(receiptID)
	if err != nil {
		return "", errors.Wrapf(err, "open receipt %s", receiptID)
	}
	defer errors.DeferredCloser(&err, reader)

	br := bufio.NewReader(reader)
	// Skip the opening boundary line.
	if _, err = br.ReadString('\n'); err != nil && err != io.EOF {
		return "", errors.Wrap(err)
	}
	for {
		line, rerr := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\n")
		if strings.HasPrefix(trimmed, "! ") {
			return strings.TrimPrefix(trimmed, "! "), nil
		}
		if trimmed == hyphence.Boundary {
			break
		}
		if rerr != nil {
			break
		}
	}
	return "", errors.ErrorWithStackf(
		"receipt %s: no `! type` line in metadata", receiptID,
	)
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
		plugin.TypeTag(), destURL.Scheme, operation,
	)
}
