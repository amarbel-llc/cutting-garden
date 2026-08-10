// tree-sitter grammar for cutting-garden's organize document dialect
// (RFC 0015 / FDR 0023).
//
//   ---
//   % generated: cg organize -group-by status caldav:.../cal/
//   - _base = @blake2b256-9ft3x
//   - _anchor = caldav:.../cal/
//   - _type = !caldav-object-vtodo-v1
//   ! organize-base-v1
//   ---
//
//   # status=
//   ## =COMPLETED
//   - [task1.ics date_start=2026-08-15 time_start=09-30] Buy milk
//
// A hyphence metadata envelope, then a body of heading-ladder lines and espalier
// object lines. Headings are the RFC 0015 ladder — `# !<type>` (spelling 1), a
// `<dim>=` dimension heading, or a `=<value>` bucket — NOT dodder's comma-tags.
//
// Adapted from dodder/zz-nvim's organize grammar (cutting-garden#43; vendor now,
// share later — see common/util.js). Reuses the shared metadata (envelope), box
// (espalier interior), and markl (@digest) rule modules.

const markl = require('../common/markl.js');
const metadata = require('../common/metadata.js');
const box = require('../common/box.js');

module.exports = grammar({
  name: 'cutting_garden_organize',

  extras: $ => [],

  rules: {
    source_file: $ => seq(optional($.metadata), repeat($._body_line)),

    _body_line: $ => choice($.heading, $.object_line, $.blank_line),

    blank_line: $ => '\n',

    // A heading ladder line: a `#+` marker then one heading term — a type
    // (`!<type>`, spelling 1), a dimension (`<dim>=`), or a bucket value
    // (`=<value>`). Disambiguated by the term's leading rune.
    heading: $ =>
      seq(
        field('marker', $.heading_marker),
        ' ',
        $._heading_term,
        '\n',
      ),
    heading_marker: $ => token(/#+/),
    _heading_term: $ =>
      choice($.heading_type, $.heading_dimension, $.heading_value),
    heading_type: $ => seq('!', field('name', $.heading_type_name)),
    heading_type_name: $ => token(/[^\s\n]+/),
    heading_dimension: $ => seq(field('name', $.heading_dim_name), '='),
    heading_dim_name: $ => token(/[^\s=\n]+/),
    heading_value: $ => seq('=', field('value', $.heading_value_text)),
    heading_value_text: $ => token(/[^\n]+/),

    // An espalier object line: `- [box] description` (or `%` for a
    // virtual/inferred-type object, preserved from dodder).
    object_line: $ =>
      seq(
        field('prefix', choice('-', '%')),
        ' ',
        $.box,
        optional(seq(' ', field('description', $.description))),
        '\n',
      ),
    description: $ => token(/[^\n]+/),

    ...metadata,
    ...box,
    ...markl,
  },
});
