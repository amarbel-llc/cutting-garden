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

-- The window-local conceal options the plugin sets must follow the organize
-- BUFFER: another buffer later shown in the same window, or a split showing
-- another buffer, keeps the user's own values (PRIOR_*, set below as the
-- global+local defaults before any organize buffer is opened).
local PRIOR_CONCEALLEVEL, PRIOR_CONCEALCURSOR = 1, ''
vim.o.conceallevel = PRIOR_CONCEALLEVEL
vim.o.concealcursor = PRIOR_CONCEALCURSOR

local organize_buf

local function window_conceal(win)
  return {
    vim.api.nvim_get_option_value('conceallevel', { win = win }),
    vim.api.nvim_get_option_value('concealcursor', { win = win }),
  }
end

local function expect_window_conceal(name, win, want)
  local got = window_conceal(win)
  report(
    got[1] == want[1] and got[2] == want[2],
    name,
    { ('want conceallevel=%d concealcursor=%q'):format(want[1], want[2]),
      ('got  conceallevel=%d concealcursor=%q'):format(got[1], got[2]) }
  )
end

-- A plain buffer with its own conceal syntax: `secret` shows as `X` only when
-- the window conceals on the cursor line (the leaked concealcursor=nvic).
local function show_conceal_probe()
  vim.cmd.enew()
  vim.api.nvim_buf_set_lines(0, 0, -1, false, { 'a secret' })
  vim.cmd([[syntax match CgProbe /secret/ conceal cchar=X]])
end

local HEADING_LINES = {
  '# zz-agent_captured',
  '',
  '## =done',
  '# status=',
  '# !caldav-object-vtodo-v1',
  '# "quoted tag"',
  '',
  '- [x.ics] Box',
}

-- A step that applies a colorscheme, then shows the heading document in a
-- fresh organize buffer (the next step reads the screen once nvim has drawn).
local function heading_scheme_setup(_, apply_scheme)
  return function()
    apply_scheme()
    vim.cmd.enew()
    vim.api.nvim_buf_set_lines(0, 0, -1, false, HEADING_LINES)
    vim.bo.filetype = 'cutting-garden-organize'
    keys('8G')
  end
end

-- A step asserting every heading kind's text draws with a style of its own
-- (not the Normal attribute) under the scheme set up just before.
local function heading_scheme_check(scheme)
  return function()
    local plain = vim.fn.screenattr(1, vim.o.columns)
    local function styled(row, col, what)
      report(
        vim.fn.screenattr(row, col) ~= plain,
        ('%s: %s is highlighted'):format(scheme, what),
        { ('row %d col %d has the Normal attribute'):format(row, col) }
      )
    end
    styled(1, 3, 'a tag heading')
    styled(3, 5, 'a value heading')
    styled(4, 3, 'a dimension heading name')
    styled(5, 4, 'a type heading name')
    styled(6, 4, 'a quoted tag heading')
  end
end

