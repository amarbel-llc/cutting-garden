# Nix side of go.mod for cutting-garden. Pure-consumer half of the
# flake-input-go_mod protocol (amarbel-llc/nixpkgs RFC 0001).
#
# Each entry routes one go.mod `require` line onto a sibling flake's
# `go-pkgs` output instead of the organic gomod2nix.toml hash. Bumping
# the corresponding flake input is then a flake.lock-only edit — no
# `go get` + `gomod2nix generate` lockstep required (the strategy the
# Phase 6 cutover landing exposed as friction).
#
# Modules NOT listed here continue to resolve through gomod2nix.toml.
# Add an entry when its upstream flake exposes `go-pkgs`; dewey is on
# the followup list but purse-first does not publish go-pkgs yet, so
# it stays organic for now.
{
  madder,
  tap,
  system,
}:
{
  # madder.go-pkgs is already scoped to madder's `go/` subdir
  # (its producer slices upstream), so no subPath here. tap.go-pkgs
  # is full-repo-filtered (polyglot), so we still slice into its
  # `go/` subdir.
  "github.com/amarbel-llc/madder/go" = {
    src = madder.packages.${system}.go-pkgs;
  };
  "github.com/amarbel-llc/tap/go" = {
    src = tap.packages.${system}.go-pkgs;
    subPath = "go";
  };
}
