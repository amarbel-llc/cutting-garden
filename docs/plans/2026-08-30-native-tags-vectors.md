# Native tags — vector index (G# → bats test)

Review checklist for `2026-08-30-native-tags-design.md`. Every row is a
whole-document vector (`assert_output - <<-EOM`) unless marked golden. Test
names are filled in as each slice lands; `—` = not yet written.

| G# | decision | slice | test (file:function) |
|---|---|---|---|
| G1 | bare atoms after id/type; `_tag-atoms = leading` default | 2 | — |
| G1 | `_tag-atoms = trailing` | 2 | — |
| G1 | `_tag-atoms = none` | 2 | — |
| G2 | tag-grouped: strip only `Via`, siblings stay | 2 | — |
| G2 | `_tag-strip = none` | 2 | — |
| G3 | levers omitted at default; config default; doc wins | 2 | — |
| G4 | `fmt-organize` regenerates + rewrites `_base` | 3 | — |
| G4 | `fmt-organize` refuses on unapplied edits | 3 | — |
| G4 | `fmt-organize` never emits reset headings | 3 | — |
| G6 | `categoriesCodec.Format` produces the tag set (Go unit) | 2 | — |
| G7 | box atom added → membership add (exact) | 2 | — |
| G7 | box atom removed → membership remove | 2 | — |
| G7 | cross-appearance tag disagreement → conflict | 2 | — |
| G7 | `%`-atom edit rejected | 2 | — |
| G8 | `list -format espalier` == organize boxes | 4 | — |
| G8 | JSON `tags` array | 4 | — |
| G8 | mesa table (golden) | 4 | — |
| G9 | bare token in box is a tag, even if it names a field | 1 | — |
| G9 | non-ground interior is a loud bad request | 1 | — |
| G9 | quoted tag (`"_ inbox"`) round-trips in box and heading | 1 | — |
| G10 | `--group-by (tags)` → `# <tag>` buckets, no dim heading | 1 | — |
| G10 | `--group-by project` → `# -client` rollup | 1 | — |
| G10 | `--group-by status=` → `# status=` / `## =value` | 1 | — |
| G10 | `--group-by date_due=(month)` | 1 | — |
| G10 | legacy `date_due:month` / `categories` / `categories/project` rejected with hint | 1 | — |
| G10 | empty-namespace bare name → error suggesting `name=` | 1 | — |
| G10 | qualifier in query position is reserved (bad request) | 1 | — |
| G10 | depth normalization | 1 | — |
| G10 | empty-heading reset (`##` pops one; `#` → ungrouped; deeper no-op) | 1 | — |
| G12 | `describe_node_types` reports `tag_set` | 4 | — |
| G13 | hand-written bare token round-trips through parse→write | 1 | — |
| G16 | existing lanes converted to whole-document vectors | 1 | all `organize*.bats` |
