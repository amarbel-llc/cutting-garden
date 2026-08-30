// Shared rules for the espalier box format (RFC 0014's ground fragment; the
// compact one-line object representation inside an organize object line):
//
//   [<id> !<type> <tag> "<quoted tag>" <key>=<value> @<blob>] description
//
// Adapted from dodder/zz-nvim's common/box.js for cutting-garden#43 (see
// common/util.js for the vendor-now/share-later note). The dialect change:
// cutting-garden box ids are relative node paths (`sched1.ics`, `sub/x.ics`) with
// dots and slashes, not dodder's `left/right` zettel ids — so `box_object_id` is a
// path token and the box is the regular `[<id> items...]` shape (the id is
// positionally first), which also removes dodder's box_object_id/box_tag GLR
// conflict. `box_field` (`key=value`, e.g. `date_start=2026-08-15`), `box_type`,
// `box_blob`, and `box_computed_tag` are unchanged.
//
// Native tags (design G9/G13): the interior is a trellis Group's GROUND subset,
// so a bare word that is not `key=value` is a TAG atom (`box_tag`, its name a
// `box_ident` — even one spelled like a field name, `status`), and a bare
// quoted string is a QUOTED tag (`box_tag` wrapping `box_tag_quoted`, the
// spelling for whitespace / reserved runes: `"_ inbox"`). `box_ident` and
// `box_bare_value` follow trellis's Ident: they stop at whitespace or a reserved
// rune `[]^=,!@<>*$~%#"'()` — parens included, so a `(…)` qualifier never
// appears inside a box (headings/envelope only).
//
// Assumes the grammar sets `extras: []`; spaces inside the brackets are matched
// explicitly. Requires the markl rules (markl_id) in the same grammar.

module.exports = {
  box: $ =>
    seq(
      '[',
      field('id', $.box_object_id),
      repeat(seq($._box_space, $._box_item)),
      optional($._box_space),
      ']',
    ),
  _box_space: $ => token(/[ \t]+/),

  // A relative node id: a path segment run with '.' and '/' admitted (a
  // filename like `sched1.ics`, or `sub/event.ics`).
  box_object_id: $ => token(/[a-zA-Z0-9][a-zA-Z0-9._\/-]*/),

  _box_item: $ =>
    choice(
      $.box_blob,
      $.box_type,
      $.box_computed_tag,
      $.box_field,
      $.box_tag,
    ),

  box_blob: $ => seq('@', $.markl_id),

  box_type: $ => seq('!', field('name', $.box_ident)),

  box_computed_tag: $ => seq('%', field('name', $.box_ident)),

  box_field: $ =>
    seq(field('key', $.box_ident), '=', field('value', $._box_value)),
  _box_value: $ => choice($.box_quoted, $.box_bare_value),
  box_bare_value: $ => token(/[^\s\[\]^=,!@<>*$~%#"'()]+/),

  // A tag atom: bare (`work`, `project-client-acme`) or quoted (`"_ inbox"`).
  // The same `box_ident` token as a field key — the parser splits them on the
  // `=` that follows a key (LR(1)), so no lexical conflict.
  box_tag: $ =>
    field(
      'name',
      choice($.box_ident, alias($.box_quoted, $.box_tag_quoted)),
    ),

  box_ident: $ => token(/[^\s\[\]^=,!@<>*$~%#"'()]+/),

  box_quoted: $ =>
    seq('"', repeat(choice($.box_escape, token.immediate(/[^"\\]+/))), '"'),
  box_escape: $ => token.immediate(/\\./),
};
