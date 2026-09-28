// Package config_common holds the shared, plugin-neutral configuration
// types for cutting-garden's config subsystem (RFC 0007): a Root (a
// plugin entry point with no credentials) and an Account (a credentialed
// root). Plugin packages embed these in their own config sections.
//
// This package imports neither internal/cgconfig nor any plugin package:
// it is the leaf every account-bearing plugin's config-section decoder
// builds on (AccountsSection), while cgconfig holds only the framework
// sections and imports no plugin (RFC 0007 § Package Layering).
package config_common

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Root is a plugin entry point with no credentials — a "preferred root"
// for plugins that cannot enumerate roots from ambient state (e.g. a
// web/yt-dlp plugin). Name is a label unique within its plugin section;
// URL is the endpoint in the form the plugin accepts on the command line
// (e.g. "caldav://dav.host/dav/me/").
//
// Root carries no tommy codegen directive of its own: it is consumed
// today only as Account's embedded base, and tommy promotes an embedded
// struct's tagged fields directly into the embedder's generated code. A
// preferred-roots plugin that delegates to a bare []Root will add the
// `//go:generate tommy generate` directive to Root then.
type Root struct {
	Name string `toml:"name"`
	URL  string `toml:"url"`
}

// Account is a credentialed root (a Root plus credential indirection) for
// plugins whose roots require authentication (caldav, and the planned
// sftp/webdav/github). The password comes from exactly one of:
//   - PasswordEnv (legacy): the name of an environment variable.
//   - PasswordSource + PasswordKey: PasswordSource selects the backend
//     ("env" resolves PasswordKey as an environment variable name — the
//     new spelling of PasswordEnv; "piggy" resolves PasswordKey as a
//     `piggy pass show` entry name). ValidatePassword enforces mutual
//     exclusivity and the PasswordSource enum; see that method.
//
// The secret itself is never stored in the config file (RFC 0007 §
// Security Considerations).
//
// SessionURL overrides the endpoint a plugin with a FIXED API host would
// otherwise hard-code — today only the fastmail plugin's JMAP Session URL
// (default https://api.fastmail.com/jmap/session), which is how the bats
// lane points a `fastmail://<name>/` account at its in-memory test server.
// Plugins whose URL already names the server (caldav, jira) ignore it.
//
//go:generate tommy generate
type Account struct {
	Root
	Username       string `toml:"username,omitempty"`
	PasswordEnv    string `toml:"password_env,omitempty"`
	PasswordSource string `toml:"password_source,omitempty"`
	PasswordKey    string `toml:"password_key,omitempty"`
	SessionURL     string `toml:"session_url,omitempty"`
}

// AccountsSection is the TOML shape of an account-bearing plugin's
// top-level config table (RFC 0007 § Plugin-Owned Sections): the
// `[[<scheme>.accounts]]` array every such plugin (caldav, fastmail, jira)
// decodes. It is the ONE tommy schema the plugins share — a plugin's
// registered config-section decoder (cutting_garden_plugins.
// MustRegisterConfigSection) runs DecodeAccountsSectionInto over its table,
// then applies its own scheme-specific validation and injects the accounts.
//
// The schema lives here rather than in each plugin because tommy
// type-checks a package with its own generated output blanked (its codegen
// bootstrap, tommy#93): hand-written code in the plugin package can never
// call a Decode<X>Into generated in that same package, so the generated
// entrypoint a plugin consumes must be defined in a leaf it imports. This
// package is that leaf.
//
//go:generate tommy generate
type AccountsSection struct {
	Accounts []Account `toml:"accounts"`
}

// ValidatePassword enforces mutual exclusivity between the legacy
// PasswordEnv and the PasswordSource/PasswordKey pair, and validates the
// PasswordSource enum. A plugin's own AccountsConfig.Validate calls this
// from its per-account loop and wraps the error with its own
// "<scheme>.accounts[%q]: %s" context (acct.Name is already known to be
// non-empty by the time that loop reaches this check).
func (a Account) ValidatePassword() error {
	if a.PasswordEnv != "" && (a.PasswordSource != "" || a.PasswordKey != "") {
		return fmt.Errorf(
			"password_env is mutually exclusive with password_source/password_key",
		)
	}
	if (a.PasswordSource == "") != (a.PasswordKey == "") {
		return fmt.Errorf(
			"password_source and password_key must be set together",
		)
	}
	switch a.PasswordSource {
	case "", "env", "piggy":
		// ok
	default:
		return fmt.Errorf(
			"unknown password_source %q (want \"env\" or \"piggy\")",
			a.PasswordSource,
		)
	}
	return nil
}

// Password resolves the account's password: PasswordEnv (legacy) or
// PasswordSource ("env": PasswordKey as an env var name; "piggy":
// PasswordKey as a `piggy pass show` entry name). Returns "", nil when no
// password field is set. A "piggy" source's missing binary or failed
// decrypt is a hard error — unlike an unset env var, which silently
// resolves to "" (ValidatePassword already guarantees PasswordSource is
// one of "", "env", "piggy" and that PasswordSource/PasswordKey are set
// together, so the switch below has no further default case to reach).
func (a Account) Password() (string, error) {
	switch {
	case a.PasswordEnv != "":
		return os.Getenv(a.PasswordEnv), nil
	case a.PasswordSource == "env":
		return os.Getenv(a.PasswordKey), nil
	case a.PasswordSource == "piggy":
		return piggyPassShow(a.PasswordKey)
	default:
		return "", nil
	}
}

// piggyStderrTailBytes caps how much of `piggy pass show`'s stderr is
// buffered for the failure diagnostic — same bound and rationale as
// plugins/optical and plugins/googlephotos' stderr tails.
const piggyStderrTailBytes = 4096

// piggyPassShow decrypts and returns the named piggy pass entry via
// `piggy pass show <key>`, trimming the trailing newline `pass show`
// writes. The binary is resolved through exec.LookPath, honoring the
// caller's PATH (the same convention as plugins/optical, plugins/ytdlp,
// plugins/googlephotos). No context.Context threading in v1 — a v2
// revision threads interfaces.ActiveContext through for both a single
// `pass show` and a batched `pass show-batch` session (out of scope here).
func piggyPassShow(key string) (string, error) {
	binPath, err := exec.LookPath("piggy")
	if err != nil {
		return "", fmt.Errorf(
			"config_common: piggy not found on PATH (%w)\n"+
				"hint: enter the devshell or run a nix-built binary",
			err,
		)
	}

	cmd := exec.Command(binPath, "pass", "show", key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		tail := stderr.Bytes()
		if len(tail) > piggyStderrTailBytes {
			tail = tail[len(tail)-piggyStderrTailBytes:]
		}
		return "", fmt.Errorf(
			"config_common: piggy pass show %q failed (%w)\nstderr-tail: %s",
			key, runErr, tail,
		)
	}

	return strings.TrimRight(stdout.String(), "\n"), nil
}
