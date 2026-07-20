package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// fakePackable is a stand-in blob store that satisfies
// domain_interfaces.BlobStore (via the embedded nil interface — none of
// its methods are exercised by packWrittenStores) and additionally
// implements blob_stores.PackableArchive, so the type assertion inside
// packWrittenStores succeeds.
type fakePackable struct {
	domain_interfaces.BlobStore

	packErr  error
	packs    int
	lastOpts blob_stores.PackOptions
}

func (f *fakePackable) Pack(opts blob_stores.PackOptions) error {
	f.packs++
	f.lastOpts = opts
	return f.packErr
}

// collectNotices returns a notice func matching pipeline.notice's shape
// that appends each formatted line to *out.
func collectNotices(out *[]string) func(string, ...any) {
	return func(format string, args ...any) {
		*out = append(*out, fmt.Sprintf(format, args...))
	}
}

// TestPackWrittenStores_PacksPackableStores: a store implementing
// PackableArchive is packed once, the run's context is threaded into
// PackOptions, and a confirming notice is emitted.
func TestPackWrittenStores_PacksPackableStores(t *testing.T) {
	ctx := errors.MakeContextDefault()
	fp := &fakePackable{}

	var notices []string
	packWrittenStores(
		ctx,
		collectNotices(&notices),
		[]writtenStore{
			{id: "archive", store: blob_stores.BlobStoreInitialized{BlobStore: fp}},
		},
	)

	if fp.packs != 1 {
		t.Fatalf("Pack called %d times, want 1", fp.packs)
	}
	if fp.lastOpts.Context != ctx {
		t.Errorf("PackOptions.Context = %v, want the run context", fp.lastOpts.Context)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "packed store=archive") {
		t.Errorf("notices = %v, want a single `packed store=archive` line", notices)
	}
}

// TestPackWrittenStores_SkipsNonPackableStores: a store that does not
// implement PackableArchive (here the zero value, whose embedded
// BlobStore is nil) is left untouched and reported as skipped.
func TestPackWrittenStores_SkipsNonPackableStores(t *testing.T) {
	var notices []string
	packWrittenStores(
		errors.MakeContextDefault(),
		collectNotices(&notices),
		[]writtenStore{
			{id: "local"}, // nil BlobStore → not a PackableArchive
		},
	)

	if len(notices) != 1 ||
		!strings.Contains(notices[0], "store=local does not support packfiles") {
		t.Errorf("notices = %v, want a single skip line for store=local", notices)
	}
}

// TestPackWrittenStores_PackErrorIsNonFatal: a Pack error degrades to a
// notice and does not panic or abort the remaining stores.
func TestPackWrittenStores_PackErrorIsNonFatal(t *testing.T) {
	failing := &fakePackable{packErr: fmt.Errorf("disk full")}
	ok := &fakePackable{}

	var notices []string
	packWrittenStores(
		errors.MakeContextDefault(),
		collectNotices(&notices),
		[]writtenStore{
			{id: "bad", store: blob_stores.BlobStoreInitialized{BlobStore: failing}},
			{id: "good", store: blob_stores.BlobStoreInitialized{BlobStore: ok}},
		},
	)

	if ok.packs != 1 {
		t.Errorf("second store packed %d times, want 1 (first failure must not abort)", ok.packs)
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %v, want one per store", notices)
	}
	if !strings.Contains(notices[0], "pack failed for store=bad") ||
		!strings.Contains(notices[0], "disk full") {
		t.Errorf("notices[0] = %q, want a pack-failed line carrying the error", notices[0])
	}
	if !strings.Contains(notices[1], "packed store=good") {
		t.Errorf("notices[1] = %q, want a success line for store=good", notices[1])
	}
}

// TestRun_PackSkipsNonPackableDefaultStore drives Run end-to-end with
// --pack against the isolated local default store (a hash-bucketed store,
// which is not a PackableArchive): the capture still succeeds (exit 0,
// receipt written) and the run reports that the store does not support
// packfiles, proving the flag is parsed and the post-receipt pack pass
// runs over the written store.
func TestRun_PackSkipsNonPackableDefaultStore(t *testing.T) {
	isolateXDG(t)
	initDefaultBlobStore(t)

	work := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(work, "a.txt"), []byte("hello\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	out, code := driveCapture(t, "-pack", "-format=tap", ".")

	if code != 0 {
		t.Errorf("exit code = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, "receipt store=") {
		t.Errorf("missing success receipt line in %q", out)
	}
	if !strings.Contains(out, "does not support packfiles") {
		t.Errorf("missing pack-skip notice for the non-packable default store in %q", out)
	}
}
