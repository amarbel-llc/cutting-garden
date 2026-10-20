package ynab

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// defaultAPIBase is YNAB's fixed REST API base. The `ynab://<account>/` URL's
// host slot names a CONFIG account, never a hostname.
const defaultAPIBase = "https://api.ynab.com/v1"

// init registers the `[ynab]` config table with the SDK's config-section
// registry (RFC 0007 § Plugin-Owned Sections), so cgconfig names no ynab type.
func init() {
	cutting_garden_plugins.MustRegisterConfigSection(schemeYNAB, decodeConfigSection)
}

// decodeConfigSection is the registered ConfigSectionDecoder: the shared
// tommy-generated config_common.DecodeAccountsSectionInto, this plugin's
// Validate, then the inject step.
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

// AccountsConfig is the ynab plugin's section of the cutting-garden config
// (RFC 0007): a list of personal-access-token accounts, arriving as
// `[[ynab.accounts]]`. Each account's URL is `ynab://<name>/`, whose host MUST
// equal the account name. The token resolves through the shared password
// fields (`password_env`, `password_source`/`password_key`); the secret never
// lives in the config file. An optional `session_url` replaces the API base
// (FDR 0027's in-memory testserver); for ynab it is a base URL such as
// `http://127.0.0.1:43114/v1`, not a session endpoint.
type AccountsConfig struct {
	Accounts []config_common.Account `toml:"accounts"`
}

// Validate enforces RFC 0007 § Plugin-Owned Sections for every account.
func (c AccountsConfig) Validate() error {
	seenName := make(map[string]struct{}, len(c.Accounts))
	for i, acct := range c.Accounts {
		if acct.Name == "" {
			return errors.BadRequestf("ynab.accounts[%d]: empty name", i)
		}
		if _, dup := seenName[acct.Name]; dup {
			return errors.BadRequestf("ynab.accounts: duplicate name %q", acct.Name)
		}
		seenName[acct.Name] = struct{}{}

		if acct.URL == "" {
			return errors.BadRequestf("ynab.accounts[%q]: empty url", acct.Name)
		}
		u, err := url.Parse(acct.URL)
		if err != nil {
			return errors.BadRequestf(
				"ynab.accounts[%q]: unparseable url %q: %s", acct.Name, acct.URL, err,
			)
		}
		if u.Scheme != schemeYNAB {
			return errors.BadRequestf(
				"ynab.accounts[%q]: url %q is not a %s:// URL",
				acct.Name, acct.URL, schemeYNAB,
			)
		}
		if u.Host != acct.Name {
			return errors.BadRequestf(
				"ynab.accounts[%q]: url host %q must equal the account name %q "+
					"(the host slot names the account, not a server)",
				acct.Name, u.Host, acct.Name,
			)
		}

		// The bearer token goes to session_url on every request, so plaintext
		// http is allowed only to a loopback host. The rule is config_common's,
		// shared with the fastmail plugin (cutting-garden#332).
		if err := acct.ValidateSessionURL(); err != nil {
			return errors.BadRequestf("ynab.accounts[%q]: %s", acct.Name, err)
		}

		if err := acct.ValidatePassword(); err != nil {
			return errors.BadRequestf("ynab.accounts[%q]: %s", acct.Name, err)
		}
	}
	return nil
}

// configuredAccounts holds the ynab accounts injected from the config at
// startup (RFC 0007 § Package Layering).
var configuredAccounts []config_common.Account

// SetConfiguredAccounts injects the ynab accounts parsed from the config.
func SetConfiguredAccounts(accounts []config_common.Account) {
	configuredAccounts = accounts
}

// Roots returns each configured account's credential-free root URI
// `ynab://<name>/`. With no configured accounts it returns an empty slice.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		roots = append(roots, accountRootURI(acct.Name))
	}
	return roots, nil
}

// RootLabels labels each account root with the account name (its display
// label: the account has no server-side name to fetch). Pure: no network.
func (Plugin) RootLabels(context.Context) (map[string]string, error) {
	return rootNames(), nil
}

// RootNames reports each configured account's `name` against its root URI
// (RFC 0020 §3.4). No I/O.
func (Plugin) RootNames(context.Context) (map[string]string, error) {
	return rootNames(), nil
}

// rootNames keys each configured account's name by its root URI. Both
// RootLabels and RootNames are this map: a ynab account's label IS its name.
func rootNames() map[string]string {
	names := make(map[string]string, len(configuredAccounts))
	for _, acct := range configuredAccounts {
		names[accountRootURI(acct.Name).String()] = acct.Name
	}
	return names
}

// accountByName returns the configured account whose Name equals name.
func accountByName(name string) (config_common.Account, bool) {
	for _, acct := range configuredAccounts {
		if acct.Name == name {
			return acct, true
		}
	}
	return config_common.Account{}, false
}

// apiBaseFor is the account's API base: its configured `session_url`
// override when set, else the fixed YNAB base.
func apiBaseFor(acct config_common.Account) string {
	if acct.SessionURL != "" {
		return acct.SessionURL
	}
	return defaultAPIBase
}
