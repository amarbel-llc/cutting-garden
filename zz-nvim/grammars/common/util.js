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

// A trellis Ident (RFC 0014 / hyphence-content.peg's Reserved set, mirrored by
// internal/trellis/lex.go's reservedRunes): a run that stops at whitespace or a
// reserved rune `[]^=,!@<>*$~%#"'()`; `-`, `/`, `.` are admitted, so `-client`,
// `date_due`, `sched1.ics`, and `project-client-acme` are single words. Use as
// `token(IDENT)` wherever a bare tag / field key / dimension name is lexed.
const IDENT = /[^\s\[\]^=,!@<>*$~%#"'()]+/;

module.exports = { sepBy1, IDENT };
