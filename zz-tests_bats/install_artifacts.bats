setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  export output
}

# bats file_tags=install

# Phase 5 step 4: assert flake.nix postInstall produced every
# expected artifact under <out>/share/. CG_BIN points at
# <out>/bin/cutting-garden so we derive <out> by stripping the
# trailing path. Nixpkgs auto-gzips manpages, so the test looks
# for `.1.gz` (the on-disk form) rather than `.1`.

# install_prefix derives the package's nix-store <out> from CG_BIN.
install_prefix() {
  local bin="${CG_BIN:-}"
  [[ -n $bin ]] || fail "CG_BIN not set; cannot derive install prefix"
  # Strip the trailing /bin/cutting-garden to get <out>.
  echo "${bin%/bin/cutting-garden}"
}

function install_emits_toplevel_manpage { # @test
  # cutting-garden(1) is the discovery page — what `man cutting-garden`
  # serves. Without it, users can't find the per-subcommand pages.
  local prefix
  prefix="$(install_prefix)"

  [[ -f "$prefix/share/man/man1/cutting-garden.1.gz" ]] ||
    fail "missing toplevel manpage at $prefix/share/man/man1/cutting-garden.1.gz"
}

function install_emits_subcommand_manpages { # @test
  # One .1.gz per user-facing subcommand.
  local prefix
  prefix="$(install_prefix)"

  for sub in capture restore diff serve failures health list; do
    [[ -f "$prefix/share/man/man1/cutting-garden-$sub.1.gz" ]] ||
      fail "missing manpage for subcommand '$sub' at $prefix/share/man/man1/cutting-garden-$sub.1.gz"
  done
}

function install_omits_hidden_subcommand_manpages { # @test
  # Hidden commands (CommandHidden: `complete`, `__write-blob`, `hook`)
  # are framework plumbing — they get no per-subcommand manpage, matching
  # their absence from the toplevel SUBCOMMANDS/SEE ALSO list.
  local prefix
  prefix="$(install_prefix)"

  for sub in complete __write-blob hook; do
    [[ ! -e "$prefix/share/man/man1/cutting-garden-$sub.1.gz" ]] ||
      fail "hidden subcommand '$sub' should not have a manpage at $prefix/share/man/man1/cutting-garden-$sub.1.gz"
  done
}

function install_emits_bash_completion { # @test
  # share/bash-completion/completions/cutting-garden — bash sources
  # the file by binary name.
  local prefix
  prefix="$(install_prefix)"

  [[ -f "$prefix/share/bash-completion/completions/cutting-garden" ]] ||
    fail "missing bash completion at $prefix/share/bash-completion/completions/cutting-garden"
}

function install_emits_fish_completion { # @test
  # share/fish/vendor_completions.d/cutting-garden.fish — fish
  # auto-loads from vendor_completions.d.
  local prefix
  prefix="$(install_prefix)"

  [[ -f "$prefix/share/fish/vendor_completions.d/cutting-garden.fish" ]] ||
    fail "missing fish completion at $prefix/share/fish/vendor_completions.d/cutting-garden.fish"
}

function install_emits_zsh_completion { # @test
  # share/zsh/site-functions/_cutting-garden — zsh auto-loads
  # functions with the `_<cmd>` prefix.
  local prefix
  prefix="$(install_prefix)"

  [[ -f "$prefix/share/zsh/site-functions/_cutting-garden" ]] ||
    fail "missing zsh completion at $prefix/share/zsh/site-functions/_cutting-garden"
}

function install_emits_cg_alias { # @test
  # The cg alias produces its own binary, its own per-shell
  # completion stubs (each baked with `cg` so `complete -F _cg cg`
  # registers correctly), and a manpage symlink at cg.1 → cutting-
  # garden.1. nixpkgs follows the symlink during compress and may
  # emit either form on disk; assert with `-e` to accept both.
  local prefix
  prefix="$(install_prefix)"

  [[ -x "$prefix/bin/cg" ]] ||
    fail "missing cg binary at $prefix/bin/cg"

  [[ -e "$prefix/share/man/man1/cg.1.gz" ]] ||
    fail "missing cg manpage at $prefix/share/man/man1/cg.1.gz"

  [[ -f "$prefix/share/bash-completion/completions/cg" ]] ||
    fail "missing cg bash completion at $prefix/share/bash-completion/completions/cg"

  [[ -f "$prefix/share/fish/vendor_completions.d/cg.fish" ]] ||
    fail "missing cg fish completion at $prefix/share/fish/vendor_completions.d/cg.fish"

  [[ -f "$prefix/share/zsh/site-functions/_cg" ]] ||
    fail "missing cg zsh completion at $prefix/share/zsh/site-functions/_cg"
}

function install_omits_gen_binary { # @test
  # The gen binary was used to produce the artifacts then deleted
  # in postInstall. Release artifacts must not ship it — it has no
  # user-facing utility.
  local prefix
  prefix="$(install_prefix)"

  [[ ! -e "$prefix/bin/cutting-garden-gen" ]] ||
    fail "cutting-garden-gen leaked into release artifacts at $prefix/bin/cutting-garden-gen"
}
