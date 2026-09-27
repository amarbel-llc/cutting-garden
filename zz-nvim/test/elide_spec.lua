-- Golden-screen spec for organize box eliding (cutting-garden#253), run by
-- checks.nvim-elide (flake.nix) / `just test-nvim-elide`:
--
--   CG_NVIM_PLUGIN=<plugin-dir> CG_NVIM_FIXTURE=<cg-organize-*.txt> \
--     nvim --clean --headless -c 'luafile elide_spec.lua'
--
-- Each step puts the editor in a state (cursor row, mode, options), forces a
-- full redraw and compares the top screen rows (trailing blanks trimmed)
-- cell-by-cell via screenstring(). Steps run from timers so the MAIN LOOP
-- processes the fed keys between them — entering insert/visual mode and
-- firing the real CursorMoved/ModeChanged autocmds (`nvim -l` cannot: its
-- script never yields to the main loop). Output is TAP on stdout; the exit
-- code is 0 only when every assertion passed.

local plugin_dir = assert(os.getenv('CG_NVIM_PLUGIN'), 'CG_NVIM_PLUGIN unset')
local fixture = assert(os.getenv('CG_NVIM_FIXTURE'), 'CG_NVIM_FIXTURE unset')

local STEP_DELAY_MS = 20
local WATCHDOG_MS = 60000

vim.o.swapfile = false
vim.o.foldenable = false
vim.o.showmode = false -- keep `-- INSERT --` out of the TAP stream
vim.opt.runtimepath:prepend(plugin_dir)

local cutting_garden = require('cutting_garden')
cutting_garden.setup()

local test_count, failures = 0, 0

local function say(line)
  io.stdout:write(line .. '\n')
end

local function report(ok, name, detail)
  test_count = test_count + 1
  say(('%s %d - %s'):format(ok and 'ok' or 'not ok', test_count, name))
  if not ok then
    failures = failures + 1
    for _, line in ipairs(detail or {}) do
      say('  # ' .. line)
    end
  end
end

local function finish(code)
  say(('1..%d'):format(test_count))
  vim.cmd('cquit ' .. code)
end

local function screen_rows(count)
  vim.cmd('redraw!')
  local rows = {}
  for row = 1, count do
    local cells = {}
    for col = 1, vim.o.columns do
      cells[col] = vim.fn.screenstring(row, col)
    end
    rows[row] = (table.concat(cells):gsub('%s+$', ''))
  end
  return rows
end

local function expect_screen(name, want)
  local got = screen_rows(#want)
  local detail = {}
  for row = 1, #want do
    if got[row] ~= want[row] then
      table.insert(detail, ('row %d: want %q'):format(row, want[row]))
      table.insert(detail, ('row %d:  got %q'):format(row, got[row]))
    end
  end
  report(#detail == 0, name .. ' [mode ' .. vim.api.nvim_get_mode().mode .. ']', detail)
end

-- Queued as typed keys: the main loop runs them after this step returns.
local function keys(sequence)
  vim.api.nvim_feedkeys(vim.keycode(sequence), 'n', false)
end

local HEADER = { '---', '! organize-base-v1', '---', '', '# work', '' }

local function with_header(rows)
  return vim.list_extend(vim.deepcopy(HEADER), rows)
end

local VERBATIM = with_header({
  '- [nsA.ics project-client-acme work date_due=2026-09-01] Acme retainer',
  '- [nsD.ics other] Loose idea',
  '- [lit2.ics',
  '    work-x',
  '    location=Bank] Read book',
  '- [t1.ics !caldav-object-vtodo-v1 urgent %overdue] Typed',
  '- [field4.ics] Someday idea',
  '~',
})

local ALL_ELIDED = with_header({
  '- […] Acme retainer',
  '- […] Loose idea',
  '- […] Read book',
  '- […] Typed',
  '- […] Someday idea',
  '~',
})

local steps = {
  function()
    vim.cmd.edit(vim.fn.fnameescape(fixture))
    -- the fixture sits in the read-only nix store; nothing is written, but
    -- entering insert mode would warn (W10) and stall the screen.
    vim.bo.readonly = false
    local buf = vim.api.nvim_get_current_buf()
    report(
      vim.bo[buf].filetype == 'cutting-garden-organize',
      'cg-organize-*.txt is detected as the organize filetype',
      { 'filetype: ' .. vim.bo[buf].filetype }
    )
    report(
      not vim.treesitter.get_parser(buf):parse()[1]:root():has_error(),
      'the fixture parses without errors'
    )
    report(
      vim.wo.conceallevel == 2 and vim.wo.concealcursor == 'nvic',
      'attach sets conceallevel=2 concealcursor=nvic',
      { ('conceallevel=%d concealcursor=%s'):format(vim.wo.conceallevel, vim.wo.concealcursor) }
    )
  end,

  -- box mode (the default)
  function()
    expect_screen(
      'box: every box elided when the cursor is off them; a wrapped box collapses to one row',
      ALL_ELIDED
    )
    keys('7G')
  end,
  function()
    expect_screen(
      'box: the cursor box is shown verbatim',
      with_header({
        '- [nsA.ics project-client-acme work date_due=2026-09-01] Acme retainer',
        '- […] Loose idea',
        '- […] Read book',
        '- […] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('10G')
  end,
  function()
    expect_screen(
      'box: a wrapped box is revealed whole with the cursor on its 2nd line',
      with_header({
        '- […] Acme retainer',
        '- […] Loose idea',
        '- [lit2.ics',
        '    work-x',
        '    location=Bank] Read book',
        '- […] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('8Gi')
  end,
  function()
    expect_screen(
      'box: insert mode reveals only the current box',
      with_header({
        '- […] Acme retainer',
        '- [nsD.ics other] Loose idea',
        '- […] Read book',
        '- […] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('<Esc>8GVj')
  end,
  function()
    expect_screen(
      'box: a visual selection reveals every box it touches',
      with_header({
        '- […] Acme retainer',
        '- [nsD.ics other] Loose idea',
        '- [lit2.ics',
        '    work-x',
        '    location=Bank] Read book',
        '- […] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('<Esc>1G')
  end,

  -- metadata mode
  function()
    cutting_garden.setup({ elide = { mode = 'metadata' } })
    expect_screen(
      'metadata: ids, !types and fields collapse per run; tags (bare and %computed) stay',
      with_header({
        '- [… project-client-acme work …] Acme retainer',
        '- [… other] Loose idea',
        '- […',
        '    work-x',
        '    …] Read book',
        '- [… urgent %overdue] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('7G')
  end,
  function()
    expect_screen(
      'metadata: the cursor box is shown verbatim',
      with_header({
        '- [nsA.ics project-client-acme work date_due=2026-09-01] Acme retainer',
        '- [… other] Loose idea',
        '- […',
        '    work-x',
        '    …] Read book',
        '- [… urgent %overdue] Typed',
        '- […] Someday idea',
        '~',
      })
    )
    keys('1G')
  end,

  -- off mode, the char lever, :CgElideToggle
  function()
    cutting_garden.setup({ elide = { mode = 'off' } })
    expect_screen('off: every box is shown verbatim', VERBATIM)

    cutting_garden.setup({ elide = { mode = 'box', char = '*' } })
    expect_screen(
      'char: the ellipsis character is a lever',
      with_header({
        '- [*] Acme retainer',
        '- [*] Loose idea',
        '- [*] Read book',
        '- [*] Typed',
        '- [*] Someday idea',
        '~',
      })
    )
    cutting_garden.setup({ elide = { char = '…' } })

    vim.cmd('CgElideToggle')
    expect_screen(':CgElideToggle turns eliding off in the buffer', VERBATIM)
    vim.cmd('CgElideToggle')
    expect_screen(':CgElideToggle turns it back on with the configured mode', ALL_ELIDED)
  end,

  -- an unbalanced box (mid-edit) is left unconcealed
  function()
    vim.cmd.enew()
    vim.api.nvim_buf_set_lines(0, 0, -1, false, { '- [b.ics] B', '- [a.ics work', '' })
    vim.bo.filetype = 'cutting-garden-organize'
    keys('3G')
  end,
  function()
    expect_screen(
      'an unbalanced box is not concealed; a well-formed box before it is',
      { '- […] B', '- [a.ics work', '', '~' }
    )
  end,

  -- invalid levers are rejected
  function()
    report(
      not pcall(cutting_garden.setup, { elide = { mode = 'tags' } }),
      'an unknown elide.mode is rejected'
    )
    report(
      not pcall(cutting_garden.setup, { elide = { char = '..' } }),
      'a multi-character elide.char is rejected'
    )
  end,
}

local function run(index)
  if index > #steps then
    finish(failures == 0 and 0 or 1)
    return
  end
  local ok, err = pcall(steps[index])
  if not ok then
    report(false, ('step %d raised'):format(index), { tostring(err) })
    finish(1)
    return
  end
  vim.defer_fn(function()
    run(index + 1)
  end, STEP_DELAY_MS)
end

vim.defer_fn(function()
  report(false, 'watchdog: the spec did not finish', { ('%d ms'):format(WATCHDOG_MS) })
  finish(2)
end, WATCHDOG_MS)

vim.schedule(function()
  run(1)
end)
