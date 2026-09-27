# cutting-garden.nvim

Neovim tree-sitter syntax highlighting for cutting-garden **organize** documents
(RFC 0015 / FDR 0023; cutting-garden#43) — the hyphence-envelope + heading-ladder
+ espalier-box text that `cg organize` produces and `$EDITOR` edits (#50).

Ported from [dodder](../../dodder)'s `zz-nvim` (the same grammar family — shared
hyphence envelope, espalier box, piggy markl-ids). This is the **vendor-now,
share-later** interim: the grammar modules under `grammars/common/` are copies;
extracting shared tree-sitter grammar homes (mirroring the `marklid.peg` /
`hyphence-content.peg` PEG composition) is a tracked #43 followup.

## What it highlights

One grammar, `cutting_garden_organize`:

- the `---`-fenced hyphence **envelope** (`- _base = @digest`, `- _anchor`,
  `- _type = !type`, `- _group-by = (tags)` / `project` — the tag-hoisting
  groupings only; a field grouping's heading is its spelling — `% provenance`,
  `! organize-base-v1`);
- the RFC 0015 **heading ladder** — `# !<type>`, a `# <dim>=` /
  `# date_due=(month)` dimension heading, a `## =<value>` bucket, a **tag
  bucket** (`# work`, `# -client`, `# "_ inbox"`), and an empty **reset**
  heading (`#`, `##`); heading depth is structure-only, so a `##`-rooted
  document parses like a `#`-rooted one;
- **espalier object lines** —
  `- [<id> !<type> <tag> "<quoted tag>" <key>=<value> @<digest>] <desc>`, with
  the box interior's id / type / bare and quoted **tag atoms** (native tags
  design G9: a bare word is always a tag) / `date_start=…`/`time_start=…` field
  atoms (#47) / digest highlighted distinctly — tags carry their own `@tag`
  capture.

## Box eliding

Box interiors are **elided at rest and revealed under the cursor**
(cutting-garden#253, the vim-pandoc concealing UX): away from the cursor,
`- [42.ics bug organize-wrap milestone=v0.3] Wrapped boxes` reads
`- […] Wrapped boxes`. Only the interior between `[` and `]` is ever hidden —
never the description after `]`.

```lua
-- the defaults; setup() merges over the current options, so calling it before
-- or after plugin/'s bare auto-setup keeps your levers
require('cutting_garden').setup({
  elide = {
    mode = 'box', -- 'box' | 'metadata' | 'off'
    char = '…',   -- any single character
  },
})
```

Modes:

- `box` (default) — the whole interior collapses to `char`: `- […] Title`. A
  wrapped box (#261) collapses to one row: its later rows are hidden and its
  `] trailer` is drawn after the ellipsis.
- `metadata` — only the non-tag atoms collapse: the id (or `+temp` id),
  `!type`, `name=value` fields and `@digest`, each run of adjacent ones (with
  the blanks between) into one `char`; tag atoms — bare, quoted, and `%computed`
  — stay visible: `- [… project-client-acme work …] Acme retainer`. A wrapped
  box keeps its line structure (one ellipsis per run per row).
- `off` — nothing is hidden.

Reveal rules: a box is shown verbatim while the cursor is on **any** of its
rows (a wrapped box is revealed whole); in insert mode only the box under the
cursor is revealed; in visual mode every box the selection touches is. A box
with a parse error (an unbalanced `[` mid-edit) is never hidden. The ellipsis
uses the box bracket highlight (`@punctuation.bracket`). The plugin sets
`conceallevel=2` and `concealcursor=nvic` in organize windows — the reveal
policy is the plugin's, not nvim's cursor-line rule.

`:CgElideToggle` flips the current buffer between `off` and the configured
mode. Eliding needs nvim 0.11+ (extmark `conceal_lines`); older versions keep
boxes verbatim, and `:checkhealth cutting_garden` reports it.

The corpus under `grammars/organize/test/corpus/` mirrors the
`zz-tests_bats/organize_*.bats` vectors and is the dialect's conformance vector
(design G11); `just test-grammar-corpus` runs it in the merge gate.

Query-string highlighting (the trellis grammar) and a completion/LSP layer
(cutting-garden#219) are out of scope here.

## Layout

```
grammars/common/{box,markl,metadata,util}.js   shared rule modules (vendored)
grammars/organize/{grammar.js, src/parser.c}   the organize grammar (committed parser)
grammars/organize/test/corpus/                 the dialect's conformance corpus (mirrors zz-tests_bats/organize_*.bats)
queries/cutting_garden_organize/highlights.scm highlight captures (dir must match the parser language name)
lua/cutting_garden/{init,health}.lua           filetype registration + folding + checkhealth
lua/cutting_garden/elide.lua                   box eliding (#253)
plugin/cutting_garden.lua                       auto-setup
test/{elide_spec.lua, fixtures/}               the eliding golden-screen spec (checks.nvim-elide)
```

## Build & install (nix)

```
nix build .#cutting-garden-nvim
```

Add the resulting store path to neovim's `runtimepath` (or via home-manager). It
ships `parser/cutting_garden_organize.so`, so neovim's built-in `vim.treesitter`
loads it with no `nvim-treesitter` dependency. The interactive `cg organize`
buffer is a temp file named `cg-organize-*.txt`, which the plugin auto-detects
(`vim.filetype.add`) — highlighting fires with no manual step. For any other
buffer, set the filetype by hand:

```vim
:set filetype=cutting-garden-organize
```

`:checkhealth cutting_garden` verifies the parser and query load.

## Develop

The devshell carries the flake-pinned tree-sitter CLI + nodejs — the same
`pkgs.tree-sitter` that compiles the parser for the plugin and runs the corpus
check — so regeneration is deterministic:

```
just codemod-generate-tree-sitter   # after a grammar.js edit (tree-sitter generate)
just test-grammar-corpus            # the sandboxed checks.grammar-corpus (merge gate)
just debug-tree-sitter-corpus -u    # rewrite the expected trees in place; review the diff
just test-nvim-elide                # the sandboxed checks.nvim-elide golden screens (merge gate)
```

Commit the regenerated `grammars/organize/src/` (the build uses the committed
`parser.c`, `generate = false`).
