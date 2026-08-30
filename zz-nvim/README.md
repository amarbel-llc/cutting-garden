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
  `- _type = !type`, `- _group-by = (tags)` / `project`, `% provenance`,
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
  capture so the planned elide-on-hover feature can key on them.

The corpus under `grammars/organize/test/corpus/` mirrors the
`zz-tests_bats/organize_*.bats` vectors and is the dialect's conformance vector
(design G11); `just test-grammar-corpus` runs it in the merge gate.

Query-string highlighting (the trellis grammar) and a completion/LSP layer
(cutting-garden#219) are out of scope here.

## Layout

```
grammars/common/{box,markl,metadata,util}.js   shared rule modules (vendored)
grammars/organize/{grammar.js, src/parser.c}   the organize grammar (committed parser)
queries/cutting_garden_organize/highlights.scm highlight captures (dir must match the parser language name)
lua/cutting_garden/{init,health}.lua           filetype registration + folding + checkhealth
plugin/cutting_garden.lua                       auto-setup
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

The tree-sitter CLI is not in the devshell (it needs node too); run it via
`nix shell`:

```
just codemod-generate-tree-sitter   # after a grammar.js edit (tree-sitter generate)
just test-grammar-corpus            # tree-sitter test
just debug-tree-sitter-corpus -u    # rewrite the expected trees; review the diff
```

Commit the regenerated `grammars/organize/src/` (the build uses the committed
`parser.c`, `generate = false`).
