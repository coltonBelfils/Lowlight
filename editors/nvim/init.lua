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
  local root = vim.fs.root(path, { 'std', '.git', 'go.mod' })
  return root or vim.fn.getcwd()
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

    local client_id = vim.lsp.start({
      name = 'lowlight-lsp',
      cmd = { 'lowlight', 'lsp', '--stdio' },
      root_dir = root_dir,
      cmd_env = cmd_env,
      settings = {},
    })

    if client_id then
      local bufopts = { noremap = true, silent = true, buffer = bufnr }

      -- Core LSP keybindings
      vim.keymap.set('n', 'K', vim.lsp.buf.hover, bufopts)
      vim.keymap.set('n', 'gd', vim.lsp.buf.definition, bufopts)
      vim.keymap.set('n', 'gD', vim.lsp.buf.declaration, bufopts)
      vim.keymap.set('n', 'gi', vim.lsp.buf.implementation, bufopts)
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

      -- Testing Keybindings
      vim.keymap.set('n', '<leader>tr', function()
        vim.cmd('!lowlight test ' .. vim.fn.expand('%'))
      end, vim.tbl_extend('force', bufopts, { desc = 'Lowlight: Run tests in current file' }))

      vim.keymap.set('n', '<leader>tf', function()
        vim.cmd('!lowlight test ' .. vim.fn.expand('%'))
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

-- 4. Formatting & Indentation Preferences
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'lowlight',
  callback = function()
    vim.opt_local.tabstop = 4
    vim.opt_local.shiftwidth = 4
    vim.opt_local.expandtab = true
    vim.opt_local.commentstring = '// %s'
  end,
})
