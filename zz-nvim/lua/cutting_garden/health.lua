-- `:checkhealth cutting_garden` — verify the organize parser and its highlights
-- query are installed and load.
local M = {}

function M.check()
  local health = vim.health or require('health')
  health.start('cutting-garden.nvim')

  local ok = pcall(vim.treesitter.language.add, 'cutting_garden_organize')
  if ok then
    health.ok('organize parser (cutting_garden_organize) loads')
  else
    health.error(
      'organize parser not found — is parser/cutting_garden_organize.so on the runtimepath?'
    )
  end

  local q = vim.treesitter.query.get('cutting_garden_organize', 'highlights')
  if q then
    health.ok('highlights query loads')
  else
    health.warn('no highlights query found for cutting_garden_organize')
  end
end

return M
