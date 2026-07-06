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
# Add an entry when its upstream flake exposes `go-pkgs`.
{
  madder,
  hyphence,
  tap,
  crap,
  purse-first,
  piggy,
  tommy,
  system,
}:
{
  # madder.go-pkgs is already scoped to madder's `go/` subdir
  # (its producer slices upstream), so no subPath here. tap.go-pkgs
  # is full-repo-filtered (polyglot), so we still slice into its
  # `go/` subdir. purse-first's go-pkgs is the whole workspace (the
  # repo root carries multiple go modules + non-Go assets), so we
  # slice into `libs/dewey`.
  "github.com/amarbel-llc/madder/go" = {
    src = madder.packages.${system}.go-pkgs;
  };
  # hyphence.go-pkgs is already scoped to its `go/` subdir (the producer
  # slices upstream, like madder), so no subPath here. The canonical
  # hyphence library extracted in madder#253; cutting-garden consumes it
  # directly rather than through madder's deleted re-export.
  "github.com/amarbel-llc/hyphence/go" = {
    src = hyphence.packages.${system}.go-pkgs;
  };
  # piggy owns the markl-id framework (piggy#183 ownership inversion); madder
  # deleted its go/pkgs/markl re-export, so cutting-garden imports piggy's
  # pkgs/markl directly. piggy's go-pkgs producer is scoped to `go/` (the
  # module root — module path is github.com/amarbel-llc/piggy/go), so no
  # subPath. piggy's passthru bridges dewey for consumers that need it, but
  # cutting-garden already bridges dewey via purse-first above, so no extra
  # entry here. Mirrors madder's consumer (madder master 0063d39).
  "github.com/amarbel-llc/piggy/go" = {
    src = piggy.packages.${system}.go-pkgs;
  };
  "github.com/amarbel-llc/tap/go" = {
    src = tap.packages.${system}.go-pkgs;
    subPath = "go";
  };
  # crap.go-pkgs is full-repo-filtered (polyglot), so slice into go-crap.
  # The module is at major version 2, so the key carries the /v2 suffix
  # while the on-disk subPath stays go-crap.
  "github.com/amarbel-llc/crap/go-crap/v2" = {
    src = crap.packages.${system}.go-pkgs;
    subPath = "go-crap";
  };
  "github.com/amarbel-llc/purse-first/libs/dewey" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/dewey";
  };
  # go-mcp lives in the same purse-first workspace as dewey, so the same
  # go-pkgs output backs it; slice into its module subdir. Bridged like
  # dewey so a purse-first bump stays a flake.lock-only edit.
  "github.com/amarbel-llc/purse-first/libs/go-mcp" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/go-mcp";
  };
  # tommy's go-pkgs is the whole repo-root module (its module path is the
  # repo root), so no subPath. Bridged so the devshell `tommy generate`
  # binary and the Go library compile at one flake.lock rev — tommy
  # stamps its version into generated files and `--check` fails on skew.
  "github.com/amarbel-llc/tommy" = {
    src = tommy.packages.${system}.go-pkgs;
  };
}
