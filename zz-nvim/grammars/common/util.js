// Shared grammar-construction helpers, not tied to any one format.
//
// Vendored from dodder/zz-nvim (code.linenisgreat.com/dodder) for cutting-garden's
// organize tree-sitter grammar (cutting-garden#43). The tree-sitter grammars are
// the same family (shared hyphence envelope + espalier box); this copy is the
// "vendor now, share later" interim — extracting shared grammar homes (mirroring
// the marklid.peg / hyphence-content.peg PEG composition) is a tracked followup.
//
// grammar.js DSL helpers (seq, repeat, ...) are provided as globals by
// tree-sitter and remain in scope inside required modules.

// One or more `rule`, separated by `sep` (no trailing separator).
function sepBy1(sep, rule) {
  return seq(rule, repeat(seq(sep, rule)));
}

module.exports = { sepBy1 };
