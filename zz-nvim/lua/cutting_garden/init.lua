-- cutting-garden.nvim — tree-sitter syntax highlighting for cutting-garden
-- organize documents (RFC 0015 / FDR 0023; cutting-garden#43).
--
-- Minimal by design (the read-side slice): register the shipped parser for the
-- organize filetype, start tree-sitter highlighting, and fold by heading depth.
-- The behavioral ftplugin (gf-to-object, type actions, format) and the LSP
-- completion layer (cutting-garden#219) are deliberately out of scope here.

local M = {}

-- The parser language name (parser/cutting_garden_organize.so) and the filetype
-- for organize documents. Organize temp files have no stable extension (the
-- interactive $EDITOR round-trip, #50, writes to a temp file), so the filetype
-- is set programmatically: `:set filetype=cutting-garden-organize`.
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
