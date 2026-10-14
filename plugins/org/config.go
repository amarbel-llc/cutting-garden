package org

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// The `[org]` config table (RFC 0007 § Plugin-Owned Sections):
//
//	[[org.accounts]]
//	name = "personal"
//	url = "https://myfiles.fastmail.com/Library/orgzly/"   # the WebDAV folder
//	username = "…"
//	password_source = "piggy"
//	password_key = "…"
//	todo_keywords = "TODO NEXT | DONE CANCELLED"           # D3; optional
//
// Unlike fastmail, url names the server: the WebDAV base folder (D2). The
// account's org:// root is org://<name>/.
func init() {
	cutting_garden_plugins.MustRegisterConfigSection(schemeOrg, decodeConfigSection)
}

func decodeConfigSection(sub *cst.Value) error {
	var section config_common.AccountsSection
	if err := config_common.DecodeAccountsSectionInto(&section, sub); err != nil {
		return err
	}
	accounts, err := validateAccounts(section.Accounts)
	if err != nil {
		return err
	}
	setConfiguredAccounts(accounts)
	return nil
}

// account is one validated org account.
type account struct {
	config_common.Account
	base     *url.URL // the WebDAV folder, path ending in "/"
	keywords orgtext.Keywords
}

func validateAccounts(raw []config_common.Account) ([]account, error) {
	seen := map[string]bool{}
	out := make([]account, 0, len(raw))
	for i, a := range raw {
		if a.Name == "" {
			return nil, errors.BadRequestf("org.accounts[%d]: empty name", i)
		}
		if seen[a.Name] {
			return nil, errors.BadRequestf("org.accounts: duplicate name %q", a.Name)
		}
		seen[a.Name] = true

		base, err := url.Parse(a.URL)
		if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return nil, errors.BadRequestf(
				"org.accounts[%q]: url %q is not an absolute http(s) WebDAV folder URL", a.Name, a.URL)
		}
		if base.Path == "" || base.Path[len(base.Path)-1] != '/' {
			base.Path += "/"
		}

		keywords := orgtext.DefaultKeywords
		if a.TodoKeywords != "" {
			if keywords, err = orgtext.ParseKeywords(a.TodoKeywords); err != nil {
				return nil, errors.BadRequestf("org.accounts[%q]: todo_keywords: %s", a.Name, err)
			}
		}

		if err := a.ValidatePassword(); err != nil {
			return nil, errors.BadRequestf("org.accounts[%q]: %s", a.Name, err)
		}
		out = append(out, account{Account: a, base: base, keywords: keywords})
	}
	return out, nil
}

// configuredAccounts is injected once at startup by the section decoder.
var configuredAccounts []account

func setConfiguredAccounts(accounts []account) {
	configuredAccounts = accounts
	resetCache()
}

func accountByName(name string) (account, bool) {
	for _, a := range configuredAccounts {
		if a.Name == name {
			return a, true
		}
	}
	return account{}, false
}

// Roots returns org://<name>/ for each configured account.
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	roots := make([]*url.URL, 0, len(configuredAccounts))
	for _, a := range configuredAccounts {
		roots = append(roots, folderURI(a.Name, ""))
	}
	return roots, nil
}

// RootLabels labels each root with its account name.
func (p Plugin) RootLabels(ctx context.Context) (map[string]string, error) {
	return p.RootNames(ctx)
}

// RootNames reports each account's configured name (RFC 0020 §3.4), so
// `!<name>` works as an organize origin. No I/O.
func (Plugin) RootNames(context.Context) (map[string]string, error) {
	names := make(map[string]string, len(configuredAccounts))
	for _, a := range configuredAccounts {
		names[folderURI(a.Name, "").String()] = a.Name
	}
	return names, nil
}

// allDoneKeywords is the union of the accounts' terminal keywords, the
// declared terminal values of the state facet. A file's own #+TODO done
// words are not declared up front.
func allDoneKeywords() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range configuredAccounts {
		for _, k := range a.keywords.Done {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, orgtext.DefaultKeywords.Done...)
	}
	return out
}
