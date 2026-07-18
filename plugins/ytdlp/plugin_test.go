package cutting_garden_plugin_ytdlp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_failures"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
)

// fakeYtdlpScript is the shim binary written onto PATH for every test
// that exercises the capture/diff round-trip. It mirrors yt-dlp's
// output template for `--write-info-json`/`--write-thumbnail`/
// `--write-subs` modes, plus the `--skip-download` short-circuit used
// by the diff freshness probe. The artifact bytes are deterministic so
// blob-ids in the test are stable across runs.
const fakeYtdlpScript = `#!/bin/sh
set -e

template=""
skip_download=0
flat=0
for arg in "$@"; do
  if [ "$pending" = "out" ]; then
    template="$arg"
    pending=""
    continue
  fi
  case "$arg" in
    -o) pending=out ;;
    --skip-download) skip_download=1 ;;
    --flat-playlist) flat=1 ;;
  esac
done

if [ "$flat" = "1" ]; then
  # CaptureRoot's classification probe (flatplaylist.go): this fake only
  # ever models a single video, so it always emits exactly one
  # --dump-json record — the FDR 0004 "one id -> single-video path"
  # signal every capture/diff test in this file relies on. No -o is
  # given for --flat-playlist calls, so this branch must come before the
  # "missing -o template" check below.
  printf '{"id":"video","title":"test","url":"https://youtu.be/dQw4w9WgXcQ","uploader":"Test Uploader","upload_date":"20260101","duration":42}\n'
  exit 0
fi

if [ -z "$template" ]; then
  echo "fake-yt-dlp: missing -o template" >&2
  exit 64
fi

# %(id)s -> "video", %(ext)s -> per-artifact ext. The fake video id is
# fixed so the test asserts against stable filenames.
prefix="$(echo "$template" | sed 's/\.%(ext)s$//' | sed 's/%(id)s/video/')"

mkdir -p "$(dirname "$prefix")"

echo '{"id":"video","title":"test","duration":1}' > "$prefix.info.json"
if [ "$skip_download" = "0" ]; then
  printf 'media-bytes' > "$prefix.mp4"
  printf 'thumb-bytes' > "$prefix.jpg"
  printf 'WEBVTT\n\n' > "$prefix.en.vtt"
fi
`

// withFakeYtdlp installs the fake shim as `yt-dlp` on PATH for the
// duration of t and returns. Restores PATH on cleanup so subsequent
// subtests see the original environment.
func withFakeYtdlp(t *testing.T) {
	t.Helper()
	installFakeYtdlp(t, fakeYtdlpScript)
}

// installFakeYtdlp writes script as the yt-dlp binary on PATH for the
// duration of t. Different shims back the failure-path tests.
func installFakeYtdlp(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

func newDiscardStore() blob_stores.BlobStoreInitialized {
	return blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)
}

func TestPlugin_CaptureRoot_WritesAllArtifacts(t *testing.T) {
	withFakeYtdlp(t)

	source := mustParseURL(t, "ytdlp:https://youtu.be/dQw4w9WgXcQ")
	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "ytdlp:https://youtu.be/dQw4w9WgXcQ",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, rep.failures)
	}
	if len(result.Entries) != 4 {
		t.Fatalf("expected 4 entries (mp4/info/jpg/vtt); got %d: %v",
			len(result.Entries), entryPaths(result.Entries))
	}

	wantExts := map[string]bool{".mp4": false, ".info.json": false, ".jpg": false, ".vtt": false}
	for _, e := range result.Entries {
		if e.Type != capture_receipt.TypeFile {
			t.Errorf("entry %q: Type = %q, want %q", e.Path, e.Type, capture_receipt.TypeFile)
		}
		if e.Root != "https://youtu.be/dQw4w9WgXcQ" {
			t.Errorf("entry %q: Root = %q, want canonical https URL", e.Path, e.Root)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: BlobId is empty", e.Path)
		}
		for ext := range wantExts {
			if strings.HasSuffix(e.Path, ext) {
				wantExts[ext] = true
			}
		}
	}
	for ext, seen := range wantExts {
		if !seen {
			t.Errorf("no entry with extension %q", ext)
		}
	}
	if len(rep.entries) != len(result.Entries) {
		t.Errorf("stream saw %d entries, result has %d", len(rep.entries), len(result.Entries))
	}
}

