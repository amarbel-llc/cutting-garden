package capture_wire

import (
	"net/url"
	"os"
	"slices"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// captureTarget extracts the http(s) URL to navigate to from a
// "<scheme>:<url>" argument, where scheme is one of p.spec.Schemes.
// Relocates plugins/web's captureTarget (cutting-garden#146 slice 2
// phase 2), generalized from the hardcoded "web" scheme to whichever
// scheme(s) the stanza claims. The canonical (and only accepted) form
// is the opaque one — "web:https://example.com" — so url.Parse yields
// Scheme="web", Opaque="https://example.com"; the inner URL must
// itself be http or https. Structural only; performs no network
// access.
func (p *Plugin) captureTarget(u *url.URL, raw string) (string, error) {
	if u == nil {
		return "", errors.BadRequestf("plugin %q: nil source for %q", p.spec.Name, raw)
	}
	if !slices.Contains(p.spec.Schemes, u.Scheme) {
		return "", errors.BadRequestf(
			"plugin %q: unsupported scheme %q (want one of %v)",
			p.spec.Name, u.Scheme, p.spec.Schemes,
		)
	}

	// Reject the authority form `<scheme>://host/...`: url.Parse treats
	// the host as the outer scheme's own authority and drops the inner
	// scheme, so http vs https is ambiguous. Point the user at the
	// opaque form, which carries the inner URL — scheme included —
	// verbatim.
	if u.Opaque == "" && u.Host != "" {
		return "", errors.BadRequestf(
			"plugin %q: use the opaque form %s:<http(s)-url> "+
				"(e.g. %s:https://%s%s), not %s://<host>",
			p.spec.Name, u.Scheme, u.Scheme, u.Host, u.Path, u.Scheme,
		)
	}

	// Derive the inner URL from raw rather than u.Opaque: the outer
	// parse peels a `?query`/`#fragment` off into u.RawQuery/
	// u.Fragment, so u.Opaque drops them. Stripping the `<scheme>:`
	// prefix off raw preserves the whole target verbatim.
	inner := strings.TrimPrefix(raw, u.Scheme+":")
	if inner == "" {
		return "", errors.BadRequestf("plugin %q: empty target in %q", p.spec.Name, raw)
	}

	iu, err := url.Parse(inner)
	if err != nil {
		return "", errors.BadRequestf(
			"plugin %q: invalid target URL %q: %v", p.spec.Name, inner, err,
		)
	}
	if iu.Scheme != "http" && iu.Scheme != "https" {
		return "", errors.BadRequestf(
			"plugin %q: target %q must be http(s), got scheme %q",
			p.spec.Name, inner, iu.Scheme,
		)
	}
	if iu.Host == "" {
		return "", errors.BadRequestf("plugin %q: target %q has no host", p.spec.Name, inner)
	}
	return inner, nil
}

// formatEnvVar names the CUTTING_GARDEN_<NAME>_FORMAT override this
// plugin's captures read, derived from the stanza name so multiple
// configured capture plugins don't collide on one shared variable —
// plugins/web's single hardcoded CUTTING_GARDEN_WEB_FORMAT,
// generalized. traversal_serve.PluginStanza.Validate already pins
// Name to the bare-TOML-key grammar ([A-Za-z0-9_-]+), so only "-" needs
// folding to "_" here.
func (p *Plugin) formatEnvVar() string {
	upper := strings.ToUpper(p.spec.Name)
	upper = strings.ReplaceAll(upper, "-", "_")
	return "CUTTING_GARDEN_" + upper + "_FORMAT"
}

// captureFormat resolves the capture format from this plugin's env
// override, falling back to defaultFormat. Trailing/leading whitespace
// is trimmed so a stray newline in the environment doesn't desync the
// format catalog.
func (p *Plugin) captureFormat() string {
	if f := strings.TrimSpace(os.Getenv(p.formatEnvVar())); f != "" {
		return f
	}
	return defaultFormat
}
