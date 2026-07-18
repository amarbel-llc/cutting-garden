package cutting_garden_plugin_ytdlp

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// flatPlaylistEntry is one parsed line from `yt-dlp --flat-playlist
// --dump-json`. The field set mirrors yt-dlp's YouTube "tab" extractor's
// flat-entry shape (yt_dlp/extractor/youtube/_tab.py, _extract_video, as
// of yt-dlp master 2026):
//
//   - id, title, url, uploader (+ uploader_id) are populated straight off
//     the channel/playlist LISTING page — no per-video fetch, satisfying
//     RFC 0012 §1's no-per-node-refetch rule.
//   - duration is populated whenever the listing shows a length badge
//     (present for ordinary videos; absent/null for live streams,
//     premieres, and some Shorts, per _extract_video's own duration
//     fallback chain).
//   - upload_date/timestamp are, BY DEFAULT, NOT populated in flat mode
//     at all — yt-dlp's own --flat-playlist help text: "some entry
//     metadata may be missing". flatPlaylistArgs always passes
//     `--extractor-args youtubetab:approximate_date`, which makes
//     YoutubeTabIE parse an APPROXIMATE timestamp out of the listing's
//     relative "N years ago" text (yt-dlp's own doc caveat: "This may
//     cause date-based filters to be slightly off"); YoutubeDL then
//     derives upload_date from that timestamp
//     (YoutubeDL._fill_common_fields, run even on flat/"in_playlist"
//     entries before --dump-json prints them). Non-YouTube extractors
//     ignore the youtubetab-namespaced arg and upload_date stays empty
//     for them — handled gracefully throughout (see entryFacets).
type flatPlaylistEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	WebpageURL string   `json:"webpage_url"`
	Uploader   string   `json:"uploader"`
	UploadDate string   `json:"upload_date"`
	Duration   *float64 `json:"duration"`
}

// flatPlaylistArgs builds the `yt-dlp --flat-playlist --dump-json`
// invocation that is the SHARED enumeration primitive behind capture's
// channel-vs-single-video classification (capture.go), traversal's
// ListRoots (traversal.go), and facet aggregation (facet.go) — FDR 0014
// §"Where bulk orchestration lives": every bulk consumer walks the same
// enumeration rather than re-deriving the tree independently. No `-o` is
// passed: `--dump-json` implies `--simulate`, so nothing is written to
// disk and the outDir passed to runYtdlp is irrelevant.
func flatPlaylistArgs(source string) []string {
	return []string{
		"--flat-playlist",
		"--dump-json",
		"--no-warnings",
		"--extractor-args", "youtubetab:approximate_date",
		"--",
		source,
	}
}

// probeFlatPlaylist runs the shared enumeration primitive against source
// and parses every `--dump-json` line yt-dlp writes to stdout: one line
// per playlist/channel entry, or exactly one line describing the video
// itself when source is a plain video URL (`--flat-playlist` only
// affects the CHILDREN of an actual playlist/channel result — yt-dlp
// still fully extracts a bare video URL). A single returned entry is
// therefore the FDR 0004 single-video classification signal; more than
// one is the channel/playlist path.
func probeFlatPlaylist(
	ctx context.Context, source string,
) ([]flatPlaylistEntry, error) {
	var (
		mu        sync.Mutex
		entries   []flatPlaylistEntry
		parseErrs []string
	)

	onLog := func(line string) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			// Not a --dump-json record (a stray status/diagnostic line
			// that slipped past --no-warnings) — the probe only cares
			// about --dump-json's own output.
			return
		}
		var e flatPlaylistEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			mu.Lock()
			parseErrs = append(parseErrs, err.Error())
			mu.Unlock()
			return
		}
		mu.Lock()
		entries = append(entries, e)
		mu.Unlock()
	}

	// outDir is empty: --dump-json writes nothing to disk, so there is no
	// meaningful working directory to scope the probe to.
	if err := runYtdlp(ctx, "", flatPlaylistArgs(source), nil, onLog); err != nil {
		return nil, err
	}

	if len(parseErrs) > 0 {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: %d malformed --dump-json line(s) probing %q:\n  %s",
			len(parseErrs), source, strings.Join(parseErrs, "\n  "),
		)
	}

	return entries, nil
}

// entryTargetURL resolves the URL to capture for one flat-playlist entry:
// its canonical `url` field, falling back to `webpage_url`. ok is false
// when neither is present — an entry the plugin cannot safely act on.
func entryTargetURL(e flatPlaylistEntry) (target string, ok bool) {
	if e.URL != "" {
		return e.URL, true
	}
	if e.WebpageURL != "" {
		return e.WebpageURL, true
	}
	return "", false
}

