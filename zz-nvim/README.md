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
  `- _type = !type`, `% provenance`, `! organize-base-v1`);
- the RFC 0015 **heading ladder** — `# !<type>`, a `# <dim>=` dimension heading,
  and a `## =<value>` bucket;
- **espalier object lines** — `- [<id> !<type> <key>=<value> @<digest>] <desc>`,
  with the box interior's id / type / `date_start=…`/`time_start=…` field atoms
  (#47) / digest highlighted distinctly.

Query-string highlighting (the trellis grammar) and a completion/LSP layer
(cutting-garden#219) are out of scope here.

## Layout

```
grammars/common/{box,markl,metadata,util}.js   shared rule modules (vendored)
grammars/organize/{grammar.js, src/parser.c}   the organize grammar (committed parser)
queries/organize/highlights.scm                highlight captures
lua/cutting_garden/{init,health}.lua           filetype registration + folding + checkhealth
plugin/cutting_garden.lua                       auto-setup
```

## Build & install (nix)

```
nix build .#cutting-garden-nvim
```

Add the resulting store path to neovim's `runtimepath` (or via home-manager). It
ships `parser/cutting_garden_organize.so`, so neovim's built-in `vim.treesitter`
loads it with no `nvim-treesitter` dependency. The organize temp file has no
stable extension, so set the filetype programmatically:

```vim
:set filetype=cutting-garden-organize
```

`:checkhealth cutting_garden` verifies the parser and query load.

## Develop

The tree-sitter CLI is not in the devshell (it needs node too); run it via
`nix shell`:

```
cd grammars/organize
nix shell nixpkgs#nodejs nixpkgs#tree-sitter -c tree-sitter generate   # after a grammar.js edit
nix shell nixpkgs#nodejs nixpkgs#tree-sitter -c tree-sitter test        # or: just debug-tree-sitter-corpus
```

Commit the regenerated `grammars/organize/src/` (the build uses the committed
`parser.c`, `generate = false`).
