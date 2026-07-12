package cutting_garden_plugin_googlephotos

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/pkgs/plugin_blob_io"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ScanForDiff downloads the current state behind the Google Photos share
// URL into a fresh tempdir and returns one freshly-hashed EntryV1 per
// artifact, suitable for the diff command to compare against the
// receipt's entries.
//
// Unlike the yt-dlp plugin — which fronts a cheap single-file info.json
// freshness probe before falling back to a full re-download — this
// plugin always does a full re-scan. A Google Photos album has no single
// canonical metadata sidecar to hash, so a content-addressed freshness
// probe would have to enumerate the album's per-item metadata anyway;
// the lighter probe is deferred (see FDR 0017 §Diff). Diff is read-only
// and atomic, so per-entry failures aggregate into the returned error
// rather than streaming through a sink.
func (Plugin) ScanForDiff(
	req cutting_garden_plugins.DiffScanRequest,
) (entries []capture_receipt.EntryV1, err error) {
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	source, err := sourceURLFromArg(req.Dir)
	if err != nil {
		return nil, err
	}

	scanDir, err := os.MkdirTemp("", "cg-gphotos-diff-*")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer func() {
		// Scandir cleanup is best-effort, mirroring CaptureRoot's tempdir
		// handling: by now every entry is hashed, so a leftover directory
		// only costs disk space — it must not fail an otherwise
		// successful (read-only) diff scan.
		if rmErr := os.RemoveAll(scanDir); rmErr != nil {
			r.Log("google-photos plugin: scandir cleanup failed: %v", rmErr)
		}
	}()

	if err = runGalleryDL(req.Context, scanDir, captureDefaultArgs(scanDir, source)); err != nil {
		return nil, err
	}

	var perEntryFailures []string

	walkErr := filepath.WalkDir(scanDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			perEntryFailures = append(perEntryFailures, walkErr.Error())
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			perEntryFailures = append(perEntryFailures, infoErr.Error())
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(scanDir, p)
		if relErr != nil {
			perEntryFailures = append(perEntryFailures, relErr.Error())
			return nil
		}
		rel = filepath.ToSlash(rel)
		id, size, blobErr := plugin_blob_io.WriteFileBlob(req.Context, req.BlobStore, p)
		if blobErr != nil {
			perEntryFailures = append(perEntryFailures, blobErr.Error())
			return nil
		}
		entries = append(entries, capture_receipt.EntryV1{
			Path:   rel,
			Root:   source,
			Type:   capture_receipt.TypeFile,
			Mode:   info.Mode().Perm(),
			Size:   size,
			BlobId: id.String(),
		})
		return nil
	})

	if walkErr != nil {
		return nil, errors.Wrap(walkErr)
	}
	if len(perEntryFailures) > 0 {
		return nil, errors.ErrorWithStackf(
			"google-photos plugin: %d entry failures during diff scan:\n  %s",
			len(perEntryFailures),
			strings.Join(perEntryFailures, "\n  "),
		)
	}

	return entries, nil
}
