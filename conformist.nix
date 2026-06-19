# cutting-garden's conformist config as a nix module (conformist#51/#114). This
# replaces the former hand-written conformist.toml: flake.nix evals it via
# `conformist.lib.evalModule` together with `conformist.lib.presets.eng`, and the
# generated `conformist.toml` (build.configFile) drives `nix fmt`
# (build.wrapper), the sandboxed `checks.formatting` (build.check), and the
# store-pinned `conformist-pre-commit` hook (build.preCommit). The impure
# git-state lane (presets.eng-impure) is a separate eval wired to `just
# lint-worktree`. `package` is injected by flake.nix.
#
# The tommy formatter and the tommy-codegen repair linter have no registry
# program (they are purse-first/RFC-0007-specific), so they live as inline
# freeform blocks in flake.nix where the `tommy` flake input is in scope — NOT
# here, since a standalone module file can't see flake inputs. Everything with a
# registry program lives here.
#
# See eng-design_patterns-conformist(7), conformist-nix(7).
{ ... }:
{
  # Go: goimports (priority 1) runs before gofumpt (priority 2) so the
  # import-grouped output is re-canonicalized by gofumpt. Both registry programs
  # pass -w (write in place), required for conformist's sandbox check to observe
  # drift.
  programs.goimports.enable = true;
  programs.goimports.priority = 1;
  programs.gofumpt.enable = true;
  programs.gofumpt.priority = 2;

  programs.nixfmt.enable = true;

  # shfmt: the registry defaults (indent_size = 2, simplify, caseIndent) emit
  # `-i 2 -s -ci` — cutting-garden's house style (conformist#52 made caseIndent
  # default-on). Narrow the includes to the shell we actually format; the
  # registry default also matches *.envrc, which cutting-garden has none of.
  programs.shfmt.enable = true;
  programs.shfmt.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];

  # just ships its own formatter; the registry wraps `just --fmt --unstable` in a
  # bash loop with a store-pinned `just`, equivalent to the old hand-written
  # [formatter.just] block but hermetic.
  programs.just.enable = true;

  # shellcheck (read-only linter) over the same shell set as shfmt.
  linters.shellcheck.enable = true;
  linters.shellcheck.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];

  settings.excludes = [
    "flake.lock"
    "go.sum"
    "gomod2nix.toml"
    "version.env"
    "sweatfile"
    "LICENSE"
    "*.md"
    "result"
    "result-*"
    ".tmp/**"
    # tommy codegen output (RFC 0007): `tommy generate` owns its formatting and
    # stamps a version header; gofumpt would fight `just generate-check`.
    "*_tommy.go"
  ];
}
