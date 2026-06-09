package cutting_garden_plugin_caldav

import (
	"context"
	"net/url"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/config_common"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
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

var _ cutting_garden_plugins.RootProvider = (*Plugin)(nil)

// Roots returns each configured account's endpoint URL as a
// credential-free traversal root (RFC 0007 § The Root-Provider
// Capability). With no configured accounts it returns an empty slice, so
// the caldav plugin contributes nothing to a no-argument listing.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		u, err := url.Parse(acct.URL)
		if err != nil {
			return nil, errors.Wrapf(err,
				"caldav account %q: parse url %q", acct.Name, acct.URL)
		}
		// Surfaced to clients (e.g. MCP resource URIs): never leak the
		// account's credentials through userinfo.
		u.User = nil
		roots = append(roots, u)
	}
	return roots, nil
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
