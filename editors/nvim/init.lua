-- Lowlight (Lightweight) Neovim Configuration
-- Requires Neovim 0.10+ (for native inlay hints and lsp.start)

-- 1. Filetype Detection
vim.filetype.add({
  extension = {
    ll = 'lowlight',
  },
})

-- 2. Root Directory Discovery
local function find_lowlight_root(fname)
  local path = fname or vim.api.nvim_buf_get_name(0)
  if path == '' then
    return vim.fn.getcwd()
  end
  local root = vim.fs.root(path, { 'lowlight.toml', '.lowlightfmt', 'std', '.git', 'go.mod' })
  return root or vim.fs.dirname(path)
end

-- 3. LSP Auto-Attachment for Lowlight
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'lowlight',
  callback = function(args)
    local bufnr = args.buf
    local root_dir = find_lowlight_root(vim.api.nvim_buf_get_name(bufnr))

    local cmd_env = {}
    if vim.fn.isdirectory(root_dir .. '/std') == 1 then
      cmd_env.LOWLIGHT_ROOT = root_dir
    end

    local default_settings = {
      lowlight = {
        format = {
          indent = 'tabs',
          tabWidth = 4,
          lineWidth = 100,
        },
        inlayHints = {
          types = true,
          parameters = true,
        },
      },
    }

    local client_id = vim.lsp.start({
      name = 'lowlight-lsp',
      cmd = { 'lowlight', 'lsp', '--stdio' },
      root_dir = root_dir,
      cmd_env = cmd_env,
      init_options = default_settings.lowlight,
      settings = default_settings,
    })

    -- Register Lowlight LSP client-side command handlers (interactive UI & navigation)
    vim.lsp.commands['lowlight.showLocations'] = function(command, _ctx)
      local cmd_args = command.arguments or {}
      local locs = cmd_args[3] or cmd_args[1] or {}
      if type(locs) ~= 'table' or #locs == 0 then
        vim.notify('No locations found', vim.log.levels.INFO)
        return
      end
      if #locs == 1 and locs[1].uri then
        local target = locs[1]
        local fname = vim.uri_to_fname(target.uri)
        local line = (target.range and target.range.start and target.range.start.line or 0) + 1
        local col = (target.range and target.range.start and target.range.start.character or 0)
        vim.cmd('edit ' .. vim.fn.fnameescape(fname))
        vim.api.nvim_win_set_cursor(0, { line, col })
        return
      end
      vim.ui.select(locs, {
        prompt = 'Select location',
        format_item = function(loc)
          local fname = vim.fn.fnamemodify(vim.uri_to_fname(loc.uri or ''), ':.')
          local line = (loc.range and loc.range.start and loc.range.start.line or 0) + 1
          return string.format('%s:%d', fname, line)
        end,
      }, function(choice)
        if choice and choice.uri then
          local fname = vim.uri_to_fname(choice.uri)
          local line = (choice.range and choice.range.start and choice.range.start.line or 0) + 1
          local col = (choice.range and choice.range.start and choice.range.start.character or 0)
          vim.cmd('edit ' .. vim.fn.fnameescape(fname))
          vim.api.nvim_win_set_cursor(0, { line, col })
        end
      end)
    end
    vim.lsp.commands['lowlight.showImplementationsQuickPick'] = function(command, ctx)
      local args = command.arguments or {}
      local title = args[1] or 'Select target'
      local items = args[2] or {}
      if #items == 0 then
        vim.notify('No implementations found', vim.log.levels.INFO)
        return
      end
      if #items == 1 and items[1].uri and items[1].uri ~= '' then
        local uri = items[1].uri
        local fname = vim.uri_to_fname(uri)
        local line = (items[1].line or 0) + 1
        local col = (items[1].character or 0) + 1
        vim.cmd('edit ' .. vim.fn.fnameescape(fname))
        vim.api.nvim_win_set_cursor(0, { line, col - 1 })
        return
      end

      vim.ui.select(items, {
        prompt = title,
        format_item = function(item)
          local desc = (item.description and item.description ~= '') and ('  (' .. item.description .. ')') or ''
          return item.label .. desc
        end,
      }, function(choice)
        if choice and choice.uri and choice.uri ~= '' then
          local fname = vim.uri_to_fname(choice.uri)
          local line = (choice.line or 0) + 1
          local col = (choice.character or 0) + 1
          vim.cmd('edit ' .. vim.fn.fnameescape(fname))
          vim.api.nvim_win_set_cursor(0, { line, col - 1 })
        elseif choice then
          vim.notify(choice.label .. ' is a built-in Lowlight primitive', vim.log.levels.INFO)
        end
      end)
    end

    vim.lsp.commands['lowlight.goToLocation'] = function(command, ctx)
      local args = command.arguments or {}
      local loc = args[1]
      if not loc then return end
      local uri = loc.uri or loc
      if type(uri) ~= 'string' or uri == '' then return end
      local fname = vim.uri_to_fname(uri)
      local line = 1
      local col = 1
      if loc.range and loc.range.start then
        line = (loc.range.start.line or 0) + 1
        col = (loc.range.start.character or 0) + 1
      end
      vim.cmd('edit ' .. vim.fn.fnameescape(fname))
      vim.api.nvim_win_set_cursor(0, { line, col - 1 })
    end

    vim.lsp.commands['lowlight.runFile'] = function(command, ctx)
      local args = command.arguments or {}
      local fname = args[1] or vim.api.nvim_buf_get_name(0)
      if fname:sub(1, 7) == 'file://' then
        fname = vim.uri_to_fname(fname)
      end
      vim.cmd('!lowlight run ' .. vim.fn.fnameescape(fname))
    end

    vim.lsp.commands['lowlight.runMain'] = function(command, ctx)
      vim.lsp.commands['lowlight.runFile'](command, ctx)
    end

    vim.lsp.commands['lowlight.runTest'] = function(command, ctx)
      local args = command.arguments or {}
      local fname = args[1] or vim.api.nvim_buf_get_name(0)
      if fname:sub(1, 7) == 'file://' then
        fname = vim.uri_to_fname(fname)
      end
      local test_name = args[2]
      if test_name and test_name ~= '' then
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(fname) .. ' -run ' .. vim.fn.fnameescape(test_name))
      else
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(fname))
      end
    end

    vim.lsp.commands['lowlight.runAllTests'] = function(command, ctx)
      local args = command.arguments or {}
      local fname = args[1]
      if fname and fname ~= '' then
        if fname:sub(1, 7) == 'file://' then
          fname = vim.uri_to_fname(fname)
        end
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(fname))
      else
        vim.cmd('!lowlight test')
      end
    end

    if client_id then
      local bufopts = { noremap = true, silent = true, buffer = bufnr }

      -- Core LSP keybindings
      vim.keymap.set('n', 'K', vim.lsp.buf.hover, bufopts)
      vim.keymap.set('n', 'gd', vim.lsp.buf.definition, bufopts)
      vim.keymap.set('n', 'gD', vim.lsp.buf.declaration, bufopts)
      vim.keymap.set('n', 'gy', vim.lsp.buf.type_definition, bufopts)
      vim.keymap.set('n', 'gi', vim.lsp.buf.implementation, bufopts)
      vim.keymap.set('n', 'gr', vim.lsp.buf.references, bufopts)
      vim.keymap.set('n', 'gS', vim.lsp.buf.workspace_symbol, bufopts)
      vim.keymap.set('n', '<leader>rn', vim.lsp.buf.rename, bufopts)
      vim.keymap.set({ 'n', 'v' }, '<leader>ca', vim.lsp.buf.code_action, bufopts)
      vim.keymap.set('n', '<C-k>', vim.lsp.buf.signature_help, bufopts)
      vim.keymap.set('n', '<leader>f', function()
        vim.lsp.buf.format({ async = true })
      end, bufopts)

      -- Diagnostic navigation
      vim.keymap.set('n', '[d', vim.diagnostic.goto_prev, bufopts)
      vim.keymap.set('n', ']d', vim.diagnostic.goto_next, bufopts)
      vim.keymap.set('n', '<leader>d', vim.diagnostic.open_float, bufopts)

      -- Testing & Running Keybindings
      vim.keymap.set('n', '<leader>rr', function()
        vim.cmd('!lowlight run ' .. vim.fn.fnameescape(vim.fn.expand('%')))
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run current file' }))

      vim.keymap.set('n', '<leader>cl', function()
        if vim.lsp.codelens then
          vim.lsp.codelens.run()
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run CodeLens on current line' }))

      vim.keymap.set('n', '<leader>tr', function()
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(vim.fn.expand('%')))
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run tests in current file' }))

      vim.keymap.set('n', '<leader>tf', function()
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(vim.fn.expand('%')))
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run tests in current file' }))

      vim.keymap.set('n', '<leader>ta', function()
        vim.cmd('!lowlight test')
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run all tests in project' }))

      -- Enable Native Inlay Hints (Neovim 0.10+)
      if vim.lsp.inlay_hint then
        vim.lsp.inlay_hint.enable(true, { bufnr = bufnr })
      end

      -- Enable Code Lens (displays '▶ Run Test' above @test functions)
      if vim.lsp.codelens then
        vim.lsp.codelens.refresh({ bufnr = bufnr })
        vim.api.nvim_create_autocmd({ 'BufEnter', 'CursorHold', 'InsertLeave' }, {
          buffer = bufnr,
          callback = function()
            vim.lsp.codelens.refresh({ bufnr = bufnr })
          end,
        })
      end
    end
  end,
})

-- 4. Formatting & Indentation Preferences (canonical `\t` indentation)

-- Indentation that mirrors `lowlight fmt`, used as 'indentexpr'. The rules, in order:
--   * a top-level declaration (or the doc comments / attributes above one) outside any
--     bracket starts at column 0, and lines inside a multi-line string keep their indent;
--   * `else` aligns with the `if` it belongs to;
--   * after a header ending in `:` (`if(...):`, `bind f = () int:`, `else:`, `when X:`,
--     `match(x):`, a result chain `load():`) or an opening `{` / `(` / `[`, indent one level;
--   * after a completed statement, continue at the level where it started, unwinding every
--     header whose single-expression body it completed; `when` arms stay put, and the
--     terminal `default` arm returns to its `match` / chain header;
--   * a `.method()` continuation line is one level deeper than its statement, and a line
--     starting with `}` / `)` / `]` closes one level.

-- Line lexer state, cached per buffer change: for each line, the bracket depth and whether it
-- starts inside a string, plus its code (string contents and `// comments` removed, `${...}`
-- interpolations kept, whitespace trimmed). Interpolated and backtick strings may span lines.
local lowlight_cache = {}

local function lowlight_lex(lnum)
  local buf = vim.api.nvim_get_current_buf()
  local tick = vim.api.nvim_buf_get_changedtick(buf)
  local c = lowlight_cache[buf]
  if not c or c.tick ~= tick then
    c = { tick = tick, lines = {}, last = 0, depth = 0, stack = {} }
    lowlight_cache[buf] = c
  end
  while c.last < lnum do
    local i = c.last + 1
    local line = vim.fn.getline(i)
    local stack = c.stack
    local top0 = stack[#stack]
    local entry = { depth = c.depth, in_string = top0 == '"' or top0 == '`' }
    local out, j, n = {}, 1, #line
    while j <= n do
      local ch = line:sub(j, j)
      local top = stack[#stack]
      if top == '"' or top == '`' then
        if ch == '\\' then
          j = j + 1
        elseif ch == top then
          table.remove(stack)
          out[#out + 1] = ch
        elseif ch == '$' and line:sub(j + 1, j + 1) == '{' then
          stack[#stack + 1] = 'I'
          j = j + 1
        end
      elseif ch == '/' and line:sub(j + 1, j + 1) == '/' then
        break
      elseif ch == '"' or ch == '`' then
        stack[#stack + 1] = ch
        out[#out + 1] = ch .. ch
      elseif ch == "'" then
        local close = line:find("'", j + (line:sub(j + 1, j + 1) == '\\' and 3 or 2), true)
        out[#out + 1] = "''"
        j = close or n
      elseif ch == '{' then
        stack[#stack + 1] = 'B'
        c.depth = c.depth + 1
        out[#out + 1] = ch
      elseif ch == '}' and top == 'I' then
        table.remove(stack) -- end of `${...}`: back inside the string
      elseif ch == '}' or ch == ')' or ch == ']' then
        if ch == '}' and top == 'B' then
          table.remove(stack)
        end
        c.depth = math.max(c.depth - 1, 0)
        out[#out + 1] = ch
      elseif ch == '(' or ch == '[' then
        c.depth = c.depth + 1
        out[#out + 1] = ch
      else
        out[#out + 1] = ch
      end
      j = j + 1
    end
    entry.code = (table.concat(out):gsub('%s+$', ''))
    c.lines[i] = entry
    c.last = i
  end
  return c.lines[lnum]
end

local function code(lnum)
  return lowlight_lex(lnum).code
end

local function starts_with_word(line, word)
  return line:match('^%s*' .. word .. '%f[^%w_]') ~= nil
end

local function nonblank(lnum)
  return vim.fn.getline(lnum):match('%S') ~= nil
end

local function is_comment(lnum)
  return vim.fn.getline(lnum):match('^%s*//') ~= nil and not lowlight_lex(lnum).in_string
end

-- Previous non-blank, non-comment line before `lnum` (0 if none).
local function prev_code_line(lnum)
  local p = vim.fn.prevnonblank(lnum - 1)
  while p > 0 and is_comment(p) do
    p = vim.fn.prevnonblank(p - 1)
  end
  return p
end

-- The `match` / result chain header line that owns the arm on line `arm`.
local function header_of(arm)
  local arm_ind = vim.fn.indent(arm)
  for i = arm - 1, 1, -1 do
    if nonblank(i) and vim.fn.indent(i) < arm_ind and not lowlight_lex(i).in_string then
      return i
    end
  end
  return arm
end

-- Is this code an `if` that is not already completed by an `else` on the same line?
local function opens_if(c)
  local is_if = c:match('^%s*}?%s*if%s*%(') or c:match('[=:,(]%s*if%s*%(') or c:match('^%s*}?%s*else%s+if')
  return is_if and not c:match('%)%s*:.*else%s*:')
end

-- The line of the `if` that an `else` written after line `from` belongs to. Scanning upward,
-- an `if` already closed by a later `else` at its level is skipped. When several enclosing `if`s
-- could take the `else`, the one at indent `hint` wins.
local function matching_if(from, hint)
  local limit = vim.fn.indent(from)
  local closed, first = {}, nil
  for i = from, 1, -1 do
    if nonblank(i) and not is_comment(i) and not lowlight_lex(i).in_string then
      local ind = vim.fn.indent(i)
      if ind <= limit then
        local c = code(i)
        local is_else = c:match('^%s*}?%s*else%f[^%w_]') ~= nil
        local is_else_if = c:match('^%s*}?%s*else%s+if') ~= nil
        if opens_if(c) and not closed[ind] then
          if hint == nil or ind == hint then
            return i
          end
          first = first or i
        elseif opens_if(c) and closed[ind] and not is_else_if then
          closed[ind] = nil
        elseif is_else and not is_else_if then
          closed[ind] = true
        elseif not is_else and not opens_if(c) and ind < limit and not c:match('^%s*[})%]]') then
          return first
        end
        limit = math.min(limit, ind)
      end
    end
  end
  return first
end

-- The first line of the statement that ends on line `lnum`: lines inside a multi-line string
-- map to the line that opened it, a closing `}` / `)` / `]` line to the line that opened the
-- block, an `else` line to its `if`, and a `.method()` continuation to the line it continues.
local function statement_start(lnum)
  local cur = lnum
  for _ = 1, 200 do
    local c = code(cur)
    local ind = vim.fn.indent(cur)
    local nxt
    if lowlight_lex(cur).in_string then
      nxt = cur - 1
    elseif c:match('^%s*[})%]]') then
      for i = cur - 1, 1, -1 do
        if nonblank(i) and vim.fn.indent(i) <= ind and not lowlight_lex(i).in_string and not is_comment(i) then
          nxt = i
          break
        end
      end
    elseif starts_with_word(c, 'else') then
      nxt = matching_if(cur - 1, ind)
    elseif c:match('^%s*%.') then
      nxt = prev_code_line(cur)
    end
    if not nxt or nxt <= 0 then
      return cur
    end
    cur = nxt
  end
  return cur
end

function _G.LowlightIndent(lnum)
  local prev = prev_code_line(lnum)
  if lowlight_lex(lnum).in_string then
    return -1 -- inside a multi-line string: keep the text as written
  end
  local cur = vim.fn.getline(lnum)
  local depth = lowlight_lex(lnum).depth
  local decl = lnum
  while decl <= vim.fn.line('$') and vim.fn.getline(decl):match('^%s*//') do
    decl = decl + 1
  end
  local dline = vim.fn.getline(decl)
  if depth == 0 and (starts_with_word(dline, 'bind') or starts_with_word(dline, 'define')
      or starts_with_word(dline, 'import') or starts_with_word(dline, 'extern') or dline:match('^%s*@')) then
    return 0
  end
  local above = vim.fn.prevnonblank(lnum - 1)
  if cur:match('^%s*//') then
    if above > 0 and is_comment(above) then
      return vim.fn.indent(above) -- continue a comment block at its indentation
    end
    if depth == 0 and above < lnum - 1 then
      return 0 -- a section comment after a blank line at top level
    end
  end
  if prev == 0 then
    return 0
  end
  local sw = vim.fn.shiftwidth()
  local closes = cur:match('^%s*[})%]]') and sw or 0
  if starts_with_word(cur, 'else') then
    local i = matching_if(prev, vim.fn.indent(lnum))
    if i then
      return vim.fn.indent(i)
    end
  end
  local pcode = code(prev)
  if pcode:match(':$') or pcode:match('[{(%[]$') then
    return vim.fn.indent(prev) + sw - closes
  end
  if cur:match('^%s*%.') then
    if pcode:match('^%s*%.') then
      return vim.fn.indent(prev)
    end
    return vim.fn.indent(statement_start(prev)) + sw
  end
  if pcode:match(',$') then
    return vim.fn.indent(prev) - closes -- the next item of a multi-line list
  end
  -- Unwind: the statement starting at `start` is complete.
  local start = statement_start(prev)
  local ind = vim.fn.indent(start)
  for _ = 1, 50 do
    local scode = code(start)
    if starts_with_word(scode, 'when') then
      break -- after a one-line arm, the next arm
    end
    if starts_with_word(scode, 'default') then
      start = statement_start(header_of(start)) -- the terminal arm ends the match / chain
      ind = vim.fn.indent(start)
    else
      local pp = prev_code_line(start)
      if pp == 0 then
        break
      end
      local ppcode = code(pp)
      if not (ppcode:match(':$') and vim.fn.indent(pp) < vim.fn.indent(start)) then
        break
      end
      if starts_with_word(ppcode, 'when') then
        ind = vim.fn.indent(pp) -- the body of a `when X:` arm: back to the arms
        break
      elseif starts_with_word(ppcode, 'default') then
        start = statement_start(header_of(pp))
        ind = vim.fn.indent(start)
      else
        start = statement_start(pp) -- the single-expression body of `pp` is complete
        ind = vim.fn.indent(start)
      end
    end
  end
  return math.max(ind - closes, 0)
end

vim.api.nvim_create_autocmd('FileType', {
  pattern = 'lowlight',
  callback = function()
    vim.opt_local.tabstop = 4
    vim.opt_local.shiftwidth = 4
    vim.opt_local.expandtab = false
    vim.opt_local.commentstring = '// %s'
    vim.opt_local.indentexpr = 'v:lua.LowlightIndent(v:lnum)'
    vim.opt_local.indentkeys = '0{,0},0),0],!^F,o,O,e,0=else,0=when,0=default'
    -- Continue `//` comments on Enter / `o` inside a comment.
    vim.opt_local.comments = '://'
    vim.opt_local.formatoptions:append('rqj')
  end,
})
