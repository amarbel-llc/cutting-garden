---
status: proposed
date: 2026-06-15
promotion-criteria: |
  proposed -> experimental: circus consumes the exported modules as a flake
  input; a headless host with `services.cutting-garden.enable = true` renders
  `/etc/cutting-garden/config.toml`, and moxy spawns `cutting-garden mcp` as a
  stdio child that surfaces the configured roots end to end. A workstation with
  `programs.cutting-garden.enable = true` renders
  `~/.config/cutting-garden/config.toml` and the binary reads it.
  experimental -> accepted: a tunneled host serves cutting-garden's MCP through
  moxy behind the Cloudflare tunnel + Zero Trust Access (circus FDR-0004), and
  the `environmentFile` secrets reach the spawned child so a credentialed
  caldav account resolves.
---

# NixOS + home-manager modules for cutting-garden

## Problem Statement

cutting-garden's configuration (RFC 0007) lives at
`$XDG_CONFIG_HOME/cutting-garden/config.toml` — per-plugin accounts/roots, e.g.
caldav accounts referencing passwords by environment-variable name. Today that
file is hand-written, and there is no nix-native way to (a) express it
declaratively or (b) make cutting-garden's MCP server available on a host that
eng's circus manages.

This FDR specifies two modules, **exported from cutting-garden's own flake** and
consumed by circus, that close both gaps with one shared config schema:

- a **home-manager** module (`programs.cutting-garden`) — the workstation case:
  render `~/.config/cutting-garden/config.toml` and install the binary;
- a **NixOS** module (`services.cutting-garden`) — the headless-host case:
  install the binary, render `/etc/cutting-garden/config.toml`, and declare the
  secret seam circus needs.

The requirements were settled with circus (`rich-elder`): the module shape
mirrors circus's own `nix-cache/nix/module.nix` (producer flake exports
`nixosModules.default`; circus consumes it), and the MCP exposure follows
**Path 1** below.

## Interface

### Exposure model — moxy stdio child (Path 1)

