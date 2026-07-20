package jira

import (
	"net/url"
	"os"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Environment variables consulted for credentials when the URL carries no
// userinfo and no configured account matches. Names match the `sisyphus`
// moxin so an existing Jira setup keeps working: the username is the
// Atlassian account email, the "password" is the API token.
const (
	envUsername = "JIRA_USERNAME"
	envToken    = "JIRA_API_TOKEN"
)

// baseURLFromArg resolves the http(s) node URL the plugin addresses from a
// parsed CLI argument. The node URL is the Jira REST origin plus an
// in-Jira path of `[/PROJECT[/ISSUE-KEY]]`. Two accepted forms mirror the
// caldav plugin's opaque/hierarchical split:
//
//   - jira://[user[:token]@]host[:port][/PROJECT[/ISSUE]]
//     → https://host[:port][/PROJECT[/ISSUE]] (the common form; TLS
//     assumed, matching Jira Cloud at *.atlassian.net).
//   - jira:<http(s)-url> → the inner URL verbatim (opaque form; the only
//     way to reach a plain-HTTP self-hosted instance, e.g. a LAN Jira at
//     jira:http://10.0.0.2:8080/PROJECT).
//
// The returned string may still carry userinfo; connectionFromArg splits
// credentials off. The in-Jira path is interpreted by nodeFromBase, which
// treats the URL path as project/issue relative to the origin — a Jira
// instance served under a context path (e.g. https://host/jira) is out of
// scope, matching the Jira Cloud target.
func baseURLFromArg(u *url.URL) (string, error) {
	if u.Scheme != schemeJira {
		return "", errors.ErrorWithStackf(
			"jira plugin: unsupported scheme %q in %q",
			u.Scheme, u.String(),
		)
	}

	// Opaque form: jira:<inner-url>. url.Parse splits ?query off the
	// opaque segment, so glue it back.
	if u.Opaque != "" {
		inner := u.Opaque
		if u.RawQuery != "" {
			inner += "?" + u.RawQuery
		}
		parsed, err := url.Parse(inner)
		if err != nil {
			return "", errors.Wrapf(err,
				"jira plugin: parse inner URL %q", inner)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", errors.ErrorWithStackf(
				"jira plugin: inner URL must be http or https, got %q in %q\n"+
					"hint: use `jira:http://host/PROJECT` or `jira:https://host/PROJECT`",
				parsed.Scheme, u.String(),
			)
		}
		return inner, nil
	}

	if u.Host == "" {
		return "", errors.ErrorWithStackf(
			"jira plugin: empty host in %q\n"+
				"hint: pass `jira://host/PROJECT` or `jira:<http-url>`",
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

// connectionFromArg resolves the base node URL and the credentials for a
// CLI argument. Credentials come from the URL's userinfo when present,
// otherwise from a configured account matched by host + longest project
// prefix, otherwise from JIRA_USERNAME / JIRA_API_TOKEN. The returned base
// has any userinfo stripped so it is safe to log.
func connectionFromArg(u *url.URL) (base, username, token string, err error) {
	base, err = baseURLFromArg(u)
	if err != nil {
		return "", "", "", err
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", "", "", errors.Wrapf(err, "jira plugin: parse base %q", base)
	}

	if parsed.User != nil {
		// Step 1: explicit URI userinfo wins (RFC 0007 § Credential
		// Resolution). Strip it from base so the connection target is
		// credential-free.
		username = parsed.User.Username()
		token, _ = parsed.User.Password()
		parsed.User = nil
		base = parsed.String()
	} else if acct, ok := matchAccount(parsed.Host, strings.TrimLeft(parsed.Path, "/")); ok {
		// Step 2: a configured account matching this node's host + longest
		// project-path prefix supplies the credentials; the token comes
		// from the account's PasswordEnv.
		username = acct.Username
		token = acct.Password()
	} else {
		// Step 3: fall back to the global environment.
		username = os.Getenv(envUsername)
		token = os.Getenv(envToken)
	}

	return base, username, token, nil
}

// originOf returns the scheme://host[:port] prefix of a URL string — the
// Jira REST API base. ok is false when the input does not parse as an
// absolute URL with a host.
func originOf(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

// nodeFromBase splits a node base URL into the Jira REST origin and the
// in-Jira address: project key (first path segment) and issue key (second
// path segment). An empty path is the root node (all projects); a one-
// segment path is a project; a two-segment path is a single issue. Any
// userinfo on base is ignored (connectionFromArg strips it first).
func nodeFromBase(base string) (origin, project, issue string, err error) {
	origin, ok := originOf(base)
	if !ok {
		return "", "", "", errors.ErrorWithStackf(
			"jira plugin: %q has no host", base,
		)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", "", "", errors.Wrapf(err, "jira plugin: parse %q", base)
	}
	segs := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for _, s := range segs {
		if s == "" {
			continue
		}
		switch {
		case project == "":
			project = s
		case issue == "":
			issue = s
		default:
			return "", "", "", errors.ErrorWithStackf(
				"jira plugin: %q has too many path segments; "+
					"expected jira://host/PROJECT[/ISSUE-KEY]", base,
			)
		}
	}
	return origin, project, issue, nil
}

// jiraURIForNode builds the `jira:` URI that re-resolves to a node at the
// given origin / project / issue — the inverse of baseURLFromArg, used by
// traversal so a listed node's URI re-classifies as a capture root.
//
//	https origin → jira://host/PROJECT[/ISSUE]
//	http  origin → jira:http://host/PROJECT[/ISSUE]  (opaque; reaches plain HTTP)
func jiraURIForNode(origin, project, issue string) *url.URL {
	path := ""
	if project != "" {
		path = "/" + project
		if issue != "" {
			path += "/" + issue
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		// Plain HTTP (or anything non-https) only round-trips through the
		// opaque form, which carries the inner scheme verbatim.
		return &url.URL{Scheme: schemeJira, Opaque: origin + path}
	}
	return &url.URL{Scheme: schemeJira, Host: parsed.Host, Path: path}
}
