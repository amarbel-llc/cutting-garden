package cutting_garden_plugin_ytdlp

import (
	"context"
	"strings"
	"testing"
)

// flatPlaylistFakeScript mimics `yt-dlp --flat-playlist --dump-json`
// against a fixture "channel" URL: three NDJSON records reproducing the
// real field shape verified against yt-dlp's YoutubeTabIE source
// (_extract_video) — id/title/url/uploader always present, duration
// present for two of three (the third models a live/premiere entry with
// no duration), upload_date present only for entries that would have it
// under `--extractor-args youtubetab:approximate_date` (one entry omits
// it, modeling a non-YouTube extractor that ignores the arg). Any other
// source URL gets a single-record response — the single-video
// classification path.
const flatPlaylistFakeScript = `#!/bin/sh
source=""
prevdash=0
for arg in "$@"; do
  if [ "$prevdash" = "1" ]; then
    source="$arg"
  fi
  case "$arg" in
    --) prevdash=1 ;;
  esac
done

case "$source" in
  *channel*)
    printf '{"id":"v1","title":"Video One","url":"https://www.youtube.com/watch?v=v1","uploader":"Chan","upload_date":"20260101","duration":100.0}\n'
    printf '{"id":"v2","title":"Video Two","url":"https://www.youtube.com/watch?v=v2","uploader":"Chan","upload_date":"20260715","duration":1500}\n'
    printf '{"id":"v3","title":"Video Three (live)","url":"https://www.youtube.com/watch?v=v3","uploader":"Chan"}\n'
    ;;
  *)
    printf '{"id":"solo","title":"Solo","url":"%s","uploader":"Solo Uploader","upload_date":"20260101","duration":10}\n' "$source"
    ;;
esac
`

func installFlatPlaylistFake(t *testing.T) {
	t.Helper()
	installFakeYtdlp(t, flatPlaylistFakeScript)
}

