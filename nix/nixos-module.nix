# NixOS module: install the cutting-garden binary and render its RFC 0007
# config.toml to /etc, for a host that exposes cutting-garden's MCP server.
#
# Under circus FDR-0004 the MCP runs as a moxy **stdio child** (Path 1): moxy
# spawns `cutting-garden mcp` and stays the single HTTP tunnel origin. So there
# is deliberately NO standalone systemd unit here — moxy owns the process. This
# module's job is to (a) install the package so moxy can invoke it, (b) render
# config.toml to ${configDir}, and (c) declare the `environmentFile` secret
# seam that circus threads into moxy's unit environment (so the spawned child
# inherits CALDAV_*_PASSWORD etc.). circus wires the actual moxy child entry.
# See docs/features/0019-nixos-home-manager-modules.md.
#
# Imported via the flake's nixosModules.default, which passes `self`.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.cutting-garden;
  shared = import ./config.nix { inherit lib pkgs; };
  inherit (lib)
    mkIf
    mkOption
    mkEnableOption
    types
    ;
  configFile = shared.renderConfigToml cfg;
in
{
  options.services.cutting-garden = {
    enable = mkEnableOption "cutting-garden's RFC 0007 config + binary on this host";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "cutting-garden.packages.\${system}.default";
      description = "The cutting-garden package to install system-wide (so moxy can spawn it).";
    };

    configDir = mkOption {
      type = types.str;
      default = "/etc/cutting-garden";
      readOnly = true;
      description = ''
        Directory holding the rendered config.toml (at
        `/etc/cutting-garden/config.toml`, written via environment.etc). The
        moxy child spec points `cutting-garden mcp` at it by setting
        `XDG_CONFIG_HOME` to this directory's PARENT (`/etc`) — the loader
        appends `cutting-garden/config.toml` (RFC 0007 § File Location).
        Read-only; exposed for the circus/moxy consumer to reference.
      '';
    };

    environmentFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/run/cutting-garden/secrets.env";
      description = ''
        Path to a file of `VAR=value` lines providing the secrets config.toml
        references by `password_env` NAME (e.g. `CALDAV_PERSONAL_PASSWORD=…`,
        RFC 0007). Provisioned out-of-band from piggy and hand-placed root-only,
        following circus's established secret pattern (nix-cache `secretKeyFile`,
        cloudflared creds) — circus does not use sops/agenix.

        No unit in THIS module consumes it: under Path 1 the MCP is a moxy
        subprocess, so circus threads this file into moxy's unit environment
        (`serviceConfig.EnvironmentFile`) and the spawned `cutting-garden mcp`
        child inherits it. This option is the declared, swappable seam (a future
        switch to systemd `LoadCredential` wraps the same option).
      '';
    };
  }
  // shared.options;

  config = mkIf cfg.enable {
    # On PATH so circus's moxy child can invoke `cutting-garden mcp`.
    environment.systemPackages = [ cfg.package ];

    # Rendered to /etc/cutting-garden/config.toml; the moxy child reads it via
    # XDG_CONFIG_HOME=/etc. Omitted when there are no configured sections (a
    # missing file is a valid empty config, RFC 0007 § File Location).
    environment.etc = lib.mkIf (configFile != null) {
      "cutting-garden/config.toml".source = configFile;
    };
  };
}
