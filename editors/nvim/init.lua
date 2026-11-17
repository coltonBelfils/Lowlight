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
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'lowlight',
  callback = function()
    vim.opt_local.tabstop = 4
    vim.opt_local.shiftwidth = 4
    vim.opt_local.expandtab = false
    vim.opt_local.commentstring = '// %s'
  end,
})
