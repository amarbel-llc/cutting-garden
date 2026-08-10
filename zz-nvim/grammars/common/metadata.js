// Shared rules for the hyphence metadata block (hyphence RFC 0001/0002; the
// `---`-fenced envelope of an organize document, RFC 0015).
//
// Adapted from dodder/zz-nvim's common/metadata.js for cutting-garden#43 (see
// common/util.js for the vendor-now/share-later note). The one dialect change:
// cutting-garden's envelope carries hyphence RFC 0002 FIELD lines
// (`- _base = @digest`, `- _anchor = <uri>`, `- _type = !type`) rather than
// dodder's bare `- tag` lines, so `tag_line` is replaced by `field_line`, which
// structures the key / `=` / value (blob, type, or bare) for distinct
// highlighting.
//
// These rules assume the grammar sets `extras: []` -- newlines are significant
// and consumed explicitly, so line boundaries survive into the tree.
//
// Requires the markl rules (markl_id, ...) spread into the same grammar.

module.exports = {
  // The metadata block ends exactly at the closing "---"; the fence line's
  // terminating newline (and any following blank line / body) belongs to
  // whatever consumes the metadata, so this composes with the structured
  // organize body without a newline conflict.
  metadata: $ =>
    seq(
      '---',
      '\n',
      repeat($._metadata_line),
      '---',
    ),

  _metadata_line: $ =>
    choice(
      $.description_line,
      $.field_line,
      $.blob_line,
      $.type_line,
      $.ref_line,
      $.comment_line,
    ),

  description_line: $ =>
    seq('#', ' ', field('text', $.description_text), '\n'),
  description_text: $ => token(/[^\n]+/),

  // A hyphence field line: `- <key> = <value>`. The value is a blob (`@digest`),
  // a type (`!type`), or a bare string (a URI, a trellis query), distinguished by
  // the leading rune so each highlights distinctly.
  field_line: $ =>
    seq(
      '-',
      ' ',
      field('key', $.field_key),
      ' ',
      '=',
      ' ',
      field('value', $._field_value),
      '\n',
    ),
  field_key: $ => token(/[^\s=]+/),
  _field_value: $ => choice($.field_blob, $.field_type, $.field_bare),
  field_blob: $ => seq('@', $.markl_id),
  field_type: $ => seq('!', field('name', $.field_type_name)),
  field_type_name: $ => token(/[^\n]+/),
  // A bare value (a URI, a trellis query) — its first rune must not be `@` or
  // `!`, so the structured blob (`@digest`) and type (`!type`) alternatives win
  // rather than being shadowed by this greedy catch-all token.
  field_bare: $ => token(/[^@!\n][^\n]*/),

  ref_line: $ => seq('<', ' ', field('ref', $.reference_id), '\n'),
  reference_id: $ => token(/[^\n]+/),

  type_line: $ =>
    seq(
      '!',
      ' ',
      field('type', $.type_name),
      optional(seq('@', field('lock', $.markl_id))),
      '\n',
    ),
  type_name: $ => token(/[^@\n]+/),

  blob_line: $ =>
    seq('@', ' ', field('ref', choice($.markl_id, $.file_path)), '\n'),
  // A file path must contain a ".", which a markl id never does, so the two are
  // mutually exclusive and longest-match separates them cleanly.
  file_path: $ => token(/[^\n@]*\.[^\n@.]+/),

  comment_line: $ =>
    seq('%', optional(field('text', $.comment_text)), '\n'),
  comment_text: $ => token(/[^\n]+/),
};
