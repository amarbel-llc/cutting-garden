; Highlights for cutting-garden's organize document dialect (RFC 0015).
; Adapted from dodder/zz-nvim's dodder_organize highlights (cutting-garden#43;
; vendor now, share later).
;
; Capture vocabulary for the native-tags dialect (design G9/G11): every TAG —
; a box's tag atom (bare or quoted), a tag bucket heading, the `_group-by`
; namespace — is `@tag`, distinct from an object id (`@variable`), a field key
; (`@property`), and a field value (`@string`/`@constant`). The planned
; elide-on-hover feature keys on that split (tags are what it hides), so keep
; `@tag` for tags only. A `(…)` meta qualifier is `@attribute` (an annotation on
; a term, not a value); an empty reset heading keeps the marker capture so it
; reads as structure.

; --- hyphence metadata envelope ---
(metadata "---" @punctuation.special)
(description_line "#" @markup.heading.marker)
(description_line text: (description_text) @markup.heading)
(comment_line "%" @comment)
(comment_line text: (comment_text) @comment)
(ref_line "<" @punctuation.special)
(ref_line ref: (reference_id) @constant)

; envelope field lines: `- <key> = <value>` (value is a blob / type / bare)
(field_line "-" @punctuation.special)
(field_line key: (field_key) @property)
(field_line "=" @operator)
(field_bare) @string
(field_type "!" @punctuation.special)
(field_type name: (field_type_name) @type)
(field_blob "@" @punctuation.special)

; the `- _group-by = <spec>` directive: `(tags)` / `project` — the same
; spelling as the `--group-by` flag (a field grouping carries no directive)
(group_by_line "-" @punctuation.special)
(group_by_line key: (field_key) @property)
(group_by_line "=" @operator)
(group_by_line value: (tag_name) @tag)

; a `(…)` meta qualifier (RFC 0014 Qualifier): `(tags)`, `date_due=(month)`
(qualifier ["(" ")"] @punctuation.bracket)
(qualifier name: (qualifier_name) @attribute)

; envelope type line: `! organize-base-v1`
(type_line "!" @punctuation.special)
(type_line type: (type_name) @type)

; --- heading ladder: `# !type` / `# dim=` / `## =value` / `# tag` / `#` ---
(heading marker: (heading_marker) @markup.heading.marker)
(heading_reset marker: (heading_marker) @markup.heading.marker)
(heading_type "!" @punctuation.special)
(heading_type name: (heading_type_name) @type)
(heading_dimension name: (dim_name) @property)
(heading_dimension "=" @operator)
(heading_value "=" @operator)
(heading_value value: (heading_value_text) @constant)
(heading_tag name: (tag_name) @tag)
(heading_tag name: (heading_tag_quoted) @tag)

; --- object lines ---
(object_line "-" @punctuation.special)
(object_line "%" @comment)
(object_line description: (description) @string)

; --- espalier box interior ---
(box ["[" "]"] @punctuation.bracket)
(box id: (box_object_id) @variable)
(box_type "!" @punctuation.special)
(box_type name: (box_ident) @type)
(box_tag name: (box_ident) @tag)
(box_tag name: (box_tag_quoted) @tag)
(box_computed_tag) @comment
(box_field key: (box_ident) @property)
(box_field "=" @operator)
(box_field value: (box_bare_value) @string)
(box_field value: (box_quoted) @string)
(box_escape) @string.escape
(box_blob "@" @punctuation.special)

; --- markl ids (shared) ---
(markl_purpose) @property
(markl_format) @type
(markl_id "-" @punctuation.delimiter)
(markl_data) @string.special
