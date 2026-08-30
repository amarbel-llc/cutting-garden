// tree-sitter grammar for cutting-garden's organize document dialect
// (RFC 0015 / FDR 0023).
//
//   ---
//   % generated: `cg organize -group-by status= caldav:.../cal/`
//   - _base = @blake2b256-9ft3x
//   - _anchor = caldav:.../cal/
//   - _type = !caldav-object-vtodo-v1
//   ! organize-base-v1
//   ---
//
//   # status=
//   ## =COMPLETED
//   - [task1.ics work "_ inbox" date_start=2026-08-15] Buy milk
//
// A hyphence metadata envelope, then a body of heading-ladder lines and espalier
// object lines. Headings are the RFC 0015 ladder, spelled with trellis terms
// (RFC 0014; native tags design G9/G10/G13): `# !<type>` (spelling 1), a
// `<dim>=` / `<dim>=(<granularity>)` dimension heading, a `=<value>` bucket, or
// a TAG bucket — bare (`# work`, `# -client`) or quoted (`# "_ inbox"`) — the
// `(tags)` / namespace groupings emit. An EMPTY heading (`#`, `##`) is a
// context reset (`heading_reset`), never an error. Heading depth is
// structure-only: the marker is `#+` at any depth, so a `##`-rooted document
// parses like a `#`-rooted one.
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

    _body_line: $ =>
      choice($.heading, $.heading_reset, $.object_line, $.blank_line),

    blank_line: $ => '\n',

    // A heading ladder line: a `#+` marker then one heading term — a type
    // (`!<type>`, spelling 1), a dimension (`<dim>=`, optionally qualified
    // `<dim>=(month)`), a bucket value (`=<value>`), or a tag bucket (bare or
    // quoted). Disambiguated by the term's leading rune, except bare tag vs
    // dimension, which the parser splits on the `=` that follows the word.
    heading: $ =>
      seq(
        field('marker', $.heading_marker),
        $._heading_space,
        $._heading_term,
        '\n',
      ),
    // An empty heading — the marker alone (trailing blanks tolerated) — is a
    // context RESET (design G10): it pops the heading context at its depth and
    // deeper. A distinct node kind so highlighters can mark it and so it never
    // parses as an error. Shares `_heading_space` with `heading` so the parser
    // (not the lexer) decides on the token after the blanks: `\n` → reset.
    heading_reset: $ =>
      seq(field('marker', $.heading_marker), optional($._heading_space), '\n'),
    heading_marker: $ => token(/#+/),
    _heading_space: $ => token(/[ \t]+/),
    _heading_term: $ =>
      choice(
        $.heading_type,
        $.heading_dimension,
        $.heading_value,
        $.heading_tag,
      ),
    heading_type: $ => seq('!', field('name', $.heading_type_name)),
    heading_type_name: $ => token(/[^\s\n]+/),
    // `<dim>=` or `<dim>=(<granularity>)` — the qualifier is a value hole with
    // a meta qualifier (RFC 0014 `Value <- Qualifier / …`).
    heading_dimension: $ =>
      seq(
        field('name', alias($._term_word, $.heading_dim_name)),
        '=',
        optional(field('qualifier', $.qualifier)),
      ),
    heading_value: $ => seq('=', field('value', $.heading_value_text)),
    heading_value_text: $ => token(/[^\n]+/),
    // A tag bucket (`(tags)` / namespace groupings): a bare trellis Ident
    // (`work`, `-client`) or a trellis String when the tag carries whitespace
    // or a reserved rune (`"_ inbox"`). The quoted form reuses the box's
    // string rule under its own node name.
    heading_tag: $ =>
      field(
        'name',
        choice(
          alias($._term_word, $.heading_tag_name),
          alias($.box_quoted, $.heading_tag_quoted),
        ),
      ),

    // A trellis Ident (RFC 0014 / hyphence-content.peg's Reserved set,
    // mirrored by internal/trellis/lex.go): runs until whitespace or a reserved
    // rune `[]^=,!@<>*$~%#"'()`; `-`, `/`, `.` are admitted, so `-client`,
    // `date_due`, and `project-client-acme` are single words.
    _term_word: $ => token(/[^\s\[\]^=,!@<>*$~%#"'()]+/),

    // A `(…)` meta qualifier (RFC 0014 `Qualifier <- '(' Ident ')'`, design
    // G10): `(tags)` as a whole `_group-by` value, `(month)` as a date
    // dimension's granularity.
    qualifier: $ => seq('(', field('name', $.qualifier_name), ')'),
    qualifier_name: $ => token(/[^\s()]+/),

    // The `- _group-by = <spec>` envelope directive carries the SAME spelling
    // as the `--group-by` flag (G10): `(tags)` (a qualifier), `project` (a bare
    // tag namespace), or a field grouping `status=` / `date_due=(month)` —
    // the last two are not emitted today (a field grouping's heading IS its
    // spelling) but are admitted so a hand-written envelope still parses.
    // Keyed by the literal `_group-by` so the generic `field_line`'s greedy bare
    // value does not swallow the structure.
    group_by_line: $ =>
      seq(
        '-',
        ' ',
        field('key', alias('_group-by', $.field_key)),
        ' ',
        '=',
        ' ',
        field('value', $._group_by_spec),
        '\n',
      ),
    _group_by_spec: $ =>
      choice($.qualifier, $.group_by_field, $.group_by_tag),
    group_by_field: $ =>
      seq(
        field('name', alias($._term_word, $.heading_dim_name)),
        '=',
        optional(field('qualifier', $.qualifier)),
      ),
    group_by_tag: $ => field('name', alias($._term_word, $.heading_tag_name)),

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

    // Overrides the shared envelope's line set (spread above, so this wins) to
    // admit the organize-specific `_group-by` directive ahead of the generic
    // field line.
    _metadata_line: $ =>
      choice(
        $.description_line,
        $.group_by_line,
        $.field_line,
        $.blob_line,
        $.type_line,
        $.ref_line,
        $.comment_line,
      ),
  },
});
