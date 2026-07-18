package cutting_garden_plugin_ytdlp

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// channelFakeYtdlpScript models a two-video channel end to end: a
// `--flat-playlist --dump-json` call against a URL containing "channel"
// yields two entries; a plain per-video download call (no
// --flat-playlist) writes a deterministic info.json + media file keyed
// by the ACTUAL video id parsed out of the trailing source URL's `v=`
// query parameter, so each of the two per-video captures in the fan-out
// produces distinguishable artifacts. Any other flat-playlist probe
// (i.e. a bare video URL) yields exactly one entry — the single-video
// classification path.
const channelFakeYtdlpScript = `#!/bin/sh
set -e

flat=0
template=""
pending=""
source=""
prevdash=0
for arg in "$@"; do
  if [ "$pending" = "out" ]; then
    template="$arg"
    pending=""
    continue
  fi
  if [ "$prevdash" = "1" ]; then
    source="$arg"
  fi
  case "$arg" in
    --flat-playlist) flat=1 ;;
    -o) pending=out ;;
    --) prevdash=1 ;;
  esac
done

if [ "$flat" = "1" ]; then
  case "$source" in
    *channel*)
      printf '{"id":"v1","title":"Video One","url":"https://www.youtube.com/watch?v=v1","uploader":"Chan","upload_date":"20260101","duration":100}\n'
      printf '{"id":"v2","title":"Video Two","url":"https://www.youtube.com/watch?v=v2","uploader":"Chan","upload_date":"20260715","duration":1500}\n'
      ;;
    *)
      printf '{"id":"solo","title":"Solo","url":"%s","uploader":"Solo","upload_date":"20260101","duration":10}\n' "$source"
      ;;
  esac
  exit 0
fi

if [ -z "$template" ]; then
  echo "fake-yt-dlp: missing -o template" >&2
  exit 64
fi

id=$(echo "$source" | sed -n 's/.*[?&]v=\([^&]*\).*/\1/p')
if [ -z "$id" ]; then
  id="unknown"
fi

prefix="$(echo "$template" | sed "s/%(id)s/$id/" | sed 's/\.%(ext)s$//')"
mkdir -p "$(dirname "$prefix")"
echo "{\"id\":\"$id\"}" > "$prefix.info.json"
printf 'media-bytes-%s' "$id" > "$prefix.mp4"
`

// channelFakeFailingSecondVideoScript is channelFakeYtdlpScript with one
// change: the per-video download for id "v2" fails non-zero, modeling
// FDR 0004's "one geo-blocked video shouldn't torch a 500-video archive"
// requirement — v1 must still succeed and appear in the result.
const channelFakeFailingSecondVideoScript = `#!/bin/sh
set -e

flat=0
template=""
pending=""
source=""
prevdash=0
for arg in "$@"; do
  if [ "$pending" = "out" ]; then
    template="$arg"
    pending=""
    continue
  fi
  if [ "$prevdash" = "1" ]; then
    source="$arg"
  fi
  case "$arg" in
    --flat-playlist) flat=1 ;;
    -o) pending=out ;;
    --) prevdash=1 ;;
  esac
done

if [ "$flat" = "1" ]; then
  printf '{"id":"v1","title":"Video One","url":"https://www.youtube.com/watch?v=v1","uploader":"Chan","upload_date":"20260101","duration":100}\n'
  printf '{"id":"v2","title":"Video Two","url":"https://www.youtube.com/watch?v=v2","uploader":"Chan","upload_date":"20260715","duration":1500}\n'
  exit 0
fi

id=$(echo "$source" | sed -n 's/.*[?&]v=\([^&]*\).*/\1/p')

if [ "$id" = "v2" ]; then
  echo "fake-yt-dlp: simulated geo-block for v2" >&2
  exit 1
fi

if [ -z "$template" ]; then
  echo "fake-yt-dlp: missing -o template" >&2
  exit 64
fi

prefix="$(echo "$template" | sed "s/%(id)s/$id/" | sed 's/\.%(ext)s$//')"
mkdir -p "$(dirname "$prefix")"
echo "{\"id\":\"$id\"}" > "$prefix.info.json"
printf 'media-bytes-%s' "$id" > "$prefix.mp4"
`