local steps = {
  function()
    vim.cmd.edit(vim.fn.fnameescape(fixture))
    -- the fixture sits in the read-only nix store; nothing is written, but
    -- entering insert mode would warn (W10) and stall the screen.
    vim.bo.readonly = false
    local buf = vim.api.nvim_get_current_buf()
    organize_buf = buf
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

  -- window options follow the organize buffer, not the window
  function()
    local win = vim.api.nvim_get_current_win()
    expect_window_conceal('organize window: conceallevel=2 concealcursor=nvic', win, { 2, 'nvic' })
    show_conceal_probe()
    expect_window_conceal(
      'same window, other buffer: the prior conceal options are back',
      win,
      { PRIOR_CONCEALLEVEL, PRIOR_CONCEALCURSOR }
    )
    expect_screen('same window, other buffer: its cursor line is not concealed', { 'a secret', '~' })

    vim.cmd.buffer(organize_buf)
    expect_window_conceal('back to the organize buffer: re-applied', win, { 2, 'nvic' })
    expect_screen('back to the organize buffer: boxes elided again', ALL_ELIDED)

    local listed = vim.api.nvim_create_buf(true, false)
    vim.cmd.buffer(listed)
    expect_window_conceal(
      'same window, an existing never-shown buffer: prior options',
      win,
      { PRIOR_CONCEALLEVEL, PRIOR_CONCEALCURSOR }
    )
    vim.cmd.buffer(organize_buf)
    expect_window_conceal('and back again: re-applied', win, { 2, 'nvic' })

    vim.cmd('split')
    vim.cmd.enew()
    local split = vim.api.nvim_get_current_win()
    expect_window_conceal(
      'split from the organize window, other buffer: prior options',
      split,
      { PRIOR_CONCEALLEVEL, PRIOR_CONCEALCURSOR }
    )
    expect_window_conceal('the organize window beside it keeps its options', win, { 2, 'nvic' })
    vim.cmd('close')

    vim.cmd('vnew')
    local beside = vim.api.nvim_get_current_win()
    expect_window_conceal(
      ':vnew beside the organize window: prior options',
      beside,
      { PRIOR_CONCEALLEVEL, PRIOR_CONCEALCURSOR }
    )
    vim.cmd('close')
    vim.api.nvim_set_current_win(win)
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

  -- a quoted identifier (`["id=<uuid>"]`, how org headlines are addressed) is
  -- an id like any other: elided in box mode, collapsed with the other
  -- metadata in metadata mode, tags kept
  function()
    vim.cmd.enew()
    vim.api.nvim_buf_set_lines(0, 0, -1, false, {
      '- ["id=099a0056-34de-48fe-9d4b-786723568b15"] priority',
      '- ["id=aaaa" work] Tagged',
      '- [x.ics] Plain',
      '',
    })
    vim.bo.filetype = 'cutting-garden-organize'
    report(
      not vim.treesitter.get_parser(0):parse()[1]:root():has_error(),
      'a quoted box id parses without errors'
    )
    keys('4G')
  end,
  function()
    expect_screen('box: a quoted id box is elided like a plain one', {
      '- […] priority',
      '- […] Tagged',
      '- […] Plain',
      '',
      '~',
    })
    cutting_garden.setup({ elide = { mode = 'metadata' } })
    expect_screen('metadata: a quoted id collapses with the metadata; tags stay', {
      '- […] priority',
      '- [… work] Tagged',
      '- […] Plain',
      '',
      '~',
    })
    cutting_garden.setup({ elide = { mode = 'box' } })
  end,

  -- highlighting: every heading and every `%` comment line draws with a style
  -- of its own, whatever the heading ends in or what follows it
  function()
    vim.cmd.enew()
    vim.api.nvim_buf_set_lines(0, 0, -1, false, {
      '% a note',
      '% the next line is a comment too',
      '% and so is this one',
      '- [x.ics] Box',
      '',
      '# zz-agent_captured',
      '- [y.ics] A',
      '# status=',
      '- [z.ics] B',
      '# done.',
      '',
      '- [w.ics] C',
      '',
      '# zz-agent_captured',
      '',
      '% a note under the heading',
      '- ["id=099a0056-34de-48fe-9d4b-786723568b15"] priority',
      '',
    })
    vim.bo.filetype = 'cutting-garden-organize'
    keys('18G')
  end,
  function()
    -- a cell past the end of every line carries the Normal attribute
    local plain = vim.fn.screenattr(1, vim.o.columns)
    local function styled(row, col, what)
      report(
        vim.fn.screenattr(row, col) ~= plain,
        what .. ' is highlighted',
        { ('row %d col %d has the Normal attribute'):format(row, col) }
      )
    end
    local function same(row_a, col_a, row_b, col_b, what)
      report(
        vim.fn.screenattr(row_a, col_a) == vim.fn.screenattr(row_b, col_b),
        what,
        { ('row %d col %d vs row %d col %d differ'):format(row_a, col_a, row_b, col_b) }
      )
    end
    styled(1, 1, 'a comment marker')
    styled(1, 4, 'a comment text')
    styled(2, 4, 'the second comment line text')
    styled(3, 4, 'the third comment line text')
    same(1, 4, 2, 4, 'comment lines 1 and 2 share a style')
    same(2, 4, 3, 4, 'comment lines 2 and 3 share a style')
    styled(6, 1, 'a heading marker')
    styled(6, 3, 'a heading ending in a letter')
    styled(8, 3, 'a heading ending in punctuation (=)')
    styled(10, 3, 'a heading whose name ends in a dot')
    styled(14, 3, 'a heading followed by a comment line and a quoted-id box')
    styled(16, 3, 'a comment line under a heading')
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

  -- heading text under real and minimal colorschemes. Last, because switching
  -- colorschemes and emptying groups cannot be undone.
  heading_scheme_setup('solarized8 (dark)', function()
    vim.o.termguicolors = true
    vim.o.background = 'dark'
    vim.opt.runtimepath:append(assert(os.getenv('CG_NVIM_SOLARIZED8'), 'CG_NVIM_SOLARIZED8 unset'))
    vim.cmd('colorscheme solarized8')
  end),
  heading_scheme_check('solarized8 (dark)'),
  heading_scheme_setup('solarized8 (light)', function()
    vim.o.background = 'light'
    vim.cmd('colorscheme solarized8')
  end),
  heading_scheme_check('solarized8 (light)'),
  -- a minimal colorscheme: the semantic captures a heading's text uses carry
  -- no style at all (an empty definition, which nvim treats as defined, so the
  -- built-in default links do not come back)
  heading_scheme_setup('minimal', function()
    vim.cmd('highlight clear')
    for _, group in ipairs({ '@tag', '@constant', '@property', '@type', '@operator' }) do
      vim.api.nvim_set_hl(0, group, {})
    end
  end),
  heading_scheme_check('minimal (@tag/@constant/@property/@type/@operator empty)'),
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
