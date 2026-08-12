-- cutting-garden.nvim — tree-sitter syntax highlighting for cutting-garden
-- organize documents (RFC 0015 / FDR 0023; cutting-garden#43).
--
-- Minimal by design (the read-side slice): register the shipped parser for the
-- organize filetype, start tree-sitter highlighting, and fold by heading depth.
-- The behavioral ftplugin (gf-to-object, type actions, format) and the LSP
-- completion layer (cutting-garden#219) are deliberately out of scope here.

local M = {}

-- The parser language name (parser/cutting_garden_organize.so) and the filetype
-- for organize documents. The interactive $EDITOR round-trip (#50) writes to a
-- temp file named `cg-organize-*.txt` (os.CreateTemp), which setup() auto-detects
-- via vim.filetype.add — so highlighting fires with no manual step. Any other
-- buffer can still be set by hand: `:set filetype=cutting-garden-organize`.
local LANG = 'cutting_garden_organize'
local FT = 'cutting-garden-organize'

-- foldexpr: a heading line's fold level is its `#` count; other lines inherit.
function M.foldexpr()
  local hashes = vim.fn.getline(vim.v.lnum):match('^(#+)%s')
  if hashes then
    return '>' .. #hashes
  end
  return '='
end

function M.setup()
  vim.treesitter.language.register(LANG, FT)

  -- Auto-detect the interactive organize buffer. The $EDITOR round-trip (#50)
  -- writes os.CreateTemp("", "cg-organize-*.txt"), e.g. /tmp/cg-organize-42.txt,
  -- so match that basename: highlighting then fires without a manual `:set
  -- filetype`. The `.txt` suffix keeps other tools treating the file as text; the
  -- explicit priority makes this specific pattern win over any generic `*.txt`
  -- rule (filetype patterns resolve by priority, then length).
  vim.filetype.add({
    pattern = {
      ['.*/cg%-organize%-.*%.txt'] = { FT, { priority = 100 } },
    },
  })

  local group =
    vim.api.nvim_create_augroup('cutting_garden_organize', { clear = true })
  vim.api.nvim_create_autocmd('FileType', {
    group = group,
    pattern = FT,
    callback = function(args)
      pcall(vim.treesitter.start, args.buf, LANG)
      vim.opt_local.foldmethod = 'expr'
      vim.opt_local.foldexpr = "v:lua.require'cutting_garden'.foldexpr()"
      vim.opt_local.commentstring = '%%s'
    end,
  })
end

return M
