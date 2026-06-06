package cutting_garden_plugin_googlephotos

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// fakeGalleryDLScript is the shim binary written onto PATH for every
// test that exercises the capture/diff round-trip. It honors the
// `--directory <dir>` flag the plugin always passes and writes two
// deterministic media files plus their `--write-metadata` JSON sidecars,
// mirroring a small Google Photos album. The artifact bytes are stable
// so blob-ids in the test are reproducible across runs.
const fakeGalleryDLScript = `#!/bin/sh
set -e

dir=""
pending=""
for arg in "$@"; do
  if [ "$pending" = "dir" ]; then
    dir="$arg"
    pending=""
    continue
  fi
  case "$arg" in
    --directory|-D) pending=dir ;;
  esac
done

if [ -z "$dir" ]; then
  echo "fake-gallery-dl: missing --directory" >&2
  exit 64
fi

mkdir -p "$dir"
printf 'photo-one-bytes' > "$dir/IMG_0001.jpg"
printf 'photo-two-bytes' > "$dir/IMG_0002.jpg"
printf '{"id":"IMG_0001"}' > "$dir/IMG_0001.jpg.json"
printf '{"id":"IMG_0002"}' > "$dir/IMG_0002.jpg.json"
`

// failingGalleryDLScript writes a recognizable line to stderr and exits
// non-zero so tests can assert the stderr-tail propagation path.
const failingGalleryDLScript = `#!/bin/sh
echo "fake-gallery-dl: simulated private album" >&2
exit 1
`

// withFakeGalleryDL installs the working shim as `gallery-dl` on PATH for
// the duration of t.
func withFakeGalleryDL(t *testing.T) {
	t.Helper()
	installFakeGalleryDL(t, fakeGalleryDLScript)
}

// installFakeGalleryDL writes script as the gallery-dl binary on PATH for
// the duration of t. Different shims back the failure-path tests.
func installFakeGalleryDL(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "gallery-dl")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gallery-dl: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

// recordingSink captures sink events for assertions without touching
// stdout/stderr.
type recordingSink struct {
	entries  []capture_receipt.EntryV1
	failures []sinkFailure
}

type sinkFailure struct {
	source string
	err    error
}

func (s *recordingSink) SetStore(string)                 {}
func (s *recordingSink) Entry(e capture_receipt.EntryV1) { s.entries = append(s.entries, e) }
func (s *recordingSink) StoreGroupReceipt(string, int)   {}
func (s *recordingSink) Notice(string, ...any)           {}
func (s *recordingSink) Failure(source string, err error) {
	s.failures = append(s.failures, sinkFailure{source: source, err: err})
}
func (s *recordingSink) Finalize() {}

func newDiscardStore() blob_stores.BlobStoreInitialized {
	return blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)
}

func TestPlugin_CaptureRoot_WritesAllArtifacts(t *testing.T) {
	withFakeGalleryDL(t)

	source := mustParseURL(t, "gphotos:https://photos.app.goo.gl/AbCdEf123")
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "gphotos:https://photos.app.goo.gl/AbCdEf123",
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, sink.failures)
	}
	if len(result.Entries) != 4 {
		t.Fatalf("expected 4 entries (2 jpg + 2 json); got %d: %v",
			len(result.Entries), entryPaths(result.Entries))
	}

	wantPaths := map[string]bool{
		"IMG_0001.jpg":      false,
		"IMG_0002.jpg":      false,
		"IMG_0001.jpg.json": false,
		"IMG_0002.jpg.json": false,
	}
	for _, e := range result.Entries {
		if e.Type != capture_receipt.TypeFile {
			t.Errorf("entry %q: Type = %q, want %q", e.Path, e.Type, capture_receipt.TypeFile)
		}
		if e.Root != "https://photos.app.goo.gl/AbCdEf123" {
			t.Errorf("entry %q: Root = %q, want canonical https URL", e.Path, e.Root)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: BlobId is empty", e.Path)
		}
		if _, ok := wantPaths[e.Path]; ok {
			wantPaths[e.Path] = true
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Errorf("no entry with path %q", p)
		}
	}
	if len(sink.entries) != len(result.Entries) {
		t.Errorf("sink saw %d entries, result has %d", len(sink.entries), len(result.Entries))
	}
}

