package cutting_garden_plugin_web

import (
	"net/url"
	"os"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// captureTarget extracts the http(s) URL chrest should navigate to from a
// `web:<url>` argument. The canonical (and only accepted) form is the
// opaque one — `web:https://example.com` — so url.Parse yields
// Scheme="web", Opaque="https://example.com". The inner URL must itself
// be http or https. Structural only; performs no network access.
func captureTarget(u *url.URL, raw string) (string, error) {
	if u == nil {
		return "", errors.BadRequestf("web plugin: nil source for %q", raw)
	}
	if u.Scheme != "web" {
		return "", errors.BadRequestf(
			"web plugin: unsupported scheme %q (use web:<http(s)-url>)", u.Scheme,
		)
	}

	// Derive the inner URL from raw rather than u.Opaque: the outer parse
	// peels a `?query`/`#fragment` off into u.RawQuery/u.Fragment, so
	// u.Opaque drops them. Stripping the `web:` prefix off raw preserves
	// the whole target verbatim.
	inner := strings.TrimPrefix(raw, "web:")
	if inner == "" {
		return "", errors.BadRequestf("web plugin: empty target in %q", raw)
	}

	iu, err := url.Parse(inner)
	if err != nil {
		return "", errors.BadRequestf("web plugin: invalid target URL %q: %v", inner, err)
	}
	if iu.Scheme != "http" && iu.Scheme != "https" {
		return "", errors.BadRequestf(
			"web plugin: target %q must be http(s), got scheme %q", inner, iu.Scheme,
		)
	}
	if iu.Host == "" {
		return "", errors.BadRequestf("web plugin: target %q has no host", inner)
	}
	return inner, nil
}

// captureFormat resolves the capture format from CUTTING_GARDEN_WEB_FORMAT,
// falling back to defaultFormat. Trailing/leading whitespace is trimmed so
// a stray newline in the environment doesn't desync the format catalog.
func captureFormat() string {
	if f := strings.TrimSpace(os.Getenv(webFormatEnv)); f != "" {
		return f
	}
	return defaultFormat
}
