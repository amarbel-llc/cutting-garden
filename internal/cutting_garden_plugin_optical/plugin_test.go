package cutting_garden_plugin_optical

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_failures"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// fakeDdrescueScript stands in for GNU ddrescue: it writes the disc
// image and rescue map (the fixed output names toolInvocation passes)
// with deterministic bytes so blob-ids are stable across runs. cmd.Dir
// is the capture tempdir, so the relative names land there.
const fakeDdrescueScript = `#!/bin/sh
set -e
printf 'fake-iso-bytes' > disc.iso
printf '# fake ddrescue mapfile\n0x00000000 0x00100000 +\n' > disc.iso.map
echo "ddrescue: finished" >&2
`

// fakeCdparanoiaScript stands in for cdparanoia -B: two deterministic
// track WAVs in the working directory.
const fakeCdparanoiaScript = `#!/bin/sh
set -e
printf 'RIFFfake-track-1WAVE' > track01.cdda.wav
printf 'RIFFfake-track-2WAVE' > track02.cdda.wav
echo "cdparanoia: done" >&2
`

// failingScript writes a recognizable stderr line and exits non-zero so
// tests can assert the stderr-tail propagation path.
const failingScript = `#!/bin/sh
echo "fake: no medium found" >&2
exit 1
`

// installFakeBin writes script as an executable named binName on PATH
// for the duration of t, restoring PATH on cleanup.
func installFakeBin(t *testing.T, binName, script string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, binName)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", binName, err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

func newDiscardStore() blob_stores.BlobStoreInitialized {
	return blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)
}

func TestPlugin_Schemes_TypeTag(t *testing.T) {
	p := Plugin{}
	gotSchemes := p.Schemes()
	if len(gotSchemes) != 1 || gotSchemes[0] != opticalScheme {
		t.Errorf("Schemes() = %v, want [%s]", gotSchemes, opticalScheme)
	}
	if p.TypeTag() != capture_receipt.TypeTagV1 {
		t.Errorf("TypeTag() = %q, want %q", p.TypeTag(), capture_receipt.TypeTagV1)
	}
}

func TestPlugin_CaptureRoot_ImageMode_WritesImageAndMap(t *testing.T) {
	installFakeBin(t, ddrescueBin, fakeDdrescueScript)

	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "optical:/dev/sr0"),
		RawArg:    "optical:/dev/sr0",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %v", result.FailCount, rep.failures)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (image + map): %v",
			len(result.Entries), entryPaths(result.Entries))
	}

	want := map[string]bool{imageFilename: false, mapFilename: false}
	for _, e := range result.Entries {
		if e.Type != capture_receipt.TypeFile {
			t.Errorf("entry %q: Type = %q, want file", e.Path, e.Type)
		}
		if e.Root != "/dev/sr0" {
			t.Errorf("entry %q: Root = %q, want /dev/sr0", e.Path, e.Root)
		}
		if e.BlobId == "" {
			t.Errorf("entry %q: BlobId empty", e.Path)
		}
		if _, ok := want[e.Path]; ok {
			want[e.Path] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing expected artifact %q", name)
		}
	}
}

func TestPlugin_CaptureRoot_AudioMode_WritesTracks(t *testing.T) {
	installFakeBin(t, cdparanoiaBin, fakeCdparanoiaScript)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "optical:/dev/sr0?mode=audio"),
		RawArg:    "optical:/dev/sr0?mode=audio",
		BlobStore: newDiscardStore(),
		// Reporter deliberately omitted: a nil stream must be tolerated.
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0", result.FailCount)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 tracks: %v",
			len(result.Entries), entryPaths(result.Entries))
	}
	for _, e := range result.Entries {
		if !strings.HasSuffix(e.Path, ".cdda.wav") {
			t.Errorf("entry %q: want .cdda.wav suffix", e.Path)
		}
	}
}

func TestPlugin_CaptureRoot_ParseError_RootFailure(t *testing.T) {
	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "optical://dev/sr0"), // host form — invalid
		RawArg:    "optical://dev/sr0",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if len(rep.failures) != 1 {
		t.Fatalf("stream failures = %d, want 1", len(rep.failures))
	}
	if !strings.Contains(rep.failures[0].err.Error(), "unexpected host") {
		t.Errorf("error %q missing 'unexpected host'", rep.failures[0].err.Error())
	}
}

func TestPlugin_CaptureRoot_ToolFailure_SurfacesStderrTail(t *testing.T) {
	installFakeBin(t, ddrescueBin, failingScript)

	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "optical:/dev/sr0"),
		RawArg:    "optical:/dev/sr0",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if result.FailCount != len(result.Failures) {
		t.Errorf("FailCount = %d, want len(Failures) = %d",
			result.FailCount, len(result.Failures))
	}
	f := result.Failures[0]
	if f.Op != capture_failures.OpPlugin {
		t.Errorf("Failures[0].Op = %q, want %q", f.Op, capture_failures.OpPlugin)
	}
	if f.Root != "/dev/sr0" {
		t.Errorf("Failures[0].Root = %q, want /dev/sr0", f.Root)
	}
	if !strings.Contains(f.Error, "no medium found") {
		t.Errorf("Failures[0].Error = %q, want the tool stderr detail", f.Error)
	}

	// The download phase verdict must be a failure carrying the error.
	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	if rep.phaseEnds[0].OK {
		t.Errorf("phaseEnds[0].OK = true, want false on tool failure")
	}
}

func TestPlugin_CaptureRoot_ToolMissing_RootFailure(t *testing.T) {
	// Point PATH at an empty dir so exec.LookPath misses ddrescue.
	t.Setenv("PATH", t.TempDir())

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "optical:/dev/sr0"),
		RawArg:    "optical:/dev/sr0",
		BlobStore: newDiscardStore(),
	})

	if result.FailCount != 1 {
		t.Fatalf("FailCount = %d, want 1", result.FailCount)
	}
	if !strings.Contains(result.Failures[0].Error, "not found on PATH") {
		t.Errorf("Failures[0].Error = %q, want 'not found on PATH'", result.Failures[0].Error)
	}
}

func entryPaths(es []capture_receipt.EntryV1) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

// Compile-time assertion that recordingReporter satisfies the Reporter
// interface.
var _ cutting_garden_plugins.Reporter = (*recordingReporter)(nil)

// recordingReporter captures Stream events for assertions, embedding
// capture_events.Nop so it only overrides what the tests inspect.
type recordingReporter struct {
	capture_events.Nop
	phaseStarts []string
	phaseEnds   []capture_events.Verdict
	entries     []capture_receipt.EntryV1
	failures    []streamFailure
}

type streamFailure struct {
	source string
	err    error
}

func (r *recordingReporter) PhaseStart(description string) {
	r.phaseStarts = append(r.phaseStarts, description)
}

func (r *recordingReporter) PhaseEnd(v capture_events.Verdict) {
	r.phaseEnds = append(r.phaseEnds, v)
}

func (r *recordingReporter) Entry(e capture_receipt.EntryV1) {
	r.entries = append(r.entries, e)
}

func (r *recordingReporter) Failure(source string, err error) {
	r.failures = append(r.failures, streamFailure{source: source, err: err})
}
