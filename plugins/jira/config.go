package jira

import (
	"context"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// init registers the `[jira]` config table with the SDK's config-section
// registry (RFC 0007 § Plugin-Owned Sections): the framework's loader
// dispatches the table here by name, so cgconfig names no jira type.
func init() {
	cutting_garden_plugins.MustRegisterConfigSection(schemeJira, decodeConfigSection)
}

// decodeConfigSection is the registered ConfigSectionDecoder: the shared
// tommy-generated config_common.DecodeAccountsSectionInto (which marks
// consumption on the shared model), this plugin's Validate, then the
// inject step. The generated decoder is consumed from config_common rather
// than from this package because tommy blanks a package's own generated
// output while type-checking it (see config_common.AccountsSection).
func decodeConfigSection(sub *cst.Value) error {
	var section config_common.AccountsSection
	if err := config_common.DecodeAccountsSectionInto(&section, sub); err != nil {
		return err
	}
	c := AccountsConfig{Accounts: section.Accounts}
	if err := c.Validate(); err != nil {
		return err
	}
	SetConfiguredAccounts(c.Accounts)
	return nil
}

// AccountsConfig is the jira plugin's section of the cutting-garden config
// (RFC 0007): a list of credentialed accounts. Each account's URL is a
// traversal root the mcp/list commands surface (Roots), and the plugin
// authenticates a node against the matching account's credentials
// (matchAccount, consulted by connectionFromArg). It is registered with
// the SDK's config-section registry as the `[jira]` table (init below), so
// accounts arrive as `[[jira.accounts]]`. An account's `username` is the Atlassian account
// email and its `password_env` names the env var holding the API token.
// The TOML codec is config_common.AccountsSection's (see
// decodeConfigSection); this type carries the jira-specific validation.
type AccountsConfig struct {
	Accounts []config_common.Account `toml:"accounts"`
}

// Validate enforces RFC 0007 § Plugin-Owned Sections: every account has a
// non-empty unique name, a non-empty jira:// URL that parses and whose
// scheme this plugin claims, and the (host, project-path) pairs are
// distinct so credential resolution (matchAccount, step 2) never sees an
// ambiguous exact match. Differing project prefixes on one host are
// permitted — longest-prefix wins at resolution. decodeConfigSection
// invokes it after decoding.
func (c AccountsConfig) Validate() error {
	seenName := make(map[string]struct{}, len(c.Accounts))
	seenHostPath := make(map[string]struct{}, len(c.Accounts))
	for i, acct := range c.Accounts {
		if acct.Name == "" {
			return errors.BadRequestf("jira.accounts[%d]: empty name", i)
		}
		if _, dup := seenName[acct.Name]; dup {
			return errors.BadRequestf(
				"jira.accounts: duplicate name %q", acct.Name,
			)
		}
		seenName[acct.Name] = struct{}{}

		if acct.URL == "" {
			return errors.BadRequestf(
				"jira.accounts[%q]: empty url", acct.Name,
			)
		}
		u, err := url.Parse(acct.URL)
		if err != nil {
			return errors.BadRequestf(
				"jira.accounts[%q]: unparseable url %q: %s",
				acct.Name, acct.URL, err,
			)
		}
		if u.Scheme != schemeJira {
			return errors.BadRequestf(
				"jira.accounts[%q]: url %q is not a %s:// URL",
				acct.Name, acct.URL, schemeJira,
			)
		}
		host, path, err := accountHostPath(u)
		if err != nil {
			return errors.BadRequestf(
				"jira.accounts[%q]: %s", acct.Name, err,
			)
		}
		key := host + "\x00" + path
		if _, dup := seenHostPath[key]; dup {
			return errors.BadRequestf(
				"jira.accounts[%q]: another account already covers "+
					"host %q path %q", acct.Name, host, path,
			)
		}
		seenHostPath[key] = struct{}{}
	}
	return nil
}

// accountHostPath resolves a jira account URL to the (host, project-path)
// pair credential resolution matches a node against. It runs the URL
// through baseURLFromArg (the same jira→http(s) normalization Roots uses)
// then strips userinfo, so the comparison key is host + leading-slash-
// trimmed path.
func accountHostPath(u *url.URL) (host, path string, err error) {
	base, err := baseURLFromArg(u)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", "", errors.Wrapf(err, "parse base %q", base)
	}
	return parsed.Host, strings.TrimLeft(parsed.Path, "/"), nil
}

// configuredAccounts holds the jira accounts injected from the
// cutting-garden config at startup (RFC 0007 § Package Layering). It lives
// in package state — like the plugin registry — so Plugin stays a
// zero-size value rather than carrying per-instance config.
var configuredAccounts []config_common.Account

// SetConfiguredAccounts injects the jira accounts parsed from the
// cutting-garden config. The registered section decoder
// (decodeConfigSection) calls it once at startup, before any command
// resolves roots.
func SetConfiguredAccounts(accounts []config_common.Account) {
	configuredAccounts = accounts
}

var _ cutting_garden_plugins.RootProvider = (*Plugin)(nil)

// Roots returns each configured account's endpoint URL as a credential-
// free traversal root (RFC 0007 § The Root-Provider Capability). With no
// configured accounts it returns an empty slice, so the jira plugin
// contributes nothing to a no-argument listing.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		u, err := url.Parse(acct.URL)
		if err != nil {
			return nil, errors.Wrapf(err,
				"jira account %q: parse url %q", acct.Name, acct.URL)
		}
		// Surfaced to clients (e.g. MCP resource URIs): never leak the
		// account's credentials through userinfo.
		u.User = nil
		roots = append(roots, u)
	}
	return roots, nil
}

// matchAccount selects the configured account whose endpoint host equals
// host and whose project-path is the longest prefix of path — RFC 0007 §
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
