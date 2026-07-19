package trellis

// The public facade for this package lives at
// code.linenisgreat.com/cutting-garden/pkgs/trellis. Exported in COPY mode
// (real source copied into pkgs/, not thin aliases): docs/features/0022-
// trellis.md places the trellis parser + evaluator in the plugin SDK
// (pkgs/) — plugins consume the AST types (Query, Path, Step, Term, ...)
// directly as ordinary data, the same rationale as config_common (RFC
// 0009 §4/§5): a non-internal package must define the types plugins hold,
// not merely alias them. The explicit package arg scopes copy mode to
// THIS package; the registry/interface facades stay alias-mode (their
// identity is load-bearing).
//
//go:generate dagnabit export -copy code.linenisgreat.com/cutting-garden/internal/trellis
