-- Elide organize box interiors at rest, reveal them under the cursor
-- (cutting-garden#253) — the vim-pandoc concealing UX for espalier boxes.
--
-- Modes (`require('cutting_garden').setup({ elide = { mode = … } })`):
--
--   box       the whole interior, `[` … `]` exclusive, collapses to `char`:
--             `- […] Title`. A wrapped box (#261) collapses to its first line
--             (its later rows are hidden with `conceal_lines`, and the last
--             row's `] trailer` is re-drawn inline after the ellipsis).
--   metadata  only the non-tag atoms collapse — the id (object or temp),
--             `!type`, `name=value` fields, `@blob` — each run of adjacent
--             elided atoms (and the blanks between them) into one `char`,
--             per line; tag atoms (bare, quoted, `%computed`) stay visible:
--             `- [… work …] Title`. A wrapped box keeps its line structure.
--   off       nothing is elided.
--
-- Reveal policy (Lua owns it; the window runs conceallevel=2 with
-- concealcursor=nvic so nvim itself never un-conceals the cursor line): a box
-- is shown verbatim when the cursor is on ANY of its rows; in visual/select
-- mode every box the selection's rows touch is shown. Boxes whose subtree
-- carries a parse error (an unbalanced `[` mid-edit) are never concealed.
--
-- Cost: marks cover only the rows visible in the buffer's windows plus a
-- margin of three window heights (so rows pulled up by `conceal_lines` are
-- already marked). A cursor move re-marks only when the revealed box set or
-- the covered range changes; a text change re-marks after a short debounce.

local M = {}

local LANG = 'cutting_garden_organize'
local NS = vim.api.nvim_create_namespace('cutting_garden_elide')
local BRACKET_HL = '@punctuation.bracket.' .. LANG
local DESCRIPTION_HL = '@string.' .. LANG
local TEXT_CHANGED_DEBOUNCE_MS = 30

local MODES = { box = true, metadata = true, off = true }

-- The box atoms `metadata` mode elides. Tags — `box_tag` (bare or quoted) and
-- `box_computed_tag` (`%name`, a display-only derived tag) — stay visible.
local METADATA_ATOMS = {
  box_object_id = true,
  box_temp_id = true,
  box_type = true,
  box_field = true,
  box_blob = true,
}

M.defaults = { mode = 'box', char = '…' }
M.options = vim.deepcopy(M.defaults)

-- Per attached buffer: { mode = <toggle override or nil>, key = <last marked
-- state>, gen = <debounce generation> }.
local attached = {}

local box_query

local function get_box_query()
  box_query = box_query or vim.treesitter.query.parse(LANG, '(box) @box')
  return box_query
end

local function validate(opts)
  if not MODES[opts.mode] then
    error(
      ("cutting_garden: elide.mode must be 'box', 'metadata' or 'off', got %s"):format(
        vim.inspect(opts.mode)
      ),
      0
    )
  end
  if type(opts.char) ~= 'string' or vim.fn.strchars(opts.char) ~= 1 then
    error(
      ('cutting_garden: elide.char must be a single character, got %s'):format(
        vim.inspect(opts.char)
      ),
      0
    )
  end
end

-- Merges opts over the CURRENT options (not the defaults), so the plugin/
-- auto-setup's bare setup() after a user's setup({ elide = … }) keeps the
-- user's levers.
function M.configure(opts)
  local merged = vim.tbl_extend('force', M.options, opts or {})
  validate(merged)
  M.options = merged
  for buf in pairs(attached) do
    M.refresh(buf, true)
  end
end

local function buffer_mode(buf)
  local s = attached[buf]
  return (s and s.mode) or M.options.mode
end

local function set_window_options(win)
  vim.api.nvim_set_option_value('conceallevel', 2, { scope = 'local', win = win })
  vim.api.nvim_set_option_value('concealcursor', 'nvic', { scope = 'local', win = win })
end

local function is_visual(mode)
  local c = mode:sub(1, 1)
  return c == 'v' or c == 'V' or c == '\22' or c == 's' or c == 'S' or c == '\19'
end

-- The 0-based row span whose boxes are revealed: the cursor row, or the rows
-- of the visual selection. nil when the current window shows another buffer.
local function reveal_rows(buf)
  local win = vim.api.nvim_get_current_win()
  if vim.api.nvim_win_get_buf(win) ~= buf then
    return nil
  end
  local cursor = vim.api.nvim_win_get_cursor(win)[1] - 1
  if is_visual(vim.api.nvim_get_mode().mode) then
    local other = vim.fn.line('v') - 1
    return math.min(cursor, other), math.max(cursor, other)
  end
  return cursor, cursor
end

-- The 0-based [first, last) row range to mark: every window's visible rows
-- plus a margin of three window heights on each side.
local function marked_range(buf)
  local first, last
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do
    local margin = 3 * vim.api.nvim_win_get_height(win)
    local top = vim.fn.line('w0', win) - 1 - margin
    local bottom = vim.fn.line('w$', win) + margin
    first = math.min(first or top, top)
    last = math.max(last or bottom, bottom)
  end
  if not first then
    return nil
  end
  return math.max(first, 0), math.min(last, vim.api.nvim_buf_line_count(buf))
end

local function line_at(buf, row)
  return vim.api.nvim_buf_get_lines(buf, row, row + 1, false)[1] or ''
end

local function conceal(buf, row, start_col, end_col)
  vim.api.nvim_buf_set_extmark(buf, NS, row, start_col, {
    end_row = row,
    end_col = end_col,
    conceal = M.options.char,
    hl_group = BRACKET_HL,
  })
end

local function mark_box_whole(buf, node)
  local start_row, start_col, end_row, end_col = node:range()
  if start_row == end_row then
    conceal(buf, start_row, start_col + 1, end_col - 1)
    return
  end
  -- A wrapped box: `- [<first-line interior>` shows as `- […`, the later rows
  -- vanish, and the last row's `] trailer` is re-drawn inline after the `…`.
  local first = line_at(buf, start_row)
  conceal(buf, start_row, start_col + 1, #first)
  local blanks, text = line_at(buf, end_row):sub(end_col + 1):match('^(%s*)(.*)$')
  local chunks = { { ']', BRACKET_HL } }
  if blanks ~= '' then
    table.insert(chunks, { blanks })
  end
  if text ~= '' then
    table.insert(chunks, { text, DESCRIPTION_HL })
  end
  vim.api.nvim_buf_set_extmark(buf, NS, start_row, #first, {
    virt_text = chunks,
    virt_text_pos = 'inline',
  })
  for row = start_row + 1, end_row do
    vim.api.nvim_buf_set_extmark(buf, NS, row, 0, { conceal_lines = '' })
  end
end

-- Collapses each same-row run of METADATA_ATOMS (plus the blanks between
-- them) to one ellipsis. A multi-row atom (a wrapped quoted value) contributes
-- one segment per row.
local function mark_box_metadata(buf, node)
  local segments = {}
  for child in node:iter_children() do
    if child:named() and METADATA_ATOMS[child:type()] then
      local start_row, start_col, end_row, end_col = child:range()
      for row = start_row, end_row do
        local line = line_at(buf, row)
        local from = row == start_row and start_col or (line:find('%S') or 1) - 1
        local to = row == end_row and end_col or #line
        local previous = segments[#segments]
        if
          previous
          and previous.row == row
          and line:sub(previous.to + 1, from):match('^%s*$')
        then
          previous.to = to
        else
          table.insert(segments, { row = row, from = from, to = to })
        end
      end
    end
  end
  for _, segment in ipairs(segments) do
    conceal(buf, segment.row, segment.from, segment.to)
  end
end

-- Re-marks the buffer. Without `force`, it is a no-op when the covered range,
-- mode and revealed box set are unchanged (the cursor-motion fast path).
function M.refresh(buf, force)
  if buf == nil or buf == 0 then
    buf = vim.api.nvim_get_current_buf()
  end
  local s = attached[buf]
  if not s then
    return
  end
  if not vim.api.nvim_buf_is_valid(buf) then
    attached[buf] = nil
    return
  end

  local mode = buffer_mode(buf)
  local ok, parser = pcall(vim.treesitter.get_parser, buf, LANG)
  local first, last = marked_range(buf)
  if mode == 'off' or not ok or not parser or not first then
    vim.api.nvim_buf_clear_namespace(buf, NS, 0, -1)
    s.key = nil
    return
  end

  local root = parser:parse()[1]:root()
  local reveal_first, reveal_last = reveal_rows(buf)
  local boxes, revealed = {}, {}
  for _, node in get_box_query():iter_captures(root, buf, first, last) do
    local start_row, _, end_row = node:range()
    if reveal_first and start_row <= reveal_last and end_row >= reveal_first then
      table.insert(revealed, start_row)
    else
      table.insert(boxes, node)
    end
  end

  local key = table.concat({ mode, M.options.char, first, last, table.concat(revealed, ',') }, '|')
  if not force and key == s.key then
    return
  end
  s.key = key

  vim.api.nvim_buf_clear_namespace(buf, NS, 0, -1)
  local mark = mode == 'metadata' and mark_box_metadata or mark_box_whole
  for _, node in ipairs(boxes) do
    if not node:has_error() then
      mark(buf, node)
    end
  end
end

local function refresh_debounced(buf)
  local s = attached[buf]
  if not s then
    return
  end
  s.gen = (s.gen or 0) + 1
  local gen = s.gen
  vim.defer_fn(function()
    if attached[buf] and attached[buf].gen == gen then
      M.refresh(buf, true)
    end
  end, TEXT_CHANGED_DEBOUNCE_MS)
end

function M.attach(buf)
  if buf == nil or buf == 0 then
    buf = vim.api.nvim_get_current_buf()
  end
  -- extmark `conceal_lines` is nvim 0.11+; older nvims keep boxes verbatim
  -- (`:checkhealth cutting_garden` says so).
  if attached[buf] or vim.fn.has('nvim-0.11') == 0 then
    return
  end
  attached[buf] = {}

  local group = vim.api.nvim_create_augroup('cutting_garden_elide_' .. buf, { clear = true })
  vim.api.nvim_create_autocmd({ 'CursorMoved', 'CursorMovedI', 'ModeChanged', 'WinScrolled' }, {
    group = group,
    buffer = buf,
    callback = function()
      M.refresh(buf)
    end,
  })
  vim.api.nvim_create_autocmd({ 'BufWinEnter', 'WinEnter' }, {
    group = group,
    buffer = buf,
    callback = function()
      if buffer_mode(buf) ~= 'off' then
        set_window_options(vim.api.nvim_get_current_win())
      end
      M.refresh(buf)
    end,
  })
  vim.api.nvim_create_autocmd({ 'TextChanged', 'TextChangedI' }, {
    group = group,
    buffer = buf,
    callback = function()
      refresh_debounced(buf)
    end,
  })
  vim.api.nvim_create_autocmd({ 'BufWipeout', 'BufDelete' }, {
    group = group,
    buffer = buf,
    callback = function()
      attached[buf] = nil
      pcall(vim.api.nvim_del_augroup_by_id, group)
    end,
  })

  if buffer_mode(buf) ~= 'off' then
    for _, win in ipairs(vim.fn.win_findbuf(buf)) do
      set_window_options(win)
    end
  end
  M.refresh(buf, true)
end

-- `:CgElideToggle`: flips the buffer between `off` and the configured mode
-- (`box` when the configured mode is itself `off`).
function M.toggle(buf)
  if buf == nil or buf == 0 then
    buf = vim.api.nvim_get_current_buf()
  end
  local s = attached[buf]
  if not s then
    vim.notify('cutting_garden: not an organize buffer', vim.log.levels.WARN)
    return
  end
  if buffer_mode(buf) == 'off' then
    s.mode = M.options.mode ~= 'off' and M.options.mode or 'box'
    for _, win in ipairs(vim.fn.win_findbuf(buf)) do
      set_window_options(win)
    end
  else
    s.mode = 'off'
  end
  M.refresh(buf, true)
end

return M
