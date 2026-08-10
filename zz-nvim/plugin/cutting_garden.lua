-- Auto-load guard: run the default setup unless the user opted out to customize.
if vim.g.cutting_garden_no_default_setup then
  return
end
require('cutting_garden').setup()
