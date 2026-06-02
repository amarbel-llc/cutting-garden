package cutting_garden_plugin_git

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

// fakeGitScript is the shim binary written onto PATH for every test
// that exercises the capture/diff round-trip. It implements the three
// git subcommands the plugin drives — `ls-remote` (with and without
// `--symref`), `clone`, and `bundle create` — with deterministic
// output so blob-ids are stable across runs.
//
// The resolved tip commit is a fixed constant; the diff freshness probe
// re-derives the same value, so a capture-then-diff against the same
// shim is a no-drift round-trip.
const fakeGitScript = `#!/bin/sh
set -e
sub="$1"
shift
case "$sub" in
  ls-remote)
    sha="1111111111111111111111111111111111111111"
    if [ "$1" = "--symref" ]; then
      printf 'ref: refs/heads/main\tHEAD\n'
      printf '%s\tHEAD\n' "$sha"
    else
      ref="$2"
      printf '%s\t%s\n' "$sha" "$ref"
    fi
    ;;
  clone)
    for a in "$@"; do dir="$a"; done
    mkdir -p "$dir"
    ;;
  bundle)
    file="$2"
    mkdir -p "$(dirname "$file")"
    printf 'BUNDLE-bytes-for-test\n' > "$file"
    ;;
  *)
    echo "fake-git: unknown subcommand $sub" >&2
    exit 64
    ;;
esac
`

// failingGitScript resolves ls-remote successfully but fails the clone
// with a recognizable stderr line, so tests can assert the stderr-tail
// propagation path.
const failingGitScript = `#!/bin/sh
set -e
sub="$1"
shift
case "$sub" in
  ls-remote)
    printf '1111111111111111111111111111111111111111\t%s\n' "$2"
    ;;
  clone)
    echo "fake-git: simulated auth failure" >&2
    exit 128
    ;;
  *)
    echo "fake-git: unexpected $sub" >&2
    exit 64
    ;;
esac
`

func withFakeGit(t *testing.T) { t.Helper(); installFakeGit(t, fakeGitScript) }

func installFakeGit(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "git")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

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

func TestPlugin_CaptureRoot_WritesBundleAndRef(t *testing.T) {
	withFakeGit(t)

	arg := "git:https://github.com/amarbel-llc/cutting-garden#main"
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, sink.failures)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries (ref.txt + repo.bundle); got %d: %v",
			len(result.Entries), entryPaths(result.Entries))
	}

	wantSource := "https://github.com/amarbel-llc/cutting-garden#main"
	seen := map[string]bool{refFileName: false, bundleFileName: false}
	for _, e := range result.Entries {
		if e.Type != capture_receipt.TypeFile {
			t.Errorf("entry %q: Type = %q, want %q", e.Path, e.Type, capture_receipt.TypeFile)
		}
		if e.Root != wantSource {
			t.Errorf("entry %q: Root = %q, want %q", e.Path, e.Root, wantSource)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: BlobId is empty", e.Path)
		}
		seen[e.Path] = true
	}
	for name, ok := range seen {
		if !ok {
			t.Errorf("no entry for %q", name)
		}
	}
	if len(sink.entries) != len(result.Entries) {
		t.Errorf("sink saw %d entries, result has %d", len(sink.entries), len(result.Entries))
	}
}

func TestPlugin_CaptureRoot_DefaultBranchResolvesHEAD(t *testing.T) {
	withFakeGit(t)

	arg := "git:https://github.com/amarbel-llc/cutting-garden"
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, sink.failures)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(result.Entries))
	}
	// Default-branch capture keeps the Root identity branch-free; the
	// resolved branch lives only in ref.txt.
	wantSource := "https://github.com/amarbel-llc/cutting-garden"
	for _, e := range result.Entries {
		if e.Root != wantSource {
			t.Errorf("entry %q: Root = %q, want %q", e.Path, e.Root, wantSource)
		}
	}
}

