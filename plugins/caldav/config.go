package caldav

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// AccountsConfig is the caldav plugin's section of the cutting-garden
// config (RFC 0007): a list of credentialed accounts. Each account's URL
// is a traversal root the mcp/list/health commands surface (Roots), and
// the plugin authenticates a node against the matching account's
// credentials (matchAccount, consulted by connectionFromArg). It is
// delegated into cgconfig.ConfigV0 as the `[caldav]` table, so accounts
// arrive as `[[caldav.accounts]]`.
//
//go:generate tommy generate
type AccountsConfig struct {
	Accounts []config_common.Account `toml:"accounts"`
}

// Validate enforces RFC 0007 § Plugin-Owned Sections: every account has a
// non-empty unique name, a non-empty caldav:// URL that parses and whose
// scheme this plugin claims, and the (host, path) pairs are distinct so
// credential resolution (matchAccount, step 2) never sees an ambiguous
// exact match. Differing path prefixes on one host are permitted —
// longest-prefix wins at resolution.
func (c AccountsConfig) Validate() error {
	seenName := make(map[string]struct{}, len(c.Accounts))
	seenHostPath := make(map[string]struct{}, len(c.Accounts))
	for i, acct := range c.Accounts {
		if acct.Name == "" {
			return errors.BadRequestf("caldav.accounts[%d]: empty name", i)
		}
		if _, dup := seenName[acct.Name]; dup {
			return errors.BadRequestf(
				"caldav.accounts: duplicate name %q", acct.Name,
			)
		}
		seenName[acct.Name] = struct{}{}

		if acct.URL == "" {
			return errors.BadRequestf(
				"caldav.accounts[%q]: empty url", acct.Name,
			)
		}
		u, err := url.Parse(acct.URL)
		if err != nil {
			return errors.BadRequestf(
				"caldav.accounts[%q]: unparseable url %q: %s",
				acct.Name, acct.URL, err,
			)
		}
		if u.Scheme != schemeCalDAV {
			return errors.BadRequestf(
				"caldav.accounts[%q]: url %q is not a %s:// URL",
				acct.Name, acct.URL, schemeCalDAV,
			)
		}
		host, path, err := accountHostPath(u)
		if err != nil {
			return errors.BadRequestf(
				"caldav.accounts[%q]: %s", acct.Name, err,
			)
		}
		key := host + "\x00" + path
		if _, dup := seenHostPath[key]; dup {
			return errors.BadRequestf(
				"caldav.accounts[%q]: another account already covers "+
					"host %q path %q", acct.Name, host, path,
			)
		}
		seenHostPath[key] = struct{}{}
	}
	return nil
}

// accountHostPath resolves a caldav account URL to the (host, path) pair
// credential resolution matches a node against. It runs the URL through
// baseURLFromArg (the same caldav→http(s) normalization ListRoots uses)
// then strips userinfo, so the comparison key is host + server path.
func accountHostPath(u *url.URL) (host, path string, err error) {
	base, err := baseURLFromArg(u)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", "", errors.Wrapf(err, "parse base %q", base)
	}
	return parsed.Host, parsed.Path, nil
}

// configuredAccounts holds the caldav accounts injected from the
// cutting-garden config at startup (RFC 0007 § Package Layering). It lives
// in package state — like the plugin registry — so Plugin stays a
// zero-size value rather than carrying per-instance config.
var configuredAccounts []config_common.Account

// SetConfiguredAccounts injects the caldav accounts parsed from the
// cutting-garden config. The composition root (cgapp) calls it once at
// startup, before any command resolves roots.
func SetConfiguredAccounts(accounts []config_common.Account) {
	configuredAccounts = accounts
}

var (
	_ cutting_garden_plugins.RootProvider = (*Plugin)(nil)
	_ cutting_garden_plugins.RootLabeler  = (*Plugin)(nil)
)

// Roots returns each configured account's endpoint URL as a
// credential-free traversal root (RFC 0007 § The Root-Provider
// Capability). With no configured accounts it returns an empty slice, so
// the caldav plugin contributes nothing to a no-argument listing.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		u, err := accountRootURL(acct)
		if err != nil {
			return nil, err
		}
		roots = append(roots, u)
	}
	return roots, nil
}

// accountRootURL parses acct's endpoint into the SAME credential-free
// *url.URL both Roots() and RootLabels() key off of, so RootLabels' map
// keys line up with Roots()' returned URLs by plain string equality.
func accountRootURL(acct config_common.Account) (*url.URL, error) {
	u, err := url.Parse(acct.URL)
	if err != nil {
		return nil, errors.Wrapf(err,
			"caldav account %q: parse url %q", acct.Name, acct.URL)
	}
	// Surfaced to clients (e.g. MCP resource URIs): never leak the
	// account's credentials through userinfo.
	u.User = nil
	return u, nil
}