func TestPlugin_CaptureRoot_ChannelFansOutPerVideoWithParentChildPaths(t *testing.T) {
	installFakeYtdlp(t, channelFakeYtdlpScript)

	rep := &recordingReporter{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "https://www.youtube.com/@channel"),
		RawArg:    "https://www.youtube.com/@channel",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %+v", result.FailCount, result.Failures)
	}
	// Two videos * two artifacts each (info.json + mp4) = 4 entries.
	if len(result.Entries) != 4 {
		t.Fatalf("got %d entries, want 4: %v", len(result.Entries), entryPaths(result.Entries))
	}

	const channelRoot = "https://www.youtube.com/@channel"
	seenPrefixes := map[string]bool{}
	for _, e := range result.Entries {
		if e.Root != channelRoot {
			t.Errorf("entry %q Root = %q, want the CHANNEL URL %q (FDR 0004: Root is constant across every video)",
				e.Path, e.Root, channelRoot)
		}
		if !strings.Contains(e.Path, "/") {
			t.Errorf("entry Path %q missing the <video-id>/ prefix", e.Path)
		}
		prefix := strings.SplitN(e.Path, "/", 2)[0]
		seenPrefixes[prefix] = true
	}
	wantPrefixes := []string{"v1", "v2"}
	gotPrefixes := make([]string, 0, len(seenPrefixes))
	for p := range seenPrefixes {
		gotPrefixes = append(gotPrefixes, p)
	}
	sort.Strings(gotPrefixes)
	if len(gotPrefixes) != len(wantPrefixes) {
		t.Fatalf("video-id path prefixes = %v, want %v", gotPrefixes, wantPrefixes)
	}
	for i := range wantPrefixes {
		if gotPrefixes[i] != wantPrefixes[i] {
			t.Errorf("prefixes[%d] = %q, want %q", i, gotPrefixes[i], wantPrefixes[i])
		}
	}

	// Each video's artifacts must be distinguishable (the fake keys
	// content on the actual per-video source URL, not a fixed stub id).
	blobsByPrefix := map[string]map[string]bool{}
	for _, e := range result.Entries {
		prefix := strings.SplitN(e.Path, "/", 2)[0]
		if blobsByPrefix[prefix] == nil {
			blobsByPrefix[prefix] = map[string]bool{}
		}
		blobsByPrefix[prefix][e.BlobId] = true
	}
	if len(blobsByPrefix["v1"]) == 0 || len(blobsByPrefix["v2"]) == 0 {
		t.Fatalf("missing per-video blobs: %+v", blobsByPrefix)
	}
	for id := range blobsByPrefix["v1"] {
		if blobsByPrefix["v2"][id] {
			t.Errorf("v1 and v2 share a blob id %q — the fake isn't producing distinguishable per-video content", id)
		}
	}
}

func TestPlugin_CaptureRoot_ChannelAggregatesPerVideoFailures(t *testing.T) {
	installFakeYtdlp(t, channelFakeFailingSecondVideoScript)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "https://www.youtube.com/@channel"),
		RawArg:    "https://www.youtube.com/@channel",
		BlobStore: newDiscardStore(),
	})

	// v1 succeeds (2 artifacts); v2 fails entirely. A failing video must
	// not abort the channel (FDR 0004: "one geo-blocked video shouldn't
	// torch a 500-video archive").
	if len(result.Entries) != 2 {
		t.Fatalf("got %d entries, want 2 (v1's artifacts only): %v", len(result.Entries), entryPaths(result.Entries))
	}
	for _, e := range result.Entries {
		if !strings.HasPrefix(e.Path, "v1/") {
			t.Errorf("entry %q leaked from the failed video v2", e.Path)
		}
	}

	if result.FailCount == 0 {
		t.Fatal("FailCount = 0, want at least 1 for the failed video v2")
	}
	var sawV2Failure bool
	for _, f := range result.Failures {
		if f.Root != "https://www.youtube.com/@channel" {
			t.Errorf("failure %+v Root should be the channel URL", f)
		}
		if strings.Contains(f.Error, "simulated geo-block") {
			sawV2Failure = true
			if f.Path != "v2" {
				t.Errorf("v2 failure Path = %q, want the bare video id \"v2\"", f.Path)
			}
		}
	}
	if !sawV2Failure {
		t.Fatalf("no failure recorded for v2's geo-block: %+v", result.Failures)
	}
}

