package cutting_garden_plugin_ytdlp

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// httpsAllowlist is the closed set of hostnames the plugin claims via
// the bare-`https:` acceptance path. Each entry is a site whose URL
// shape yt-dlp routes to one of its built-in extractors AND whose
// hosts we want cutting-garden to consume without forcing the
// `ytdlp:` prefix. Other yt-dlp-supported sites must be prefixed
// with `ytdlp:` so cutting-garden does not silently claim every
// `https:` argument.
//
// Add a host here only after confirming: (1) yt-dlp ships a working
// extractor for its URL surface, (2) at least one URL shape works
// without site-specific auth (auth requirements are yt-dlp's
// responsibility — captures fail with yt-dlp's error if the user
// hits an auth-gated URL).
var httpsAllowlist = map[string]struct{}{
	// YouTube
	"youtu.be":          {},
	"youtube.com":       {},
	"www.youtube.com":   {},
	"m.youtube.com":     {},
	"music.youtube.com": {},
	// Instagram (#44). Public posts/reels work without auth;
	// stories and private accounts need yt-dlp cookies — surfaced
	// as yt-dlp's error at capture time.
	"instagram.com":     {},
	"www.instagram.com": {},
}

// sourceURLFromArg extracts the canonical https URL that yt-dlp will
// be invoked against from a parsed CLI argument. Three accepted forms:
//
//   - ytdlp:<source-url>     u.Opaque carries the full inner URL.
//   - ytdlp://<host>/<path>  u.Host and u.Path; reconstructed as https.
//   - https://<youtube-host>/<path>  passed through verbatim.
//
// Returns an error suitable for ValidateSource refusal on https hosts
// outside the allowlist or on any other unsupported shape.
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
		if _, ok := httpsAllowlist[host]; !ok {
			return "", errors.ErrorWithStackf(
				"ytdlp plugin: https host %q is not in the bare-https allowlist\n"+
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