func TestProbeFlatPlaylist_ParsesMultiEntryChannel(t *testing.T) {
	installFlatPlaylistFake(t)

	entries, err := probeFlatPlaylist(context.Background(), "https://www.youtube.com/@channel")
	if err != nil {
		t.Fatalf("probeFlatPlaylist: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}

	if entries[0].ID != "v1" || entries[0].Uploader != "Chan" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[0].Duration == nil || *entries[0].Duration != 100.0 {
		t.Errorf("entries[0].Duration = %v, want 100.0", entries[0].Duration)
	}
	if entries[0].UploadDate != "20260101" {
		t.Errorf("entries[0].UploadDate = %q, want 20260101", entries[0].UploadDate)
	}

	// v3 models a live/premiere entry: no duration, no upload_date. Both
	// MUST decode as their Go zero values without error, since flat mode
	// legitimately omits them for some entries (yt-dlp's own docs: "some
	// entry metadata may be missing").
	if entries[2].Duration != nil {
		t.Errorf("entries[2].Duration = %v, want nil (absent)", entries[2].Duration)
	}
	if entries[2].UploadDate != "" {
		t.Errorf("entries[2].UploadDate = %q, want empty (absent)", entries[2].UploadDate)
	}
}

func TestProbeFlatPlaylist_SingleVideoYieldsOneEntry(t *testing.T) {
	installFlatPlaylistFake(t)

	entries, err := probeFlatPlaylist(context.Background(), "https://youtu.be/dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("probeFlatPlaylist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (single-video classification): %+v", len(entries), entries)
	}
	if entries[0].ID != "solo" {
		t.Errorf("entries[0].ID = %q, want solo", entries[0].ID)
	}
}

func TestProbeFlatPlaylist_NonZeroExit_PropagatesError(t *testing.T) {
	installFakeYtdlp(t, failingYtdlpScript)

	_, err := probeFlatPlaylist(context.Background(), "https://youtu.be/abc")
	if err == nil {
		t.Fatal("probeFlatPlaylist returned nil error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "simulated geo-block") {
		t.Errorf("error %q missing stderr diagnostic", err.Error())
	}
}

func TestEntryTargetURL(t *testing.T) {
	cases := []struct {
		name string
		e    flatPlaylistEntry
		want string
		ok   bool
	}{
		{"url present", flatPlaylistEntry{URL: "https://a"}, "https://a", true},
		{"webpage_url fallback", flatPlaylistEntry{WebpageURL: "https://b"}, "https://b", true},
		{"neither", flatPlaylistEntry{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := entryTargetURL(tc.e)
			if got != tc.want || ok != tc.ok {
				t.Errorf("entryTargetURL(%+v) = (%q, %v), want (%q, %v)", tc.e, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEntryVideoID(t *testing.T) {
	cases := []struct {
		name string
		e    flatPlaylistEntry
		want string
		ok   bool
	}{
		{"id present", flatPlaylistEntry{ID: "abc123"}, "abc123", true},
		{"id sanitized", flatPlaylistEntry{ID: "a/b/../c"}, "a_b_.._c", true},
		{"falls back to url path", flatPlaylistEntry{URL: "https://x.example/watch/xyz"}, "xyz", true},
		{"nothing usable", flatPlaylistEntry{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := entryVideoID(tc.e)
			if got != tc.want || ok != tc.ok {
				t.Errorf("entryVideoID(%+v) = (%q, %v), want (%q, %v)", tc.e, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestSanitizePathSegment(t *testing.T) {
	cases := map[string]string{
		"normal":  "normal",
		"a/b":     "a_b",
		`a\b`:     "a_b",
		"..":      "_",
		".":       "_",
		"":        "_",
		"../../x": ".._.._x",
	}
	for in, want := range cases {
		if got := sanitizePathSegment(in); got != want {
			t.Errorf("sanitizePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractChannelLimit_NoParamLeavesURLUnchanged(t *testing.T) {
	u := mustParseURL(t, "ytdlp:https://www.youtube.com/@channel")
	cleaned, limit, hasLimit, err := extractChannelLimit(u)
	if err != nil {
		t.Fatalf("extractChannelLimit: %v", err)
	}
	if hasLimit {
		t.Errorf("hasLimit = true, want false")
	}
	if limit != 0 {
		t.Errorf("limit = %d, want 0", limit)
	}
	if cleaned != u {
		t.Errorf("cleaned URL should be the SAME pointer when no limit param is present (byte-identical round-trip for the single-video path)")
	}
}

func TestExtractChannelLimit_StripsParamAndPreservesOthers(t *testing.T) {
	u := mustParseURL(t, "ytdlp:https://www.youtube.com/playlist?list=PL123&cg-ytdlp-limit=5")
	cleaned, limit, hasLimit, err := extractChannelLimit(u)
	if err != nil {
		t.Fatalf("extractChannelLimit: %v", err)
	}
	if !hasLimit || limit != 5 {
		t.Fatalf("hasLimit=%v limit=%d, want true/5", hasLimit, limit)
	}
	if cleaned.Query().Get(channelLimitParam) != "" {
		t.Errorf("cleaned query still carries %s: %v", channelLimitParam, cleaned.Query())
	}
	if cleaned.Query().Get("list") != "PL123" {
		t.Errorf("cleaned query lost unrelated param `list`: %v", cleaned.Query())
	}
}

func TestExtractChannelLimit_RejectsInvalidValue(t *testing.T) {
	for _, raw := range []string{"-1", "abc", "1.5"} {
		u := mustParseURL(t, "ytdlp:https://www.youtube.com/@channel?cg-ytdlp-limit="+raw)
		if _, _, _, err := extractChannelLimit(u); err == nil {
			t.Errorf("extractChannelLimit(limit=%q) returned nil error, want a validation error", raw)
		}
	}
}

func TestApplyChannelLimit_RefusesAboveThresholdWithNoExplicitLimit(t *testing.T) {
	entries := make([]flatPlaylistEntry, defaultChannelCaptureThreshold+1)
	_, err := applyChannelLimit(entries, false, 0)
	if err == nil {
		t.Fatal("applyChannelLimit returned nil error above the default threshold with no explicit limit")
	}
	if !strings.Contains(err.Error(), channelLimitParam) {
		t.Errorf("error %q missing hint at %s", err.Error(), channelLimitParam)
	}
}

func TestApplyChannelLimit_AllowsAtOrBelowThreshold(t *testing.T) {
	entries := make([]flatPlaylistEntry, defaultChannelCaptureThreshold)
	got, err := applyChannelLimit(entries, false, 0)
	if err != nil {
		t.Fatalf("applyChannelLimit: %v", err)
	}
	if len(got) != defaultChannelCaptureThreshold {
		t.Errorf("got %d entries, want %d", len(got), defaultChannelCaptureThreshold)
	}
}

func TestApplyChannelLimit_ExplicitLimitTruncates(t *testing.T) {
	entries := make([]flatPlaylistEntry, 10)
	got, err := applyChannelLimit(entries, true, 3)
	if err != nil {
		t.Fatalf("applyChannelLimit: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d entries, want 3", len(got))
	}
}

func TestApplyChannelLimit_ExplicitZeroMeansUnlimited(t *testing.T) {
	entries := make([]flatPlaylistEntry, defaultChannelCaptureThreshold+50)
	got, err := applyChannelLimit(entries, true, 0)
	if err != nil {
		t.Fatalf("applyChannelLimit: %v", err)
	}
	if len(got) != len(entries) {
		t.Errorf("got %d entries, want all %d (explicit 0 == unlimited)", len(got), len(entries))
	}
}
