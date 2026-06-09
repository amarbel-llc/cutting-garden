package cutting_garden_plugin_caldav

import (
	"net/url"
	"os"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Environment variables consulted for credentials when the URL carries
// no userinfo. Names match bob's caldav package so existing setups keep
// working.
const (
	envUsername = "CALDAV_USERNAME"
	envPassword = "CALDAV_PASSWORD"
)

// baseURLFromArg resolves the http(s) collection URL the plugin will
// PROPFIND/REPORT against from a parsed CLI argument. Two accepted
// forms mirror the yt-dlp plugin's opaque/hierarchical split:
//
//   - caldav://[user@]host[:port]/path  → https://host[:port]/path
//     (the common form; TLS is assumed).
//   - caldav:<http(s)-url>              → the inner URL verbatim
//     (opaque form; the only way to reach a plain-HTTP server, e.g. a
//     LAN Radicale at caldav:http://10.0.0.2:5232/).
//
// The returned string may still carry userinfo; connectionFromArg
// splits credentials off.
func baseURLFromArg(u *url.URL) (string, error) {
	if u.Scheme != schemeCalDAV {
		return "", errors.ErrorWithStackf(
			"caldav plugin: unsupported scheme %q in %q",
			u.Scheme, u.String(),
		)
	}

	// Opaque form: caldav:<inner-url>. url.Parse splits ?query off the
	// opaque segment, so glue it back.
	if u.Opaque != "" {
		inner := u.Opaque
		if u.RawQuery != "" {
			inner += "?" + u.RawQuery
		}
		parsed, err := url.Parse(inner)
		if err != nil {
			return "", errors.Wrapf(err,
				"caldav plugin: parse inner URL %q", inner)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", errors.ErrorWithStackf(
				"caldav plugin: inner URL must be http or https, got %q in %q\n"+
					"hint: use `caldav:http://host/path` or `caldav:https://host/path`",
				parsed.Scheme, u.String(),
			)
		}
		return inner, nil
	}

	if u.Host == "" {
		return "", errors.ErrorWithStackf(
			"caldav plugin: empty host in %q\n"+
				"hint: pass `caldav://host/path` or `caldav:<http-url>`",
			u.String(),
		)
	}

	rebuilt := &url.URL{
		Scheme:   "https",
		User:     u.User,
		Host:     u.Host,
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}
	return rebuilt.String(), nil
}

// connectionFromArg resolves the base URL and the credentials for a CLI
// argument. Credentials come from the URL's userinfo when present,
// otherwise from CALDAV_USERNAME / CALDAV_PASSWORD. The returned base
// has any userinfo stripped so it is safe to log.
func connectionFromArg(u *url.URL) (base, username, password string, err error) {
	base, err = baseURLFromArg(u)
	if err != nil {
		return "", "", "", err
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", "", "", errors.Wrapf(err, "caldav plugin: parse base %q", base)
	}

	if parsed.User != nil {
		// Step 1: explicit URI userinfo wins (RFC 0007 § Credential
		// Resolution). Strip it from base so the connection target is
		// credential-free.
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
		parsed.User = nil
		base = parsed.String()
	} else if acct, ok := matchAccount(parsed.Host, parsed.Path); ok {
		// Step 2: a configured account matching this node's host + longest
		// path prefix supplies the credentials; the password comes from the
		// account's PasswordEnv.
		username = acct.Username
		password = acct.Password()
	} else {
		// Step 3: fall back to the global environment (today's behavior).
		username = os.Getenv(envUsername)
		password = os.Getenv(envPassword)
	}

	return base, username, password, nil
}

// originOf returns the scheme://host[:port] prefix of a URL string.
// ok is false when the input does not parse as an absolute URL with a
// host.
func originOf(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

// serverPath returns the leading-slash-stripped path of an absolute
// resource URL — the stable, host-independent key stored as
// EntryV1.Path. e.g. https://h/dav/u/cal/a.ics → "dav/u/cal/a.ics".
func serverPath(absURL string) string {
	parsed, err := url.Parse(absURL)
	if err != nil {
		return strings.TrimLeft(absURL, "/")
	}
	return strings.TrimLeft(parsed.EscapedPath(), "/")
}
