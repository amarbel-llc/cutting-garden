package jira

import (
	"bytes"
	"context"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// allFields is the Jira field selector for a full-fidelity capture: every
// system and custom field on the issue. Capture and diff request the same
// set so an unchanged issue hashes identically through both paths.
var allFields = []string{"*all"}

// issueMode is the synthetic permission recorded for every captured issue.
// Remote objects have no filesystem mode; 0644 is the natural mode for the
// `.json` files they materialize into on a local restore.
const issueMode = 0o644

// storeIssue hashes one issue's canonical JSON body into store and returns
// the EntryV1 describing it plus its receipt path. Capture and diff share
// this so the EntryV1 shape (Path/Root/Type/Mode — the keys the diff
// comparator matches on) is defined in exactly one place and the two paths
// cannot drift apart. The path is returned even on error so callers can
// name the failure.
func storeIssue(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	origin string,
	iss issue,
) (entry capture_receipt.EntryV1, path string, err error) {
	path = issuePath(iss.key)

	id, size, err := plugin_blob_io.WriteReaderBlob(
		ctx, store, bytes.NewReader(iss.data),
	)
	if err != nil {
		return capture_receipt.EntryV1{}, path, err
	}

	return capture_receipt.EntryV1{
		Path:   path,
		Root:   origin,
		Type:   capture_receipt.TypeFile,
		Mode:   issueMode,
		Size:   size,
		BlobId: id.String(),
	}, path, nil
}

// issuePath is the stable, host-independent receipt key for an issue:
// `PROJECT/KEY.json` (e.g. `PROJ/PROJ-42.json`). The project segment is
// derived from the key so a cross-project capture (from a bare-host root)
// lands each issue under its own project directory.
func issuePath(key string) string {
	return projectOfKey(key) + "/" + key + ".json"
}

// projectOfKey returns the project key embedded in an issue key — the part
// before the final hyphen (Jira issue keys are `PROJECT-NUMBER`). A key
// with no hyphen is returned unchanged so a malformed key still yields a
// non-empty directory rather than an empty path segment.
func projectOfKey(key string) string {
	if i := strings.LastIndex(key, "-"); i > 0 {
		return key[:i]
	}
	return key
}