func TestPlugin_CaptureRoot_RejectsOffAllowlistHost(t *testing.T) {
	withFakeGalleryDL(t)

	source := mustParseURL(t, "gphotos:https://example.com/album")
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "gphotos:https://example.com/album",
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(sink.failures) != 1 {
		t.Fatalf("sink failures = %d, want 1", len(sink.failures))
	}
	if !strings.Contains(sink.failures[0].err.Error(), "not a Google Photos host") {
		t.Errorf("error %q missing 'not a Google Photos host'", sink.failures[0].err.Error())
	}
}

func TestPlugin_CaptureRoot_NonZeroExit_SurfacesStderrTail(t *testing.T) {
	installFakeGalleryDL(t, failingGalleryDLScript)

	source := mustParseURL(t, "gphotos:https://photos.app.goo.gl/AbCdEf123")
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "gphotos:https://photos.app.goo.gl/AbCdEf123",
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(sink.failures) != 1 {
		t.Fatalf("sink failures = %d, want 1", len(sink.failures))
	}
	got := sink.failures[0].err.Error()
	if !strings.Contains(got, "stderr-tail:") {
		t.Errorf("error %q missing 'stderr-tail:' marker", got)
	}
	if !strings.Contains(got, "simulated private album") {
		t.Errorf("error %q missing stderr line from fake shim", got)
	}
}

func TestPlugin_ScanForDiff_ReturnsFreshEntries(t *testing.T) {
	withFakeGalleryDL(t)

	source := mustParseURL(t, "gphotos:https://photos.app.goo.gl/AbCdEf123")
	entries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:   context.Background(),
		Dir:       source,
		RawDir:    "gphotos:https://photos.app.goo.gl/AbCdEf123",
		BlobStore: newDiscardStore(),
		// A real receipt would carry prior entries; the diff command
		// compares them against what ScanForDiff returns. The full
		// re-scan ignores them and reports current state.
		ReceiptEntries: nil,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("ScanForDiff returned %d entries, want 4: %v", len(entries), entryPaths(entries))
	}
	for _, e := range entries {
		if e.Root != "https://photos.app.goo.gl/AbCdEf123" {
			t.Errorf("entry %q: Root = %q, want canonical https URL", e.Path, e.Root)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: BlobId is empty", e.Path)
		}
	}
}

func TestPlugin_ScanForDiff_NonZeroExit_Errors(t *testing.T) {
	installFakeGalleryDL(t, failingGalleryDLScript)

	source := mustParseURL(t, "gphotos:https://photos.app.goo.gl/AbCdEf123")
	_, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:   context.Background(),
		Dir:       source,
		RawDir:    "gphotos:https://photos.app.goo.gl/AbCdEf123",
		BlobStore: newDiscardStore(),
	})
	if err == nil {
		t.Fatal("ScanForDiff returned nil error; expected gallery-dl failure")
	}
	if !strings.Contains(err.Error(), "gallery-dl failed") {
		t.Errorf("error %q missing 'gallery-dl failed' marker", err.Error())
	}
}

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	gotSchemes := p.Schemes()
	if len(gotSchemes) != 1 || gotSchemes[0] != "gphotos" {
		t.Errorf("Schemes() = %v, want [gphotos]", gotSchemes)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestTailWriter_KeepsOnlyTail(t *testing.T) {
	var buf bytes.Buffer
	w := newTailWriter(&buf, 8)
	for _, chunk := range [][]byte{
		[]byte("aaaa"),
		[]byte("bbbb"),
		[]byte("cccc"),
	} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got := buf.String(); got != "bbbbcccc" {
		t.Errorf("tail = %q, want %q", got, "bbbbcccc")
	}
}

func entryPaths(es []capture_receipt.EntryV1) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}
