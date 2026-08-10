// Shared rules for markl-id tokens (piggy markl-id(7); the @digest in an
// espalier box, e.g. `@blake2b256-9ft3x`).
//
// Vendored from dodder/zz-nvim (code.linenisgreat.com/dodder) for cutting-garden#43
// (see common/util.js for the vendor-now/share-later note). markl-ids are the
// shared piggy primitive, so this copies verbatim.
//
// A markl id has the text form:  [purpose@]format-data
//   purpose  optional semantic context (hyphens/underscores), terminated by "@"
//   format   the algorithm / human-readable part (e.g. blake2b256) -- letters,
//            digits, underscores
//   data     the blech32 payload + checksum -- matched leniently as [a-z0-9]+
//            for highlighting.
//
// grammar.js DSL helpers (seq, field, token, optional, ...) are globals provided
// by tree-sitter and remain in scope inside required modules.

module.exports = {
  markl_id: $ =>
    seq(
      optional($.markl_purpose),
      field('format', $.markl_format),
      '-',
      field('data', $.markl_data),
    ),

  // The trailing "@" is part of the token so the lexer can distinguish a
  // purpose (which requires "@") from a bare format without conflict.
  markl_purpose: $ => token(/[a-z0-9_][a-z0-9_-]*@/),

  markl_format: $ => token(/[a-z0-9_]+/),

  markl_data: $ => token(/[a-z0-9]+/),
};