func TestPlugin_CaptureRoot_ChannelRespectsExplicitLimit(t *testing.T) {
	installFakeYtdlp(t, channelFakeYtdlpScript)

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "https://www.youtube.com/@channel?cg-ytdlp-limit=1"),
		RawArg:    "https://www.youtube.com/@channel?cg-ytdlp-limit=1",
		BlobStore: newDiscardStore(),
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %+v", result.FailCount, result.Failures)
	}
	for _, e := range result.Entries {
		if !strings.HasPrefix(e.Path, "v1/") {
			t.Errorf("entry %q present despite cg-ytdlp-limit=1 (only the first video should be captured)", e.Path)
		}
	}
	if len(result.Entries) == 0 {
		t.Fatal("no entries captured with cg-ytdlp-limit=1, want v1's artifacts")
	}
}

func TestPlugin_CaptureRoot_ChannelAboveDefaultThresholdRefusesWithHint(t *testing.T) {
	installFakeYtdlp(t, manyVideosFakeYtdlpScript(defaultChannelCaptureThreshold+1))

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "https://www.youtube.com/@bigchannel"),
		RawArg:    "https://www.youtube.com/@bigchannel",
		BlobStore: newDiscardStore(),
	})

	if result.FailCount == 0 {
		t.Fatal("FailCount = 0, want a refusal above the default threshold")
	}
	if len(result.Entries) != 0 {
		t.Fatalf("got %d entries, want 0 (refused before any download)", len(result.Entries))
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures = %+v, want exactly one root-level refusal", result.Failures)
	}
	if !strings.Contains(result.Failures[0].Error, channelLimitParam) {
		t.Errorf("refusal error %q missing hint at %s", result.Failures[0].Error, channelLimitParam)
	}
}

func TestPlugin_CaptureRoot_ChannelExplicitZeroLimitBypassesThreshold(t *testing.T) {
	n := defaultChannelCaptureThreshold + 1
	installFakeYtdlp(t, manyVideosFakeYtdlpScript(n))

	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, "https://www.youtube.com/@bigchannel?cg-ytdlp-limit=0"),
		RawArg:    "https://www.youtube.com/@bigchannel?cg-ytdlp-limit=0",
		BlobStore: newDiscardStore(),
	})

	if result.FailCount != 0 {
		t.Fatalf("FailCount = %d, want 0; failures: %+v", result.FailCount, result.Failures)
	}
	prefixes := map[string]bool{}
	for _, e := range result.Entries {
		prefixes[strings.SplitN(e.Path, "/", 2)[0]] = true
	}
	if len(prefixes) != n {
		t.Fatalf("captured %d distinct videos, want all %d (cg-ytdlp-limit=0 == unlimited)", len(prefixes), n)
	}
}

// manyVideosFakeYtdlpScript returns a fake yt-dlp shim whose
// --flat-playlist response has n synthetic entries and whose per-video
// download always succeeds with a tiny fixed artifact, keyed by the
// actual id in the -o template — used to exercise the
// defaultChannelCaptureThreshold guardrail at both sides of the
// boundary without hand-writing n printf lines.
func manyVideosFakeYtdlpScript(n int) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n\nflat=0\ntemplate=\"\"\npending=\"\"\nsource=\"\"\nprevdash=0\n")
	b.WriteString("for arg in \"$@\"; do\n")
	b.WriteString("  if [ \"$pending\" = \"out\" ]; then\n    template=\"$arg\"\n    pending=\"\"\n    continue\n  fi\n")
	b.WriteString("  if [ \"$prevdash\" = \"1\" ]; then\n    source=\"$arg\"\n  fi\n")
	b.WriteString("  case \"$arg\" in\n    --flat-playlist) flat=1 ;;\n    -o) pending=out ;;\n    --) prevdash=1 ;;\n  esac\n")
	b.WriteString("done\n\n")
	b.WriteString("if [ \"$flat\" = \"1\" ]; then\n")
	for i := 1; i <= n; i++ {
		b.WriteString(sprintfEntry(i))
	}
	b.WriteString("  exit 0\nfi\n\n")
	b.WriteString("id=$(echo \"$source\" | sed -n 's/.*[?&]v=\\([^&]*\\).*/\\1/p')\n")
	b.WriteString("prefix=\"$(echo \"$template\" | sed \"s/%(id)s/$id/\" | sed 's/\\.%(ext)s$//')\"\n")
	b.WriteString("mkdir -p \"$(dirname \"$prefix\")\"\n")
	b.WriteString("printf 'x' > \"$prefix.mp4\"\n")
	return b.String()
}

func sprintfEntry(i int) string {
	id := "v" + strconv.Itoa(i)
	return "  printf '{\"id\":\"" + id + "\",\"title\":\"V\",\"url\":\"https://www.youtube.com/watch?v=" + id + "\",\"uploader\":\"Chan\",\"duration\":10}\\n'\n"
}
