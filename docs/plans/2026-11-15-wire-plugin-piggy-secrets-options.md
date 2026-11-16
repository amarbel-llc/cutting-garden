# Options: piggy-sourced secrets for out-of-process traversal plugins

Date: 2026-11-15
Issue: cutting-garden#285 (milestone "traversal wire and conformance")
Status: ruled 2026-11-16 (see Ruling). Nothing here is implemented.

## Problem

`config_common.Account` resolves a password from `password_env`, or from
`password_source = "env" | "piggy"` plus `password_key`
(`docs/plans/2026-09-28-piggy-pass-password-source-design.md`), for the
in-process caldav, fastmail and jira plugins. An out-of-process traversal
plugin (RFC 0013) gets only its own wrapper-stripped `config_toml` in
`initialize`, and RFC 0013 says "no credential material crosses the wire":
the plugin resolves indirections itself from its inherited environment.
The first consumer that cannot reach a piggy entry is smith's
`serve-cutting_garden`, which reads only its own keystore
(smith-keystore(5)).

Fact that shapes the options: the out-of-tree plugins named so far are not
all Go (`fj-cg` is Rust, cutting-garden#140; smith's source is Rust). A Go
package cannot be imported by them.

## Options

### (a) Convention: the plugin accepts the same fields and shells out

RFC 0013 and cutting-garden-config(5) state that a plugin's section SHOULD
accept `password_source` / `password_key` with the config_common semantics
(`piggy pass show <key>`, trailing newline trimmed, missing binary or
non-zero exit is a hard error, no empty fallback), and that
`config_toml` carries the key NAME only.

- Wire and RFC 0013's current rule ("no credential on the wire") are
  unchanged. No host change.
- Works for any language: the contract is two field names and one command.
- Each plugin reimplements about 20 lines, and can drift from config_common
  (error text, trimming, no-fallback rule). A conformance case
  (a stub `piggy` on PATH) can pin the behavior, but the driver only checks
  what a peer chooses to expose through config.
- The plugin inherits the host's environment (RFC 0013 already says so), so
  `piggy-agent` access and the PIN-prompt story are the same as in-process.
  Several plugins resolving separately means one PIN prompt each when the
  agent is not running (the cost design note 2026-09-28 already accepts).

### (b) The host resolves and passes the secret in the handshake

Add a resolved-credentials member to `initialize`, or an environment
variable on the spawned process.

- Plugins need no piggy dependency; one resolution point, so the v2 batch
  (`piggy pass show-batch`) in the 2026-09-28 note becomes possible for
  wire plugins too.
- Reverses an explicit RFC 0013 rule (Security Considerations: a host MUST
  NOT inject resolved secrets into `config_toml`; `initialize` text: no
  credential material anywhere on the wire). That is a protocol change,
  needing a version bump or a capability flag.
- Exposure surface, none of which I have verified in this repo: whether the
  host or a plugin logs `initialize` params, whether a wire trace or
  conformance transcript records them, and, for the env-var form, that
  `/proc/<pid>/environ` is readable to the same user (the RFC already puts
  same-user processes inside the trust boundary, so this adds little over
  (a), where the plugin runs `piggy` itself). The argv form must be
  excluded: argv is visible to other processes in `ps`.
- The host decrypts for plugins it spawns lazily even if they never use the
  secret, unless resolution is gated on the plugin declaring a need.

### (c) Extract the resolver as a shared importable package

`pkgs/config_common` already ships `Account.Password()` to Go consumers
through dagnabit copy mode (RFC 0009 section 5), so for a Go plugin this
already exists and needs only documentation.

- Helps Go plugins only; smith and `fj-cg` are Rust, so (c) alone does not
  answer the issue's motivating case.
- Valid as an addition to (a): Go plugins import the resolver, other
  languages follow the written convention.

## Recommendation

(a), with (c) named in the docs as the Go shortcut, and (b) deferred.

Reasons: it keeps RFC 0013's no-credentials-on-the-wire rule and needs no
protocol bump; it serves the Rust plugins that motivate the issue; and the
only thing (b) buys (a single batched PIN session) is already a deferred v2
item that can be revisited when it is built. If the operator wants the
batch, (b) should be a separate RFC 0013 revision with an explicit
capability flag, not folded into this change.

If (a) is ruled, the follow-up (a docs-only merge) is: RFC 0013
`initialize` / Security Considerations gain the convention (field names,
semantics, hard-error rule, key name only on the wire); RFC 0007 gains a
cross-reference; cutting-garden-config(5) documents the fields for wire
sections; and the smith side is tracked on the smith repo, not here.

## Open question for the operator

Is a host allowed to log `initialize` params at all? Under (a) this does not
matter (only a key name is present); under (b) it decides whether the
handshake can carry a secret safely. I have not checked the host's logging;
that is a verification step before (b) is considered.

Under the ruling below this question is moot: no credential or key name
reaches a plugin, so nothing in `initialize` is sensitive.

## Ruling (2026-11-16)

**(d) piggy as a local auth decoration proxy.** None of (a), (b) or (c).
piggy (the operator also named it piggy/fibby) decorates a plugin's outbound
requests with the credential, so a traversal plugin, in-process or wire, in
any language, holds no credential and resolves no indirection. Its config
names the proxy, not a password.

Reason: (a) unblocks nothing immediately (the real unblock is smith-side
work either way), and the tent work already needs the same proxy, so the
marginal cost here is a route entry, not a subsystem. It also keeps RFC
0013's no-credentials-on-the-wire rule trivially, and gives the one
batched PIN session (b) was for.

Deliberately not specified here: how the proxy is addressed, how routes map
to entries, TLS handling. That is piggy's design, captured in piggy's own
docs when it is built; cutting-garden cross-references it and does not
restate it.

### Follow-ups implied

- piggy repo: the decoration-proxy feature itself. Tracked there, not here.
- cutting-garden, once piggy documents the mechanism: RFC 0013 Security
  Considerations and cutting-garden-config(5) cross-reference it as the way a
  wire plugin's account authenticates; `password_source = "piggy"` stays as
  the in-process path until the proxy supersedes it.
- smith (`serve-cutting_garden`): stays blocked on #285 until the proxy
  exists; the smith side is tracked on the smith repo.
- The conformance case with a stub `piggy` on PATH from option (a) is not
  needed.
