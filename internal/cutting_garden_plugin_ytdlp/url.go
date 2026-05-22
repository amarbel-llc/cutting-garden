package cutting_garden_plugin_ytdlp

import (
	"net/url"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// youtubeHosts is the closed allowlist for the bare-https acceptance
// path. Other yt-dlp-supported sites must be prefixed with the
// explicit `ytdlp:` scheme so cutting-garden does not silently claim
// every `https:` argument.
var youtubeHosts = map[string]struct{}{
	"youtu.be":          {},
	"youtube.com":       {},
	"www.youtube.com":   {},
	"m.youtube.com":     {},
	"music.youtube.com": {},
}

// sourceURLFromArg extracts the canonical https URL that yt-dlp will
// be invoked against from a parsed CLI argument. Three accepted forms:
//
//   - ytdlp:<source-url>     u.Opaque carries the full inner URL.
//   - ytdlp://<host>/<path>  u.Host and u.Path; reconstructed as https.
//   - https://<youtube-host>/<path>  passed through verbatim.
//
// Returns an error suitable for ValidateSource refusal on https hosts
// outside the YouTube allowlist or on any other unsupported shape.
func sourceURLFromArg(u *url.URL) (string, error) {
	switch u.Scheme {
	case "ytdlp":
		if u.Opaque != "" {
			// url.Parse splits ?query and #fragment off the opaque
			// segment even for `scheme:opaque?query` shapes, so glue
			// them back so the inner URL round-trips intact.
			rebuilt := u.Opaque
			if u.RawQuery != "" {
				rebuilt += "?" + u.RawQuery
			}
			if u.Fragment != "" {
				rebuilt += "#" + u.Fragment
			}
			return rebuilt, nil
		}
		if u.Host == "" {
			return "", errors.ErrorWithStackf(
				"ytdlp plugin: empty source URL in %q\n"+
					"hint: pass `ytdlp:<full-url>` or `ytdlp://<host>/<path>`",
				u.String(),
			)
		}
		// Reconstruct as https. yt-dlp itself defaults to https for
		// schemeless inputs, but spelling it out keeps the EntryV1.Root
		// value stable across the two ytdlp forms.
		rebuilt := &url.URL{
			Scheme:   "https",
			Host:     u.Host,
			Path:     u.Path,
			RawQuery: u.RawQuery,
			Fragment: u.Fragment,
		}
		return rebuilt.String(), nil

	case "https":
		host := strings.ToLower(u.Host)
		if _, ok := youtubeHosts[host]; !ok {
			return "", errors.ErrorWithStackf(
				"ytdlp plugin: https host %q is not in the YouTube allowlist\n"+
					"hint: prefix the URL with `ytdlp:` to force the yt-dlp plugin",
				u.Host,
			)
		}
		return u.String(), nil
	}

	return "", errors.ErrorWithStackf(
		"ytdlp plugin: unsupported scheme %q in %q",
		u.Scheme, u.String(),
	)
}
