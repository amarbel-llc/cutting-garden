package cutting_garden_plugin_googlephotos

import (
	"net/url"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// googlePhotosHosts is the closed set of hostnames the plugin accepts as
// a Google Photos source. Unlike the yt-dlp plugin, which claims the
// bare `https` scheme under a host allowlist, this plugin claims only
// its own `gphotos` scheme — so the allowlist is not about preventing
// cutting-garden from silently grabbing arbitrary `https:` arguments
// (a `gphotos:` prefix is always explicit). Instead it is a guard
// against pointing the Google Photos backend at a non-Google-Photos URL,
// which gallery-dl could not extract anyway.
//
//   - photos.app.goo.gl  — short share links (`gphotos:photos.app.goo.gl/XXXX`).
//   - photos.google.com  — full share URLs (`.../share/<token>`).
//
// Add a host here only after confirming gallery-dl ships a working
// extractor for its URL surface.
var googlePhotosHosts = map[string]struct{}{
	"photos.app.goo.gl": {},
	"photos.google.com": {},
}

// sourceURLFromArg extracts the canonical https Google Photos URL that
// gallery-dl will be invoked against from a parsed CLI argument. Two
// accepted forms (mirroring the git plugin's opaque/hierarchical split):
//
//   - gphotos:<share-url>     u.Opaque carries the inner URL. The inner
//     URL may be fully-qualified (`gphotos:https://photos.app.goo.gl/X`)
//     or bare-host (`gphotos:photos.app.goo.gl/X`), in which case https
//     is assumed.
//   - gphotos://<host>/<path> u.Host and u.Path; reconstructed as https.
//
// The resolved host MUST be in googlePhotosHosts. Returns an error
// suitable for ValidateSource refusal on any other host or shape. The
// resolved URL is also guarded against a leading `-` so it can never be
// misread as an option by the gallery-dl child process.
func sourceURLFromArg(u *url.URL) (string, error) {
	if u.Scheme != "gphotos" {
		return "", errors.ErrorWithStackf(
			"google-photos plugin: unsupported scheme %q in %q\n"+
				"hint: pass `gphotos:<share-url>` or `gphotos://<host>/<path>`",
			u.Scheme, u.String(),
		)
	}

	var raw string
	switch {
	case u.Opaque != "":
		// url.Parse splits ?query and #fragment off the opaque segment;
		// glue them back so the inner URL round-trips intact.
		raw = u.Opaque
		if u.RawQuery != "" {
			raw += "?" + u.RawQuery
		}
		if u.Fragment != "" {
			raw += "#" + u.Fragment
		}
		// Bare-host inner (`gphotos:photos.app.goo.gl/X`): assume https
		// so the host check and gallery-dl both see a real URL.
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}

	case u.Host != "":
		// Hierarchical form: reconstruct as https. Spelling out https
		// (rather than leaning on gallery-dl's default) keeps the
		// EntryV1.Root value stable across the two argument forms.
		rebuilt := &url.URL{
			Scheme:   "https",
			Host:     u.Host,
			Path:     u.Path,
			RawQuery: u.RawQuery,
			Fragment: u.Fragment,
		}
		raw = rebuilt.String()

	default:
		return "", errors.ErrorWithStackf(
			"google-photos plugin: empty source URL in %q\n"+
				"hint: pass `gphotos:<share-url>` or `gphotos://<host>/<path>`",
			u.String(),
		)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.ErrorWithStackf(
			"google-photos plugin: cannot parse source URL %q: %v", raw, err,
		)
	}

	host := strings.ToLower(parsed.Hostname())
	if _, ok := googlePhotosHosts[host]; !ok {
		return "", errors.ErrorWithStackf(
			"google-photos plugin: host %q is not a Google Photos host\n"+
				"hint: expected one of photos.app.goo.gl, photos.google.com",
			parsed.Host,
		)
	}

	if strings.HasPrefix(raw, "-") {
		return "", errors.ErrorWithStackf(
			"google-photos plugin: source %q begins with '-'\n"+
				"hint: a source must not look like a command-line option",
			raw,
		)
	}

	return raw, nil
}
