package org

import (
	"context"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // timezone resolves even on a host without a zoneinfo database

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
//	timezone = "America/New_York"                          # optional
//	tag_files = ["_tags.org"]                              # tag vocabularies; optional
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
	loc      *time.Location // for the timestamps the plugin writes
	tagFiles map[string]bool
}

// isTagFile reports whether rel (a path relative to the account's folder) is
// one of the account's tag-vocabulary files.
func (a account) isTagFile(rel string) bool { return a.tagFiles[rel] }

// validateTagFiles checks an account's tag_files and returns them as a set:
// each entry is a clean relative path to an .org file (no leading `/`, no `..`
// or empty segment, no trailing `/`), matched exactly against a file's path
// under the account's folder.
func validateTagFiles(files []string) (map[string]bool, error) {
	set := make(map[string]bool, len(files))
	for _, f := range files {
		switch {
		case f == "":
			return nil, errors.BadRequestf("tag_files: empty entry")
		case strings.HasPrefix(f, "/") || strings.HasSuffix(f, "/"):
			return nil, errors.BadRequestf(
				"tag_files: %q must be a file path relative to the account folder (no leading or trailing /)", f)
		case !strings.HasSuffix(f, ".org"):
			return nil, errors.BadRequestf("tag_files: %q is not an .org file", f)
		}
		for _, seg := range strings.Split(f, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return nil, errors.BadRequestf("tag_files: %q has an empty, `.` or `..` path segment", f)
			}
		}
		set[f] = true
	}
	return set, nil
}

// now is the current time in the account's zone.
func (a account) now() time.Time {
	if a.loc == nil {
		return time.Now()
	}
	return time.Now().In(a.loc)
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

		loc := time.Local
		if a.Timezone != "" {
			if loc, err = time.LoadLocation(a.Timezone); err != nil {
				return nil, errors.BadRequestf("org.accounts[%q]: timezone %q: %s", a.Name, a.Timezone, err)
			}
		}

		if err := a.ValidatePassword(); err != nil {
			return nil, errors.BadRequestf("org.accounts[%q]: %s", a.Name, err)
		}
		tagFiles, err := validateTagFiles(a.TagFiles)
		if err != nil {
			return nil, errors.BadRequestf("org.accounts[%q]: %s", a.Name, err)
		}
		out = append(out, account{Account: a, base: base, keywords: keywords, loc: loc, tagFiles: tagFiles})
	}
	return out, nil
}

// configuredAccounts is injected once at startup by the section decoder.
var configuredAccounts []account

func setConfiguredAccounts(accounts []account) {
	configuredAccounts = accounts
	resetCache()
	resetPasswords()
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
	return accountKeywords(func(k orgtext.Keywords) []string { return k.Done })
}

// allKeywords is the union of the accounts' keywords, active then done:
// the state buckets organize pre-renders as move targets.
func allKeywords() []string {
	return accountKeywords(func(k orgtext.Keywords) []string {
		return append(append([]string(nil), k.Active...), k.Done...)
	})
}

func accountKeywords(pick func(orgtext.Keywords) []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range configuredAccounts {
		for _, k := range pick(a.keywords) {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, pick(orgtext.DefaultKeywords)...)
	}
	return out
}