// entryVideoID resolves the path-segment identity for one entry: its
// `id` field, falling back to the last path segment of its target URL.
// ok is false when neither yields anything usable. The result is always
// sanitized (see sanitizePathSegment) since it becomes an EntryV1.Path
// prefix and a traversal Node's display name — untrusted listing data
// (RFC 0012 Security Considerations).
func entryVideoID(e flatPlaylistEntry) (id string, ok bool) {
	if e.ID != "" {
		return sanitizePathSegment(e.ID), true
	}
	if target, hasURL := entryTargetURL(e); hasURL {
		if u, err := url.Parse(target); err == nil {
			base := path.Base(strings.TrimRight(u.Path, "/"))
			if base != "" && base != "." && base != "/" {
				return sanitizePathSegment(base), true
			}
		}
	}
	return "", false
}

// sanitizePathSegment defends the FDR 0004 `<video-id>/` receipt path
// prefix against a hostile or malformed id: path separators and the
// parent/self directory tokens can never survive into an EntryV1.Path
// segment, since the id comes from an external listing, not a trusted
// local filesystem walk.
func sanitizePathSegment(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// channelLimitParam is the cutting-garden-only query parameter that
// carries the FDR 0004 `--ytdlp-limit` cost guardrail. It is stripped
// from the URL before the URL reaches yt-dlp (see extractChannelLimit) —
// CaptureRootRequest has no per-plugin options field (unlike, say, the
// optical plugin's `?mode=` convention, which stays on the URL yt-dlp
// itself would never see), so a query parameter on the source URL is the
// established precedent for a plugin-specific knob in this SDK. Value 0
// means "no cap" — an explicit opt into unlimited (FDR 0004 Open
// Questions).
const channelLimitParam = "cg-ytdlp-limit"

// defaultChannelCaptureThreshold is the entry count above which a
// channel/playlist capture refuses without an explicit channelLimitParam —
// the FDR 0004 "necessary for shippability" guardrail: without it,
// capturing a large channel silently saturates the destination blob
// store. Refuse-with-hint (rather than silently truncating) is the
// posture FDR 0004's Open Questions leans toward.
const defaultChannelCaptureThreshold = 25

// extractChannelLimit splits u into the yt-dlp-facing URL (with
// channelLimitParam stripped from its query string) and the requested
// limit. hasLimit is false when the caller passed no channelLimitParam,
// in which case cleaned == u unchanged (no query re-encoding), so the
// single-video path's existing byte-identical URL handling is untouched
// for every caller that never uses this guardrail.
func extractChannelLimit(u *url.URL) (cleaned *url.URL, limit int, hasLimit bool, err error) {
	if u == nil {
		return nil, 0, false, errors.ErrorWithStackf(
			"ytdlp plugin: nil source URL",
		)
	}
	raw := u.Query().Get(channelLimitParam)
	if raw == "" {
		return u, 0, false, nil
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil || n < 0 {
		return nil, 0, false, errors.ErrorWithStackf(
			"ytdlp plugin: invalid %s value %q (want a non-negative integer; 0 means no cap)",
			channelLimitParam, raw,
		)
	}
	q := u.Query()
	q.Del(channelLimitParam)
	clone := *u
	clone.RawQuery = q.Encode()
	return &clone, n, true, nil
}

// applyChannelLimit truncates entries per the FDR 0004 cost guardrail: an
// explicit channelLimitParam caps to that many entries (0, or a limit
// at/above len(entries), means no cap — an explicit opt into
// unlimited). With no explicit limit, entries beyond
// defaultChannelCaptureThreshold are refused rather than silently
// downloaded, with a hint at the query param that lifts the cap.
func applyChannelLimit(
	entries []flatPlaylistEntry, hasLimit bool, limit int,
) ([]flatPlaylistEntry, error) {
	if hasLimit {
		if limit == 0 || limit >= len(entries) {
			return entries, nil
		}
		return entries[:limit], nil
	}
	if len(entries) > defaultChannelCaptureThreshold {
		return nil, errors.ErrorWithStackf(
			"ytdlp plugin: %d videos found, exceeding the default cap of %d\n"+
				"hint: add `?%s=N` to the source URL to capture only the newest N (or `?%s=0` for no cap)",
			len(entries), defaultChannelCaptureThreshold,
			channelLimitParam, channelLimitParam,
		)
	}
	return entries, nil
}