cutting-garden's MCP is stdio-only (`internal/mcp/mcp.go` →
`transport.NewStdio`). Under circus FDR-0004, moxy is the single HTTP origin
behind one Cloudflare tunnel and aggregates stdio MCP children. So on a host,
`cutting-garden mcp` runs as a **moxy stdio child**, not a standalone HTTP
origin and not its own systemd unit — moxy owns the process. cutting-garden
grows **no new transport**. (Local-workstation exposure remains the existing
`cutting-garden-clown-plugin` + mkCircus path, cutting-garden#101.)

Consequently the NixOS module deliberately defines **no systemd service**. Its
jobs are: install the package (so moxy can invoke it), render config.toml to
`/etc`, and declare an `environmentFile` seam. circus wires the moxy child entry
(command + `XDG_CONFIG_HOME` + env-file) on its side.

### Flake outputs

```
nixosModules.default       = import ./nix/nixos-module.nix self;
homeManagerModules.default = import ./nix/home-manager-module.nix self;
```

Both are `self:`->module-fn so `package` self-defaults to the flake's
`cutting-garden`. The shared schema + renderer is `nix/config.nix`
(`{ lib, pkgs }: { options; renderConfigToml; }`), imported by both modules so
the option surface and the rendered TOML cannot drift from each other or from
the Go structs (`internal/config_common`, `plugins/caldav`).

### Options (both modules)

- `enable`, `package`.
- `caldav.accounts` — list of `{ name; url; username?; passwordEnv?; }`,
  rendered to RFC 0007 `[[caldav.accounts]]` (`passwordEnv` → `password_env`,
  null fields omitted).
- `traversalPlugins` / `plugins` (cutting-garden#158) — lists of
  `{ name; command; schemes; configSection?; protocols?; extraConfig?; }`,
  rendered to `[[traversal_plugins]]` / `[[plugins]]` array-of-tables
  (RFC 0013 §Host integration, generalized by cutting-garden#146). These are
  the declarative seam for out-of-process wire plugins like forgejo-cli's
  `fj-cg` — circus wires one via `traversalPlugins` without any
  cutting-garden-module change. `extraConfig` is a freeform attrset rendered
  to the plugin's own `[<configSection>]` table (`configSection` defaults to
  `name`); the nix module stays plugin-agnostic — it has no per-plugin-type
  option, mirroring the Go side's own config-agnostic `SectionTOML`
  wrapper-stripping.

### Options (NixOS only)

- `configDir` (read-only, `/etc/cutting-garden`) — where config.toml is
  rendered; circus points the child at it via `XDG_CONFIG_HOME=/etc`.
- `environmentFile` (`nullOr path`) — a file of `VAR=value` lines supplying the
  secrets config.toml references by `password_env` name. Provisioned out-of-band
  from piggy and threaded into **moxy's** unit by circus (no unit here consumes
  it). The seam is mechanism-agnostic: a later switch to systemd
  `LoadCredential` wraps the same option.

## Examples

Workstation (home-manager):

```nix
programs.cutting-garden = {
  enable = true;
  caldav.accounts = [{
    name = "personal";
    url = "caldav://dav.host/dav/me/";
    username = "me";
    passwordEnv = "CALDAV_PERSONAL_PASSWORD";
  }];
};
# → ~/.config/cutting-garden/config.toml
```

Headless host (NixOS), consumed by circus:

```nix
services.cutting-garden = {
  enable = true;
  caldav.accounts = [{ name = "team"; url = "caldav://dav.host/dav/team/";
                       username = "me"; passwordEnv = "CALDAV_TEAM_PASSWORD"; }];
  traversalPlugins = [{
    name = "fj";
    command = [ "${fj-cg}/bin/fj-cg" "traversal-serve" ];
    schemes = [ "fj" ];
    extraConfig.roots = [{ name = "forge"; url = "fj://forge.example/linenisgreat/cutting-garden"; }];
  }];
  environmentFile = "/run/cutting-garden/secrets.env";  # placed out-of-band
};
# → /etc/cutting-garden/config.toml. circus adds a moxy child:
#     command: cutting-garden mcp ; env: XDG_CONFIG_HOME=/etc
#   and sets moxy's unit EnvironmentFile = services.cutting-garden.environmentFile
```

## Limitations

- **No systemd unit (Path 1).** The MCP is a moxy subprocess; this module
  installs + configures but does not run it. The moxy-child registration lives
  in circus, not in these exported modules.
- **Config path via `XDG_CONFIG_HOME`.** The headless wiring sets
  `XDG_CONFIG_HOME=/etc` for the child rather than passing a config path
  directly — `mcp`/`list` have no `-config` flag yet. A follow-up adds one so
  the wiring can use `--config /etc/cutting-garden/config.toml`.
- **Secrets are out-of-band.** No sops/agenix; `environmentFile` is a path the
  host provisions from piggy, matching circus's nix-cache `secretKeyFile`
  pattern. Plaintext passwords never enter config.toml or the nix store
  (RFC 0007 § Security Considerations).
- **caldav plus generic wire-plugin sections.** `caldav.accounts` is the only
  TYPED per-plugin section; every other plugin section (a wire plugin's own
  `[<configSection>]` table, cutting-garden#158) goes through the freeform
  `extraConfig` attrset on `traversalPlugins`/`plugins` entries rather than a
  dedicated option — the schema stays structured so a FUTURE typed section
  (ytdlp/sftp/webdav/github) can still drop in additively if warranted.
- **Eval-check only.** A pure `nix flake check` (`checks.modules-eval`) renders
  a sample config and confirms the binary loads it and surfaces the root. A full
  NixOS VM `nixosTest` (booting a host + stubbed moxy child) is a deferred
  follow-up.

## More Information

- [RFC 0007](../rfcs/0007-config-subsystem.md) — the config.toml schema these
  modules render (`Root`/`Account`, the caldav section, file location).
- circus FDR-0004 (mcp-tunnel) — the moxy-single-origin model Path 1 follows;
  circus FDR-0007 / `nix-cache/nix/module.nix` — the module + `secretKeyFile`
  secret-seam template mirrored here.
- cutting-garden#101 — the `cutting-garden-clown-plugin` + mkCircus local
  exposure path (Path 3), unchanged by this FDR.
- Follow-ups: a `-config` flag on `mcp`/`list`; a NixOS VM test; the circus-side
  moxy-child registration consuming these modules.
