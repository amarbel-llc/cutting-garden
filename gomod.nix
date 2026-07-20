# Nix side of go.mod for cutting-garden. Pure-consumer half of the
# flake-input-go_mod protocol (amarbel-llc/nixpkgs RFC 0001).
#
# Each entry routes one go.mod `require` line onto a sibling flake's
# `go-pkgs` output instead of the organic gomod2nix.toml hash. Bumping
# the corresponding flake input is then a flake.lock-only edit — no
# `go get` + `gomod2nix generate` lockstep required (the strategy the
# Phase 6 cutover landing exposed as friction).
#
# Modules NOT listed here continue to resolve through gomod2nix.toml —
# OR through a listed producer's `passthru.goFlakeInputs` re-exports:
# igloo's depth-N inheritance (igloo#58) walks producer passthrus
# transitively, so madder's bridge brings tap/go, tommy, crap/go-crap/v2,
# hyphence/go, and piggy/go along without explicit entries here
# (cutting-garden#134; they were declared directly before). Their revs
# are pinned to OUR flake inputs via the madder.inputs.*.follows wiring
# in flake.nix, so bumping any of them remains a flake.lock-only edit.
# Add an explicit entry only for a module no listed producer re-exports.
{
  madder,
  purse-first,
  system,
}:
{
  # madder.go-pkgs is already scoped to madder's `go/` subdir
  # (its producer slices upstream), so no subPath here. purse-first's
  # go-pkgs is the whole workspace (the repo root carries multiple go
  # modules + non-Go assets), so we slice into each module subdir.
  "code.linenisgreat.com/madder/go" = {
    src = madder.packages.${system}.go-pkgs;
  };
  "code.linenisgreat.com/purse-first/libs/dewey" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/dewey";
  };
  # go-mcp lives in the same purse-first workspace as dewey, so the same
  # go-pkgs output backs it; slice into its module subdir. Bridged like
  # dewey so a purse-first bump stays a flake.lock-only edit. Neither
  # purse-first module is re-exported by any producer's passthru, so both
  # stay direct.
  "code.linenisgreat.com/purse-first/libs/go-mcp" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/go-mcp";
  };
}
