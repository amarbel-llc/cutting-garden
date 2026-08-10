// Shared rules for the espalier box format (RFC 0014's ground fragment; the
// compact one-line object representation inside an organize object line):
//
//   [<id> !<type> <key>=<value> @<blob> <tag>] description
//
// Adapted from dodder/zz-nvim's common/box.js for cutting-garden#43 (see
// common/util.js for the vendor-now/share-later note). The dialect change:
// cutting-garden box ids are relative node paths (`sched1.ics`, `sub/x.ics`) with
// dots and slashes, not dodder's `left/right` zettel ids — so `box_object_id` is a
// path token and the box is the regular `[<id> items...]` shape (the id is
// positionally first), which also removes dodder's box_object_id/box_tag GLR
// conflict. `box_field` (`key=value`, e.g. `date_start=2026-08-15`), `box_type`,
// `box_blob`, `box_computed_tag`, and `box_tag` are unchanged.
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
      $.box_quoted,
      $.box_tag,
    ),

  box_blob: $ => seq('@', $.markl_id),

  box_type: $ => seq('!', field('name', $.box_ident)),

  box_computed_tag: $ => seq('%', field('name', $.box_ident)),

  box_field: $ =>
    seq(field('key', $.box_ident), '=', field('value', $._box_value)),
  _box_value: $ => choice($.box_quoted, $.box_bare_value),
  box_bare_value: $ => token(/[^ \t\]"]+/),

  box_tag: $ => $.box_ident,

  box_ident: $ => token(/[a-zA-Z0-9][a-zA-Z0-9_-]*/),

  box_quoted: $ =>
    seq('"', repeat(choice($.box_escape, token.immediate(/[^"\\]+/))), '"'),
  box_escape: $ => token.immediate(/\\./),
};
