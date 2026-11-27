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
        target = '',
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

    vim.lsp.commands['lowlight.runTest'] = function(command, _ctx)
      local args = command.arguments or {}
      local fname = args[1] or vim.api.nvim_buf_get_name(0)
      if type(fname) == 'string' and fname:sub(1, 7) == 'file://' then
        fname = vim.uri_to_fname(fname)
      end
      local test_name = args[2]
      local filter = (test_name and test_name ~= '') and ('^' .. test_name:gsub('([%^%$%(%)%%%.%[%]%*%+%-%?])', '\\%1') .. '$') or nil
      if _G.LowlightTest then
        _G.LowlightTest.run({ target = fname, filter = filter })
      else
        vim.cmd('!lowlight test ' .. vim.fn.fnameescape(fname))
      end
    end

    vim.lsp.commands['lowlight.runAllTests'] = function(command, _ctx)
      local args = command.arguments or {}
      local fname = args[1]
      if type(fname) == 'string' and fname ~= '' and fname:sub(1, 7) == 'file://' then
        fname = vim.uri_to_fname(fname)
      end
      if _G.LowlightTest then
        _G.LowlightTest.run({ target = (fname and fname ~= '') and fname or nil })
      elseif fname and fname ~= '' then
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
        if _G.LowlightTest then
          _G.LowlightTest.run_nearest()
        else
          vim.cmd('!lowlight test ' .. vim.fn.fnameescape(vim.fn.expand('%')))
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run nearest @test (or file)' }))

      vim.keymap.set('n', '<leader>tf', function()
        if _G.LowlightTest then
          _G.LowlightTest.run({ target = vim.api.nvim_buf_get_name(0) })
        else
          vim.cmd('!lowlight test ' .. vim.fn.fnameescape(vim.fn.expand('%')))
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run tests in current file' }))

      vim.keymap.set('n', '<leader>ta', function()
        if _G.LowlightTest then
          _G.LowlightTest.run({})
        else
          vim.cmd('!lowlight test')
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run all tests in project' }))

      vim.keymap.set('n', '<leader>tt', function()
        if _G.LowlightTest then
          _G.LowlightTest.pick_and_run()
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Pick and run test' }))

      vim.keymap.set('n', '<leader>to', function()
        if _G.LowlightTest then
          _G.LowlightTest.open_output()
        end
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Open test output (gF jumps to links)' }))

      -- Enable Native Inlay Hints (Neovim 0.10+)
      if vim.lsp.inlay_hint then
        vim.lsp.inlay_hint.enable(true, { bufnr = bufnr })
      end

      -- Buffer commands for comptime / @derive expansion & target switching
      vim.api.nvim_buf_create_user_command(bufnr, 'LowlightExpand', function()
        local uri = vim.uri_from_bufnr(bufnr)
        vim.lsp.buf_request(bufnr, 'lowlight/expandDocument', { textDocument = { uri = uri } }, function(err, result)
          if err or not result or not result.text then
            vim.notify('LowlightExpand failed: ' .. tostring((err and err.message) or 'no result'), vim.log.levels.ERROR)
            return
          end
          local lines = vim.split('// Expanded by Lowlight (target: ' .. tostring(result.target or 'host') .. ')\n' .. result.text, '\n', { plain = true })
          vim.cmd('vnew')
          local scratch = vim.api.nvim_get_current_buf()
          vim.bo[scratch].buftype = 'nofile'
          vim.bo[scratch].bufhidden = 'wipe'
          vim.bo[scratch].swapfile = false
          vim.bo[scratch].filetype = 'lowlight'
          vim.api.nvim_buf_set_lines(scratch, 0, -1, false, lines)
          vim.bo[scratch].modifiable = false
        end)
      end, { desc = 'Preview expanded comptime and @derive code' })

      vim.api.nvim_buf_create_user_command(bufnr, 'LowlightTarget', function(opts)
        local apply_target = function(triple)
          triple = vim.trim(triple or '')
          local client = vim.lsp.get_client_by_id(client_id)
          if client then
            client.config.settings = client.config.settings or { lowlight = {} }
            client.config.settings.lowlight = client.config.settings.lowlight or {}
            client.config.settings.lowlight.target = triple
            client:notify('workspace/didChangeConfiguration', { settings = client.config.settings })
          end
          vim.notify('Lowlight target: ' .. (triple ~= '' and triple or 'host default'), vim.log.levels.INFO)
        end
        local arg = vim.trim(opts.args or '')
        if opts.bang then
          apply_target('')
          return
        end
        if arg ~= '' then
          apply_target(arg)
          return
        end
        local targets = {
          '',
          'x86_64-unknown-linux-gnu',
          'aarch64-unknown-linux-gnu',
          'aarch64-apple-darwin',
          'x86_64-apple-darwin',
          'x86_64-pc-windows-msvc',
          'wasm32-unknown-unknown',
          'riscv64-unknown-linux-gnu',
        }
        vim.ui.select(targets, {
          prompt = 'Select Lowlight target triple:',
          format_item = function(t) return t == '' and '(host default)' or t end,
        }, function(choice)
          if choice ~= nil then
            apply_target(choice)
          end
        end)
      end, { nargs = '?', bang = true, desc = 'Switch Lowlight compilation target triple (:LowlightTarget! resets to host)' })

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

-- 5. Native Async Lowlight Test Runner (`lowlight test --json`)
do
  local test_ns = vim.api.nvim_create_namespace('lowlight_tests')
  local output_buf = nil
  local active_job = nil
  local last_output_lines = {}
  local last_root = vim.fn.getcwd()

  local M = {}

  local function regex_escape(s)
    return (tostring(s or ''):gsub('([%^%$%(%)%%%.%[%]%*%+%-%?])', '\\%1'))
  end

  local function resolve_path(raw_path, root_dir)
    if not raw_path or raw_path == '' then
      return nil
    end
    if raw_path:sub(1, 1) == '/' or raw_path:match('^[A-Za-z]:[/\\]') then
      return raw_path
    end
    local cand = (root_dir or vim.fn.getcwd()) .. '/' .. raw_path
    if vim.fn.filereadable(cand) == 1 then
      return cand
    end
    return raw_path
  end

  function M.parse_event(line)
    if type(line) ~= 'string' then
      return nil
    end
    local trimmed = line:gsub('\r$', ''):match('^%s*(.-)%s*$')
    if trimmed == '' or trimmed:sub(1, 1) ~= '{' then
      return nil
    end
    local ok, decoded = pcall(vim.json.decode, trimmed)
    if ok and type(decoded) == 'table' then
      return decoded
    end
    return nil
  end

  function M.extract_trace_locations(msg, root_dir)
    local out = {}
    if type(msg) ~= 'string' or msg == '' then
      return out
    end
    for line in (msg .. '\n'):gmatch('(.-)\n') do
      local path, lnum, col, rest = line:match('([^%s:()"\'<>]+%.ll):(%d+):(%d+):?%s*(.*)')
      if not path then
        path, lnum, rest = line:match('([^%s:()"\'<>]+%.ll):(%d+)%s*(.*)')
        col = '1'
      end
      if path and lnum then
        table.insert(out, {
          filename = resolve_path(path, root_dir),
          lnum = math.max(1, tonumber(lnum) or 1),
          col = math.max(1, tonumber(col) or 1),
          text = (rest and rest ~= '' and rest) or line:match('^%s*(.-)%s*$'),
        })
      end
    end
    return out
  end

  function M.find_nearest_test(lines, cursor_lnum)
    if type(lines) ~= 'table' or #lines == 0 then
      return nil
    end
    local start = math.min(#lines, math.max(1, cursor_lnum or 1))
    for i = start, 1, -1 do
      local raw = lines[i] or ''
      local name = raw:match('^%s*bind%s+([A-Za-z_][A-Za-z0-9_]*)%s*=')
      if name then
        if name == 'test_main' then
          return name
        end
        for j = i - 1, math.max(1, i - 6), -1 do
          local prev = (lines[j] or ''):match('^%s*(.-)%s*$')
          if prev ~= '' and not prev:match('^//') then
            if prev == '@test' or prev:match('^@test[^A-Za-z0-9_]') then
              return name
            elseif not prev:match('^@') then
              break
            end
          end
        end
      end
      local inline_name = raw:match('^%s*@test[^A-Za-z0-9_].-%sbind%s+([A-Za-z_][A-Za-z0-9_]*)%s*=')
      if inline_name then
        return inline_name
      end
    end
    return nil
  end

  local function find_loaded_buf(abs_path)
    if not abs_path or abs_path == '' then
      return nil
    end
    local target = vim.fs.normalize(abs_path)
    for _, b in ipairs(vim.api.nvim_list_bufs()) do
      if vim.api.nvim_buf_is_loaded(b) then
        local bname = vim.api.nvim_buf_get_name(b)
        if bname ~= '' and vim.fs.normalize(bname) == target then
          return b
        end
      end
    end
    return nil
  end

  local function set_extmark(file, line, text, hl)
    local b = find_loaded_buf(file)
    if not b or not line or line < 1 then
      return
    end
    local ln = math.min(vim.api.nvim_buf_line_count(b) - 1, math.max(0, line - 1))
    pcall(vim.api.nvim_buf_set_extmark, b, test_ns, ln, 0, {
      id = line,
      virt_text = { { ' ' .. text, hl } },
      virt_text_pos = 'eol',
    })
  end

  local function refresh_output_buffer()
    if not output_buf or not vim.api.nvim_buf_is_valid(output_buf) then
      return
    end
    vim.bo[output_buf].modifiable = true
    vim.api.nvim_buf_set_lines(output_buf, 0, -1, false, last_output_lines)
    vim.bo[output_buf].modifiable = false
  end

  function M.open_output()
    if not output_buf or not vim.api.nvim_buf_is_valid(output_buf) then
      output_buf = vim.api.nvim_create_buf(false, true)
      vim.bo[output_buf].buftype = 'nofile'
      vim.bo[output_buf].bufhidden = 'hide'
      vim.bo[output_buf].swapfile = false
      vim.bo[output_buf].filetype = 'lowlight-test-output'
    end
    local root = last_root or vim.fn.getcwd()
    vim.bo[output_buf].path = root .. ',' .. root .. '/std'
    refresh_output_buffer()
    vim.cmd('botright split')
    vim.api.nvim_win_set_buf(0, output_buf)
    vim.cmd('resize 14')
    vim.keymap.set('n', 'q', '<cmd>close<CR>', { buffer = output_buf, silent = true })
    vim.keymap.set('n', '<CR>', 'gF', { buffer = output_buf, remap = true, silent = true })
  end

  function M.run(opts)
    opts = opts or {}
    if active_job then
      pcall(vim.fn.jobstop, active_job)
      active_job = nil
    end

    local cur_file = vim.api.nvim_buf_get_name(0)
    local root_dir = find_lowlight_root(opts.target or cur_file)
    last_root = root_dir

    local cmd = { 'lowlight', 'test', '--json' }
    if opts.release then
      table.insert(cmd, '--release')
    end
    if opts.filter and opts.filter ~= '' then
      table.insert(cmd, '-run')
      table.insert(cmd, opts.filter)
    end
    if opts.target and opts.target ~= '' then
      table.insert(cmd, opts.target)
    end

    local env = {}
    if vim.fn.isdirectory(root_dir .. '/std') == 1 then
      env.LOWLIGHT_ROOT = root_dir
    end

    for _, b in ipairs(vim.api.nvim_list_bufs()) do
      if vim.api.nvim_buf_is_loaded(b) and vim.bo[b].filetype == 'lowlight' then
        vim.api.nvim_buf_clear_namespace(b, test_ns, 0, -1)
        vim.diagnostic.reset(test_ns, b)
      end
    end

    last_output_lines = {}
    local qf_items = {}
    local diags_by_file = {}
    local stdout_buf = ''
    local summary_ev = nil

    local function append_out(text)
      for l in (tostring(text) .. '\n'):gmatch('(.-)\n') do
        table.insert(last_output_lines, l)
      end
      if #last_output_lines > 0 and last_output_lines[#last_output_lines] == '' then
        table.remove(last_output_lines)
      end
    end

    local function add_diag(file, line, col, msg)
      if not file or file == '' then return end
      local norm = vim.fs.normalize(file)
      diags_by_file[norm] = diags_by_file[norm] or {}
      table.insert(diags_by_file[norm], {
        lnum = math.max(0, (line or 1) - 1),
        col = math.max(0, (col or 1) - 1),
        severity = vim.diagnostic.severity.ERROR,
        source = 'lowlight-test',
        message = msg,
      })
    end

    local function handle_event(ev)
      if ev.event == 'run' then
        append_out('=== RUN   ' .. (ev.desc or ev.name or ''))
        set_extmark(ev.file, ev.line, '⟳ running...', 'Comment')
      elseif ev.event == 'pass' then
        if ev.output and ev.output ~= '' then append_out(ev.output) end
        local dur = string.format('%dµs', ev.durationUs or 0)
        append_out('--- PASS: ' .. (ev.desc or ev.name or '') .. ' (' .. dur .. ', 0 leaks)')
        set_extmark(ev.file, ev.line, '✓ ' .. dur, 'DiagnosticOk')
      elseif ev.event == 'fail' then
        if ev.output and ev.output ~= '' then append_out(ev.output) end
        append_out('--- FAIL: ' .. (ev.desc or ev.name or ''))
        for l in ((ev.message or '') .. '\n'):gmatch('(.-)\n') do
          if l ~= '' then append_out('    ' .. l) else append_out('') end
        end
        set_extmark(ev.file, ev.line, '✗ failed', 'DiagnosticError')

        if type(ev.cases) == 'table' and #ev.cases > 0 then
          for _, c in ipairs(ev.cases) do
            local cf = resolve_path(c.failFile ~= '' and c.failFile or ev.failFile or ev.file, root_dir)
            local cl = (c.failLine and c.failLine > 0) and c.failLine or (ev.failLine or ev.line or 1)
            local cc = (c.failCol and c.failCol > 0) and c.failCol or (ev.failCol or 1)
            local title = string.format('case [%d]%s', c.index or 0, (c.label and c.label ~= '') and (' (' .. c.label .. ')') or '')
            local summary = title .. ': ' .. ((c.message or ''):match('^([^\n]+)') or 'failed')
            add_diag(cf, cl, cc, title .. '\n' .. (c.message or ''))
            table.insert(qf_items, {
              filename = cf,
              lnum = cl,
              col = cc,
              type = 'E',
              text = summary,
            })
          end
        else
          local ff = resolve_path((ev.failFile and ev.failFile ~= '') and ev.failFile or ev.file, root_dir)
          local fl = (ev.failLine and ev.failLine > 0) and ev.failLine or (ev.line or 1)
          local fc = (ev.failCol and ev.failCol > 0) and ev.failCol or (ev.col or 1)
          add_diag(ff, fl, fc, ev.message or 'assertion failed')
          local locs = M.extract_trace_locations(ev.message, root_dir)
          if #locs > 0 then
            for _, loc in ipairs(locs) do
              table.insert(qf_items, {
                filename = loc.filename,
                lnum = loc.lnum,
                col = loc.col,
                type = 'E',
                text = loc.text,
              })
            end
          else
            table.insert(qf_items, {
              filename = ff,
              lnum = fl,
              col = fc,
              type = 'E',
              text = (ev.message or 'test failed'):match('^([^\n]+)') or 'test failed',
            })
          end
        end
      elseif ev.event == 'build_error' then
        append_out('BUILD ERROR: ' .. (ev.message or ''))
        for _, loc in ipairs(M.extract_trace_locations(ev.message, root_dir)) do
          table.insert(qf_items, {
            filename = loc.filename,
            lnum = loc.lnum,
            col = loc.col,
            type = 'E',
            text = loc.text,
          })
        end
      elseif ev.event == 'summary' then
        summary_ev = ev
        append_out(string.format(
          '\nTest results: %d passed, %d failed across %d package(s) (total %d)',
          ev.passed or 0, ev.failed or 0, ev.packages or 1, ev.total or 0
        ))
      end
    end

    local function consume_chunk(data)
      if not data then return end
      stdout_buf = stdout_buf .. table.concat(data, '\n')
      while true do
        local pos = stdout_buf:find('\n', 1, true)
        if not pos then break end
        local line = stdout_buf:sub(1, pos - 1):gsub('\r$', '')
        stdout_buf = stdout_buf:sub(pos + 1)
        local ev = M.parse_event(line)
        if ev then
          handle_event(ev)
        elseif line ~= '' then
          append_out(line)
        end
      end
      refresh_output_buffer()
    end

    active_job = vim.fn.jobstart(cmd, {
      cwd = root_dir,
      env = next(env) and env or nil,
      stdout_buffered = false,
      on_stdout = function(_, data)
        vim.schedule(function()
          consume_chunk(data)
        end)
      end,
      on_stderr = function(_, data)
        if not data then return end
        vim.schedule(function()
          for _, l in ipairs(data) do
            if l ~= '' then append_out(l) end
          end
          refresh_output_buffer()
        end)
      end,
      on_exit = function(_, code)
        vim.schedule(function()
          active_job = nil
          if stdout_buf ~= '' then
            local ev = M.parse_event(stdout_buf)
            if ev then handle_event(ev) end
            stdout_buf = ''
          end
          for _, b in ipairs(vim.api.nvim_list_bufs()) do
            if vim.api.nvim_buf_is_loaded(b) then
              local norm = vim.fs.normalize(vim.api.nvim_buf_get_name(b))
              if diags_by_file[norm] then
                vim.diagnostic.set(test_ns, b, diags_by_file[norm], {})
              end
            end
          end
          vim.fn.setqflist({}, ' ', {
            title = 'Lowlight Tests',
            items = qf_items,
          })
          refresh_output_buffer()
          if #qf_items > 0 then
            vim.cmd('botright copen 8')
          elseif code == 0 then
            pcall(vim.cmd, 'cclose')
          end
          local passed = (summary_ev and summary_ev.passed) or 0
          local failed = (summary_ev and summary_ev.failed) or (code ~= 0 and 1 or 0)
          if failed > 0 or code ~= 0 then
            vim.notify(string.format('Lowlight Tests: %d passed, %d failed', passed, failed), vim.log.levels.ERROR)
          else
            vim.notify(string.format('Lowlight Tests: %d passed, 0 failed', passed), vim.log.levels.INFO)
          end
        end)
      end,
    })
  end

  function M.run_nearest()
    local fname = vim.api.nvim_buf_get_name(0)
    local cursor = vim.api.nvim_win_get_cursor(0)
    local lines = vim.api.nvim_buf_get_lines(0, 0, -1, false)
    local nearest = M.find_nearest_test(lines, cursor[1])
    if nearest and nearest ~= 'test_main' then
      M.run({ target = fname, filter = '^' .. regex_escape(nearest) .. '$' })
    else
      M.run({ target = fname })
    end
  end

  function M.pick_and_run()
    local cur_file = vim.api.nvim_buf_get_name(0)
    local root_dir = find_lowlight_root(cur_file)
    local cmd = { 'lowlight', 'test', '--list', '--json', root_dir }
    local env = vim.fn.isdirectory(root_dir .. '/std') == 1 and { LOWLIGHT_ROOT = root_dir } or nil
    local lines
    if vim.system then
      local res = vim.system(cmd, { cwd = root_dir, env = env, text = true }):wait()
      lines = vim.split((res and res.stdout) or '', '\n', { trimempty = true })
    else
      lines = vim.fn.systemlist(cmd)
    end
    local items = {}
    for _, l in ipairs(lines or {}) do
      local ev = M.parse_event(l)
      if ev and ev.event == 'test' then
        table.insert(items, ev)
      end
    end
    if #items == 0 then
      vim.notify('No Lowlight tests discovered in workspace', vim.log.levels.INFO)
      return
    end
    vim.ui.select(items, {
      prompt = 'Run Lowlight Test:',
      format_item = function(item)
        return string.format('%s  (%s:%d)', item.desc or item.name, item.relFile or item.file, item.line or 1)
      end,
    }, function(choice)
      if not choice then return end
      local filter = (not choice.isTestMain and choice.name) and ('^' .. regex_escape(choice.name) .. '$') or nil
      M.run({ target = choice.file, filter = filter })
    end)
  end

  _G.LowlightTest = M

  vim.api.nvim_create_user_command('LowlightTestRun', function()
    M.run_nearest()
  end, { desc = 'Run nearest Lowlight @test' })
  vim.api.nvim_create_user_command('LowlightTestFile', function()
    M.run({ target = vim.api.nvim_buf_get_name(0) })
  end, { desc = 'Run Lowlight tests in current file' })
  vim.api.nvim_create_user_command('LowlightTestAll', function()
    M.run({})
  end, { desc = 'Run all Lowlight tests in project' })
  vim.api.nvim_create_user_command('LowlightTestPick', function()
    M.pick_and_run()
  end, { desc = 'Pick and run a Lowlight test' })
  vim.api.nvim_create_user_command('LowlightTestOutput', function()
    M.open_output()
  end, { desc = 'Open Lowlight test output buffer' })
end