func TestPlugin_CaptureRoot_BadArg(t *testing.T) {
	withFakeGit(t)

	arg := "git:#main"
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(sink.failures) != 1 || !strings.Contains(sink.failures[0].err.Error(), "empty remote") {
		t.Fatalf("expected 'empty remote' failure, got %v", sink.failures)
	}
}

func TestPlugin_CaptureRoot_CloneFailure_SurfacesStderrTail(t *testing.T) {
	installFakeGit(t, failingGitScript)

	arg := "git:https://github.com/amarbel-llc/cutting-garden#main"
	sink := &recordingSink{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      sink,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	got := sink.failures[0].err.Error()
	if !strings.Contains(got, "stderr-tail:") {
		t.Errorf("error %q missing 'stderr-tail:' marker", got)
	}
	if !strings.Contains(got, "simulated auth failure") {
		t.Errorf("error %q missing stderr line from fake shim", got)
	}
}

func TestPlugin_ScanForDiff_RefMatchEmitsReceiptEntries(t *testing.T) {
	withFakeGit(t)

	arg := "git:https://github.com/amarbel-llc/cutting-garden#main"
	source := mustParseURL(t, arg)

	captureResult := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    source,
		RawArg:    arg,
		BlobStore: newDiscardStore(),
		Sink:      &recordingSink{},
	})
	if captureResult.FailCount != 0 {
		t.Fatalf("capture FailCount = %d", captureResult.FailCount)
	}

	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            source,
		RawDir:         arg,
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

func TestPlugin_ScanForDiff_RefMissTriggersRescan(t *testing.T) {
	withFakeGit(t)

	arg := "git:https://github.com/amarbel-llc/cutting-garden#main"
	source := "https://github.com/amarbel-llc/cutting-garden#main"

	staleEntries := []capture_receipt.EntryV1{
		{Path: refFileName, Root: source, Type: capture_receipt.TypeFile, BlobId: "sha256-stale"},
		{Path: bundleFileName, Root: source, Type: capture_receipt.TypeFile, BlobId: "sha256-stale-bundle"},
	}

	diffEntries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            mustParseURL(t, arg),
		RawDir:         arg,
		BlobStore:      newDiscardStore(),
		ReceiptEntries: staleEntries,
	})
	if err != nil {
		t.Fatalf("ScanForDiff: %v", err)
	}
	if len(diffEntries) != 2 {
		t.Fatalf("rescan returned %d entries, want 2: %v", len(diffEntries), entryPaths(diffEntries))
	}
	// Fresh entries carry real blob-ids, not the stale receipt's.
	for _, e := range diffEntries {
		if strings.HasPrefix(e.BlobId, "sha256-stale") {
			t.Errorf("entry %q kept stale blob-id %q", e.Path, e.BlobId)
		}
	}
}

func TestPlugin_ScanForDiff_NoEntriesForSource(t *testing.T) {
	withFakeGit(t)

	arg := "git:https://github.com/amarbel-llc/cutting-garden#main"
	_, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:        context.Background(),
		Dir:            mustParseURL(t, arg),
		RawDir:         arg,
		BlobStore:      newDiscardStore(),
		ReceiptEntries: []capture_receipt.EntryV1{{Path: "x", Root: "git://other/repo#main"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no entries for source") {
		t.Fatalf("expected 'no entries for source' error, got %v", err)
	}
}

func TestEntriesForRoot(t *testing.T) {
	source := "https://github.com/x/y#main"
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
				t.Fatalf("entriesForRoot: got %d, want %d (%v)", len(got), tc.want, entryPaths(got))
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
	if len(gotSchemes) != 1 || gotSchemes[0] != "git" {
		t.Errorf("Schemes() = %v, want [git]", gotSchemes)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestTailWriter_KeepsOnlyTail(t *testing.T) {
	var buf bytes.Buffer
	w := newTailWriter(&buf, 8)
	for _, chunk := range [][]byte{[]byte("aaaa"), []byte("bbbb"), []byte("cccc")} {
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
