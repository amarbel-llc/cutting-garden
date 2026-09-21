package fastmail

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// init registers the `[fastmail]` config table with the SDK's config-section
// registry (RFC 0007 § Plugin-Owned Sections): the framework's loader
// dispatches the table here by name, so cgconfig names no fastmail type.
func init() {
	cutting_garden_plugins.MustRegisterConfigSection(schemeFastmail, decodeConfigSection)
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

// AccountsConfig is the fastmail plugin's section of the cutting-garden
// config (RFC 0007): a list of bearer-token accounts. Each account's URL is
// `fastmail://<name>/` — the host slot names the account, NOT a hostname
// (the JMAP API host is fixed), so the URL's host MUST equal the account
// name. The bearer token is resolved from the account's PasswordEnv (e.g.
// `FASTMAIL_API_TOKEN`); the secret itself never lives in the config file.
// Registered with the SDK's config-section registry as the `[fastmail]`
// table (init below), so accounts arrive as `[[fastmail.accounts]]`. The
// TOML codec is config_common.AccountsSection's (see decodeConfigSection);
// this type carries the fastmail-specific validation.
type AccountsConfig struct {
	Accounts []config_common.Account `toml:"accounts"`
}

// Validate enforces RFC 0007 § Plugin-Owned Sections: every account has a
// non-empty unique name and a `fastmail://<name>/` URL that parses, whose
// scheme this plugin claims, and whose host equals the account name — so
// `fastmail://<name>/` classifies to that account (classifyURI keys on the
// host slot). decodeConfigSection invokes it after decoding.
func (c AccountsConfig) Validate() error {
	seenName := make(map[string]struct{}, len(c.Accounts))
	for i, acct := range c.Accounts {
		if acct.Name == "" {
			return errors.BadRequestf("fastmail.accounts[%d]: empty name", i)
		}
		if _, dup := seenName[acct.Name]; dup {
			return errors.BadRequestf(
				"fastmail.accounts: duplicate name %q", acct.Name,
			)
		}
		seenName[acct.Name] = struct{}{}

		if acct.URL == "" {
			return errors.BadRequestf(
				"fastmail.accounts[%q]: empty url", acct.Name,
			)
		}
		u, err := url.Parse(acct.URL)
		if err != nil {
			return errors.BadRequestf(
				"fastmail.accounts[%q]: unparseable url %q: %s",
				acct.Name, acct.URL, err,
			)
		}
		if u.Scheme != schemeFastmail {
			return errors.BadRequestf(
				"fastmail.accounts[%q]: url %q is not a %s:// URL",
				acct.Name, acct.URL, schemeFastmail,
			)
		}
		if u.Host != acct.Name {
			return errors.BadRequestf(
				"fastmail.accounts[%q]: url host %q must equal the account name %q "+
					"(the host slot names the account, not a server)",
				acct.Name, u.Host, acct.Name,
			)
		}
	}
	return nil
}

// configuredAccounts holds the fastmail accounts injected from the
// cutting-garden config at startup (RFC 0007 § Package Layering). Package
// state — like the plugin registry — so Plugin stays a zero-size value.
var configuredAccounts []config_common.Account

// SetConfiguredAccounts injects the fastmail accounts parsed from the
// cutting-garden config. The registered section decoder
// (decodeConfigSection) calls it once at startup, before any command
// resolves roots.
func SetConfiguredAccounts(accounts []config_common.Account) {
	configuredAccounts = accounts
}

// Roots returns each configured account's credential-free root URI
// `fastmail://<name>/` (RFC 0007 § The Root-Provider Capability). With no
// configured accounts it returns an empty slice, so the plugin contributes
// nothing to a no-argument listing.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		roots = append(roots, accountRootURI(acct.Name))
	}
	return roots, nil
}

// RootLabels labels each account root with the account name — the account's
// display name (RFC 0007). Pure and non-fatal: no network, and it simply
// keys the account's root URI to its name.
func (Plugin) RootLabels(context.Context) (map[string]string, error) {
	labels := make(map[string]string, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		labels[accountRootURI(acct.Name).String()] = acct.Name
	}
	return labels, nil
}

// accountByName returns the configured account whose Name equals name.
// Validate guarantees names are unique, so the first match is definitive;
// ok is false when no account carries that name.
func accountByName(name string) (config_common.Account, bool) {
	for _, acct := range configuredAccounts {
		if acct.Name == name {
			return acct, true
		}
	}
	return config_common.Account{}, false
}

// resolveClient builds a JMAP client for a classified node: it resolves the
// node's account name to its bearer token and session endpoint. An unknown
// account is a bad request (a node URI naming an account that is not
// configured).
func resolveClient(ref nodeRef) (*client, error) {
	acct, ok := accountByName(ref.account)
	if !ok {
		return nil, errors.BadRequestf(
			"fastmail plugin: unknown account %q", ref.account,
		)
	}
	return newClient(resolveSessionURL(acct.Name), acct.Password()), nil
}
