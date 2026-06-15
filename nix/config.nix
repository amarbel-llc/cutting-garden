# Shared config schema (RFC 0007) + config.toml renderer, imported by BOTH the
# NixOS and home-manager modules so the option surface and the rendered
# config.toml cannot drift. Mirrors the Go schema in internal/config_common
# (Root/Account) and plugins/caldav (AccountsConfig) — keep the TOML key names
# (snake_case) in lockstep with those struct tags.
{ lib, pkgs }:
let
  inherit (lib) mkOption types;
  tomlFormat = pkgs.formats.toml { };

  account = types.submodule {
    options = {
      name = mkOption {
        type = types.str;
        example = "personal";
        description = "Account label, unique within the plugin section (TOML `name`).";
      };
      url = mkOption {
        type = types.str;
        example = "caldav://dav.host/dav/me/";
        description = "Endpoint URI in the form the plugin accepts on the CLI (TOML `url`).";
      };
      username = mkOption {
        type = types.nullOr types.str;
        default = null;
        description = "Account username (TOML `username`). Omitted from config.toml when null.";
      };
      passwordEnv = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "CALDAV_PERSONAL_PASSWORD";
        description = ''
          Name of the environment variable holding the account password (TOML
          `password_env`) — NEVER the secret value itself (RFC 0007 § Security
          Considerations). Omitted from config.toml when null.
        '';
      };
    };
  };
in
{
  # Option declarations shared by both modules. Each module merges these into
  # its own namespace (`programs.cutting-garden` / `services.cutting-garden`).
  options = {
    caldav.accounts = mkOption {
      type = types.listOf account;
      default = [ ];
      example = lib.literalExpression ''
        [ { name = "personal"; url = "caldav://dav.host/dav/me/"; username = "me";
            passwordEnv = "CALDAV_PERSONAL_PASSWORD"; } ]
      '';
      description = ''
        cutting-garden caldav accounts (RFC 0007), rendered to
        `[[caldav.accounts]]` tables in config.toml. caldav is the only config
        section implemented today; future plugin sections extend this attrset.
      '';
    };
  };

  # renderConfigToml maps the typed config to a config.toml store path, or null
  # when there is nothing to write — a missing file is a valid empty config
  # (RFC 0007 § File Location), so the caller skips writing one. camelCase
  # options become snake_case TOML keys; null fields are omitted.
  renderConfigToml =
    cfg:
    let
      settings = lib.filterAttrs (_: v: v != { }) {
        caldav = lib.optionalAttrs (cfg.caldav.accounts != [ ]) {
          accounts = map (
            a:
            {
              inherit (a) name url;
            }
            // lib.optionalAttrs (a.username != null) { inherit (a) username; }
            // lib.optionalAttrs (a.passwordEnv != null) { password_env = a.passwordEnv; }
          ) cfg.caldav.accounts;
        };
      };
    in
    if settings == { } then null else tomlFormat.generate "cutting-garden-config.toml" settings;
}
