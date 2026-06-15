# home-manager module: render cutting-garden's RFC 0007 config.toml into the
# user's XDG config dir and install the binary. The MCP server is launched by
# an MCP client (claude/clown, or a user moxy child) over stdio, so this module
# manages config + package only — there is no long-running service to define.
# This is the workstation counterpart of the headless NixOS module
# (see docs/features/0019-nixos-home-manager-modules.md).
#
# Imported via the flake's homeManagerModules.default, which passes `self` so
# `package` can self-default to the flake's cutting-garden.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.cutting-garden;
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
  options.programs.cutting-garden = {
    enable = mkEnableOption "cutting-garden's RFC 0007 config + binary for this user";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "cutting-garden.packages.\${system}.default";
      description = "The cutting-garden package to install into the user environment.";
    };
  }
  // shared.options;

  config = mkIf cfg.enable (
    {
      home.packages = [ cfg.package ];
    }
    // lib.optionalAttrs (configFile != null) {
      # Renders to $XDG_CONFIG_HOME/cutting-garden/config.toml — exactly where
      # the binary's config loader reads it (RFC 0007 § File Location).
      xdg.configFile."cutting-garden/config.toml".source = configFile;
    }
  );
}
