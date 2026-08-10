; Highlights for cutting-garden's organize document dialect (RFC 0015).
; Adapted from dodder/zz-nvim's dodder_organize highlights (cutting-garden#43;
; vendor now, share later).

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

; envelope type line: `! organize-base-v1`
(type_line "!" @punctuation.special)
(type_line type: (type_name) @type)

; --- heading ladder: `# !type` / `# dim=` / `## =value` ---
(heading marker: (heading_marker) @markup.heading.marker)
(heading_type "!" @punctuation.special)
(heading_type name: (heading_type_name) @type)
(heading_dimension name: (heading_dim_name) @property)
(heading_dimension "=" @operator)
(heading_value "=" @operator)
(heading_value value: (heading_value_text) @constant)

; --- object lines ---
(object_line "-" @punctuation.special)
(object_line "%" @comment)
(object_line description: (description) @string)

; --- espalier box interior ---
(box ["[" "]"] @punctuation.bracket)
(box id: (box_object_id) @variable)
(box_type "!" @punctuation.special)
(box_type name: (box_ident) @type)
(box_tag (box_ident) @constant)
(box_computed_tag) @comment
(box_field key: (box_ident) @property)
(box_field "=" @operator)
(box_field value: (box_bare_value) @string)
(box_quoted) @string
(box_escape) @string.escape
(box_blob "@" @punctuation.special)

; --- markl ids (shared) ---
(markl_purpose) @property
(markl_format) @type
(markl_id "-" @punctuation.delimiter)
(markl_data) @string.special