func TestPlugin_CaptureRoot_RejectsOffAllowlistHTTPS(t *testing.T) {
	withFakeYtdlp(t)

	source := mustParseURL(t, "https://vimeo.com/123")
	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "https://vimeo.com/123",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(rep.failures) != 1 {
		t.Fatalf("stream failures = %d, want 1", len(rep.failures))
	}
	if !strings.Contains(rep.failures[0].err.Error(), "bare-https allowlist") {
		t.Errorf("error %q missing 'bare-https allowlist'", rep.failures[0].err.Error())
	}
}

func TestPlugin_ScanForDiff_InfoJSONMatchEmitsReceiptEntries(t *testing.T) {
	withFakeYtdlp(t)

	// First capture to get the canonical info.json BlobId.
	source := mustParseURL(t, "ytdlp:https://youtu.be/dQw4w9WgXcQ")
	// Reporter deliberately omitted: a nil stream must be tolerated
	// (ReporterOrNop inside CaptureRoot).
	captureResult := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "ytdlp:https://youtu.be/dQw4w9WgXcQ",
		BlobStore: newDiscardStore(),
	})
	if captureResult.FailCount != 0 {
		t.Fatalf("capture FailCount = %d", captureResult.FailCount)
	}

	// Diff against the same URL — info.json content is identical, so
	// the probe should re-emit the receipt entries verbatim.
	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            source,
		RawDir:         "ytdlp:https://youtu.be/dQw4w9WgXcQ",
		BlobStore:      newDiscardStore(),
		ReceiptEntries: captureResult.Entries,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	if !sameEntrySet(diffEntries, captureResult.Entries) {
		t.Errorf("diff entries differ from receipt:\n  diff:    %v\n  receipt: %v",
			entryPaths(diffEntries), entryPaths(captureResult.Entries))
	}
}

func TestPlugin_ScanForDiff_InfoJSONMissTriggersRescan(t *testing.T) {
	withFakeYtdlp(t)

	source := mustParseURL(t, "ytdlp:https://youtu.be/dQw4w9WgXcQ")

	// Synthesize a "previous" receipt whose info.json blob-id doesn't
	// match what the fake will produce. Easiest way: use a bogus
	// BlobId on the info.json entry.
	staleEntries := []capture_receipt.EntryV1{
		{
			Path: "video.info.json", Root: "https://youtu.be/dQw4w9WgXcQ",
			Type: capture_receipt.TypeFile, BlobId: "sha256-stale",
		},
		{
			Path: "video.mp4", Root: "https://youtu.be/dQw4w9WgXcQ",
			Type: capture_receipt.TypeFile, BlobId: "sha256-stale-media",
		},
	}

	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            source,
		RawDir:         "ytdlp:...",
		BlobStore:      newDiscardStore(),
		ReceiptEntries: staleEntries,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	// The rescan walks all four artifacts produced by the full
	// invocation; their BlobIds are fresh and won't equal the stale
	// receipt's. Just assert we got the expected count + extensions.
	if len(diffEntries) != 4 {
		t.Fatalf("rescan returned %d entries, want 4: %v", len(diffEntries), entryPaths(diffEntries))
	}
}