// rootLabelCache memoizes each calendar-scoped account's resolved DAV
// displayname for the life of the process (cutting-garden#120): account
// configuration and a calendar's displayname are both effectively static
// once the process is running, root aggregation may run once per
// list_nodes/`list` call against a long-lived `mcp` server, and RootLabels'
// PROPFIND would otherwise be paid again on every such call. This is
// deliberately simpler than the RFC 0012 §11 facet/listing caches (no TTL,
// no change-token revalidation) — those are keyed by node URI and exist to
// keep a per-node summary fresh against live data; an account's own
// displayname changing requires an operator to notice and is not the kind
// of thing this cache needs to chase, so a process restart (which any
// `[[caldav.accounts]]` edit already needs) is an acceptable staleness
// bound. A PROPFIND FAILURE is deliberately NOT cached (see
// resolveAccountRootLabel), so a transient network blip retries on the
// next call rather than being stuck unlabeled for the process's life.
var (
	rootLabelCacheMu sync.Mutex
	rootLabelCacheM  = map[string]string{} // account name -> resolved label ("" = none)
)

// RootLabels resolves a friendlier display label for each configured
// account whose endpoint is itself a single calendar (a calendar-SCOPED
// account) — the remaining cutting-garden#120 surface after #162's
// home-level discovery: a calendar-HOME account's children already get
// their displayname from discoverCalendars during descent (ListRoots ->
// calendarNodes -> calendarLabel), but the home URL itself has no
// meaningful displayname of its own, so this method leaves it unlabeled
// (RootLabels omits the key, and the framework's default label derivation
// applies). Resolution reuses discoverCalendars — the SAME Depth:1
// PROPFIND/parse path ListRoots and capture already share — rather than a
// second PROPFIND/XML implementation.
//
// Non-fatal throughout (cutting-garden#165): any per-account failure
// (URL parse, connection resolution, PROPFIND) simply omits that
// account's key from the result rather than failing root aggregation, so
// the framework's default label derivation applies to that root instead.
func (Plugin) RootLabels(ctx context.Context) (map[string]string, error) {
	labels := make(map[string]string, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		label, ok := resolveAccountRootLabel(ctx, acct)
		if !ok {
			continue
		}
		u, err := accountRootURL(acct)
		if err != nil {
			continue
		}
		labels[u.String()] = label
	}
	return labels, nil
}

// resolveAccountRootLabel returns acct's cached (or freshly PROPFIND'd)
// DAV displayname. ok is false when the account is a calendar-home (no
// top-level label to give — its children are labeled during descent
// instead), the calendar has no displayname, or resolution failed for any
// reason (a bad account URL, a connection/credential error, or the
// PROPFIND itself failing) — every case degrades to "no override" rather
// than erroring (cutting-garden#165).
func resolveAccountRootLabel(
	ctx context.Context, acct config_common.Account,
) (string, bool) {
	rootLabelCacheMu.Lock()
	label, cached := rootLabelCacheM[acct.Name]
	rootLabelCacheMu.Unlock()
	if cached {
		return label, label != ""
	}

	u, err := url.Parse(acct.URL)
	if err != nil {
		return "", false
	}
	base, username, password, err := connectionFromArg(u)
	if err != nil {
		return "", false
	}
	c := newClient(base, username, password)
	selfIsCalendar, calendars, err := c.discoverCalendars(ctx)
	if err != nil {
		// Transient (network, server trouble): do NOT cache, so a later
		// call retries instead of being stuck unlabeled for the rest of
		// the process's life.
		return "", false
	}

	// A calendar-home account (selfIsCalendar == false), or a calendar the
	// server gave no displayname for, is a DEFINITIVE "no top-level label
	// to add" for the life of this process — cache the negative too, so
	// repeated calls don't re-PROPFIND for nothing.
	label = ""
	if selfIsCalendar && len(calendars) > 0 {
		label = calendars[0].displayName
	}
	rootLabelCacheMu.Lock()
	rootLabelCacheM[acct.Name] = label
	rootLabelCacheMu.Unlock()
	return label, label != ""
}

// matchAccount selects the configured account whose endpoint host equals
// host and whose endpoint path is the longest prefix of path — RFC 0007 §
// Credential Resolution step 2. ok is false when no account matches.
// Validate guarantees the (host, path) pairs are distinct, so the
// longest-prefix winner is unambiguous.
func matchAccount(host, path string) (config_common.Account, bool) {
	host = strings.ToLower(host)
	var best config_common.Account
	bestLen := -1
	for _, acct := range configuredAccounts {
		u, err := url.Parse(acct.URL)
		if err != nil {
			continue
		}
		ah, ap, err := accountHostPath(u)
		if err != nil {
			continue
		}
		if strings.ToLower(ah) != host || !strings.HasPrefix(path, ap) {
			continue
		}
		if len(ap) > bestLen {
			best, bestLen = acct, len(ap)
		}
	}
	return best, bestLen >= 0
}
