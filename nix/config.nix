# Shared config schema (RFC 0007) + config.toml renderer, imported by BOTH the
# NixOS and home-manager modules so the option surface and the rendered
# config.toml cannot drift. Mirrors the Go schema in internal/config_common
# (Root/Account), plugins/caldav (AccountsConfig), and internal/traversal_serve
# (PluginStanza) — keep the TOML key names (snake_case) in lockstep with those
# struct tags.
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

  # pluginStanza mirrors internal/traversal_serve.PluginStanza (RFC 0013
  # §Host integration, generalized by cutting-garden#146 slice 2). It backs
  # BOTH the `traversalPlugins` (legacy `[[traversal_plugins]]` alias table)
  # and `plugins` (general `[[plugins]]` table) options below — the two
  # tables share one Go struct and only differ in how `command` is
  # interpreted at registration (verbatim argv vs. base invocation the host
  # appends a protocol subcommand to), which is a Go-side dispatch detail
  # invisible to this schema.
  pluginStanza = types.submodule {
    options = {
      name = mkOption {
        type = types.str;
        example = "fj";
        description = ''
          Stanza name (TOML `name`) — MUST be unique across every
          `traversalPlugins`/`plugins` entry. Also the default `configSection`.
        '';
      };
      command = mkOption {
        type = types.listOf types.str;
        example = [
          "/path/to/fj-cg"
          "traversal-serve"
        ];
        description = ''
          Argv to spawn the plugin (TOML `command`), resolved via `$PATH` when
          not absolute. A `traversalPlugins` entry's command is the full argv
          verbatim (e.g. includes `traversal-serve`); a `plugins` entry's
          command is the BASE binary invocation with NO protocol subcommand —
          the host appends `traversal-serve` and/or `capture-serve` /
          `capture-batch` itself, per `protocols` (RFC 0013 §Host
          integration).
        '';
      };
      schemes = mkOption {
        type = types.listOf types.str;
        example = [ "fj" ];
        description = ''
          URI schemes this plugin claims (TOML `schemes`), validated against
          the plugin's `initialize` echo at first spawn. MUST NOT collide with
          another stanza or a linked plugin.
        '';
      };
      configSection = mkOption {
        type = types.nullOr types.str;
        default = null;
        example = "fj";
        description = ''
          Name of this plugin's own config table (TOML `config_section`);
          defaults to `name` when null. Only meaningful together with
          `extraConfig`.
        '';
      };
      protocols = mkOption {
        type = types.nullOr (
          types.listOf (
            types.enum [
              "capture"
              "traversal"
            ]
          )
        );
        default = null;
        example = [ "traversal" ];
        description = ''
          Wire protocol(s) this plugin speaks (TOML `protocols`); an unset or
          empty value defaults to `["traversal"]` on the Go side (RFC 0013
          §Host integration). Meaningful on either table, though only
          `plugins`/general-table entries need anything other than the
          default in practice. Omitted from config.toml when null.
        '';
      };
      extraConfig = mkOption {
        type = tomlFormat.type;
        default = { };
        example = lib.literalExpression ''
          { roots = [ { name = "forge"; url = "fj://forge.example/"; } ]; }
        '';
        description = ''
          The plugin's own config section (RFC 0007 § Plugin-Owned Sections),
          rendered to a top-level `[<configSection>]` TOML table — nested
          lists of attrsets become `[[<configSection>.<key>]]` array-of-tables
          — and passed to the plugin wrapper-stripped as `initialize`'s
          `config_toml` (RFC 0013 §Host integration,
          `traversal_serve.SectionTOML`). Freeform, deliberately: this keeps
          the nix module plugin-agnostic, so any wire plugin's own section
          works without a per-plugin-type option here. Empty (default) emits
          no section table.
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
        `[[caldav.accounts]]` tables in config.toml.

        `url` MAY point at a single calendar collection OR at a
        principal/calendar-home (a URL that PROPFINDs back multiple
        calendars) — the plugin auto-DISCOVERS the calendar collections
        beneath a home-level URL (cutting-garden#162), so one account entry
        covers every calendar under that principal without hand-enumerating
        each one. `list`/the `mcp` server then surface each discovered
        calendar as its own child node, labeled by its server-side
        displayname.
      '';
    };

    traversalPlugins = mkOption {
      type = types.listOf pluginStanza;
      default = [ ];
      example = lib.literalExpression ''
        [ { name = "fj"; command = [ "''${fj-cg}/bin/fj-cg" "traversal-serve" ];
            schemes = [ "fj" ];
            extraConfig.roots = [ { name = "forge"; url = "fj://forge.example/"; } ]; } ]
      '';
      description = ''
        Out-of-process traversal wire plugins (RFC 0013 §Host integration),
        rendered to `[[traversal_plugins]]` array-of-tables in config.toml —
        the legacy/compatibility table whose `command` is the full argv
        verbatim. Each entry with a non-empty `extraConfig` also renders a
        top-level `[<configSection>]` table (`configSection` defaults to
        `name`).
      '';
    };

    plugins = mkOption {
      type = types.listOf pluginStanza;
      default = [ ];
      example = lib.literalExpression ''
        [ { name = "web"; command = [ "chrest" ]; schemes = [ "web" ];
            protocols = [ "capture" ]; } ]
      '';
      description = ''
        General wire-plugin stanzas (cutting-garden#146 slice 2's
        generalization of `traversalPlugins` to also cover the RFC 0008
        capture transport), rendered to `[[plugins]]` array-of-tables in
        config.toml. `command` is the BASE binary invocation with no protocol
        subcommand; `protocols` selects which wire session(s) the host
        launches for it. Each entry with a non-empty `extraConfig` also
        renders a top-level `[<configSection>]` table (`configSection`
        defaults to `name`).
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
      # `or [ ]` / `or { }` tolerate a partial ad-hoc cfg attrset (e.g. a
      # flake-check's `renderConfigToml { caldav.accounts = …; }` call site
      # that predates traversalPlugins/plugins) as well as the full,
      # every-option-present cfg a NixOS/home-manager module module-merge
      # always supplies.
      traversalPlugins = cfg.traversalPlugins or [ ];
      plugins = cfg.plugins or [ ];
      caldavAccounts = cfg.caldav.accounts or [ ];

      renderStanza =
        p:
        {
          inherit (p) name command schemes;
        }
        // lib.optionalAttrs (p.configSection != null) { config_section = p.configSection; }
        // lib.optionalAttrs (p.protocols != null) { inherit (p) protocols; };

      # Each plugin stanza's non-empty extraConfig becomes its OWN top-level
      # TOML table, named by configSection (default name) — the section
      # traversal_serve.SectionTOML extracts wrapper-stripped at plugin
      # registration. Pooled across BOTH tables: a configSection is a flat
      # TOML key regardless of which array-of-tables declared the stanza.
      pluginConfigSections = lib.listToAttrs (
        map (p: {
          name = if p.configSection != null then p.configSection else p.name;
          value = p.extraConfig;
        }) (builtins.filter (p: p.extraConfig != { }) (traversalPlugins ++ plugins))
      );

      settings =
        lib.optionalAttrs (caldavAccounts != [ ]) {
          caldav.accounts = map (
            a:
            {
              inherit (a) name url;
            }
            // lib.optionalAttrs (a.username != null) { inherit (a) username; }
            // lib.optionalAttrs (a.passwordEnv != null) { password_env = a.passwordEnv; }
          ) caldavAccounts;
        }
        // lib.optionalAttrs (traversalPlugins != [ ]) {
          traversal_plugins = map renderStanza traversalPlugins;
        }
        // lib.optionalAttrs (plugins != [ ]) {
          plugins = map renderStanza plugins;
        }
        // pluginConfigSections;
    in
    if settings == { } then null else tomlFormat.generate "cutting-garden-config.toml" settings;
}