func TestEntriesForRoot(t *testing.T) {
	source := "https://youtu.be/dQw4w9WgXcQ"
	cases := []struct {
		name      string
		entries   []capture_receipt.EntryV1
		want      int
		wantPaths []string
	}{
		{
			name: "multi-root match",
			entries: []capture_receipt.EntryV1{
				{Path: "a", Root: source},
				{Path: "b", Root: source},
				{Path: "c", Root: "https://other"},
			},
			want:      2,
			wantPaths: []string{"a", "b"},
		},
		{
			name: "single-root collapse (all dotted)",
			entries: []capture_receipt.EntryV1{
				{Path: "a", Root: "."},
				{Path: "b", Root: "."},
			},
			want:      2,
			wantPaths: []string{"a", "b"},
		},
		{
			name: "ambiguous mixed (no match, not all dotted)",
			entries: []capture_receipt.EntryV1{
				{Path: "a", Root: "."},
				{Path: "b", Root: "https://other"},
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := entriesForRoot(tc.entries, source)
			if len(got) != tc.want {
				t.Fatalf("entriesForRoot: got %d, want %d (%v)",
					len(got), tc.want, entryPaths(got))
			}
			for i, p := range tc.wantPaths {
				if got[i].Path != p {
					t.Errorf("entry[%d].Path = %q, want %q", i, got[i].Path, p)
				}
			}
		})
	}
}

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	gotSchemes := p.Schemes()
	if len(gotSchemes) != 2 || gotSchemes[0] != "ytdlp" || gotSchemes[1] != "https" {
		t.Errorf("Schemes() = %v, want [ytdlp https]", gotSchemes)
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

func sameEntrySet(a, b []capture_receipt.EntryV1) bool {
	if len(a) != len(b) {
		return false
	}
	have := map[string]string{}
	for _, e := range b {
		have[e.Path] = e.BlobId
	}
	for _, e := range a {
		if have[e.Path] != e.BlobId {
			return false
		}
	}
	return true
}

func entryPaths(es []capture_receipt.EntryV1) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

// failingYtdlpScript writes a recognizable line to stderr and exits
// non-zero so tests can assert the stderr-tail propagation path.
const failingYtdlpScript = `#!/bin/sh
echo "fake-yt-dlp: simulated geo-block" >&2
exit 1
`

// silentYtdlpScript exits 0 without writing anything, simulating the
// "yt-dlp refused the URL silently" case the diff probe defends
// against (no info.json produced).
const silentYtdlpScript = `#!/bin/sh
exit 0
`

func TestPlugin_CaptureRoot_NonZeroExit_SurfacesStderrTail(t *testing.T) {
	installFakeYtdlp(t, failingYtdlpScript)

	source := mustParseURL(t, "ytdlp:https://youtu.be/abc")
	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    "ytdlp:https://youtu.be/abc",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(rep.failures) != 1 {
		t.Fatalf("stream failures = %d, want 1", len(rep.failures))
	}
	got := rep.failures[0].err.Error()
	if !strings.Contains(got, "stderr-tail:") {
		t.Errorf("error %q missing 'stderr-tail:' marker", got)
	}
	if !strings.Contains(got, "simulated geo-block") {
		t.Errorf("error %q missing stderr line from fake shim", got)
	}

	// Task-2 contract: the tool failure also lands in result.Failures
	// as a root-level plugin failure for the failure receipt.
	if result.FailCount != len(result.Failures) {
		t.Errorf("FailCount = %d, want len(Failures) = %d",
			result.FailCount, len(result.Failures))
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %+v, want exactly one", result.Failures)
	}
	f := result.Failures[0]
	if f.Op != capture_failures.OpPlugin {
		t.Errorf("Failures[0].Op = %q, want %q", f.Op, capture_failures.OpPlugin)
	}
	if f.Root == "" || f.Path == "" {
		t.Errorf("Failures[0] root/path empty: %+v (root-level failures carry the source)", f)
	}
	if !strings.Contains(f.Error, "simulated geo-block") {
		t.Errorf("Failures[0].Error = %q, want the yt-dlp stderr detail", f.Error)
	}
}

func TestPlugin_ScanForDiff_ProbeMissingInfoJSON_ReportsError(t *testing.T) {
	installFakeYtdlp(t, silentYtdlpScript)

	source := mustParseURL(t, "ytdlp:https://youtu.be/abc")
	receipt := []capture_receipt.EntryV1{
		{
			Path: "abc.info.json", Root: "https://youtu.be/abc",
			Type: capture_receipt.TypeFile, BlobId: "sha256-stale",
		},
	}

	_, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            source,
		RawDir:         "ytdlp:https://youtu.be/abc",
		BlobStore:      newDiscardStore(),
		ReceiptEntries: receipt,
	})
	if err == nil {
		t.Fatalf("ScanForDiff returned nil error; expected missing info.json")
	}
	if !strings.Contains(err.Error(), "probe info.json missing") {
		t.Errorf("error %q missing 'probe info.json missing' marker", err.Error())
	}
}
