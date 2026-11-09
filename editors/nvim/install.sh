#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Installing Lowlight Neovim Support ==="

NVIM_CONFIG="${HOME}/.config/nvim"
mkdir -p "${NVIM_CONFIG}/ftdetect"
mkdir -p "${NVIM_CONFIG}/syntax"
mkdir -p "${NVIM_CONFIG}/after/ftplugin"

# 1. Install ftdetect & syntax
cp -f "${SCRIPT_DIR}/ftdetect/lowlight.vim" "${NVIM_CONFIG}/ftdetect/lowlight.vim"
cp -f "${SCRIPT_DIR}/syntax/lowlight.vim" "${NVIM_CONFIG}/syntax/lowlight.vim"
echo "  ✓ Installed ftdetect/lowlight.vim and syntax/lowlight.vim"

# 2. Install after/ftplugin/lowlight.lua (works with any Neovim config)
cat << 'EOF' > "${NVIM_CONFIG}/after/ftplugin/lowlight.lua"
-- Lowlight LSP and buffer settings
local bufnr = vim.api.nvim_get_current_buf()
local fname = vim.api.nvim_buf_get_name(bufnr)

local function find_lowlight_root(path)
  if path == '' then return vim.fn.getcwd() end
  local root = vim.fs.root(path, { 'std', '.git', 'go.mod' })
  return root or vim.fn.getcwd()
end

local root_dir = find_lowlight_root(fname)
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
  vim.keymap.set('n', 'K', vim.lsp.buf.hover, bufopts)
  vim.keymap.set('n', 'gd', vim.lsp.buf.definition, bufopts)
  vim.keymap.set('n', 'gD', vim.lsp.buf.declaration, bufopts)
  vim.keymap.set('n', 'gi', vim.lsp.buf.implementation, bufopts)
  vim.keymap.set('n', '<leader>rn', vim.lsp.buf.rename, bufopts)
  vim.keymap.set({ 'n', 'v' }, '<leader>ca', vim.lsp.buf.code_action, bufopts)
  vim.keymap.set('n', '<C-k>', vim.lsp.buf.signature_help, bufopts)
  vim.keymap.set('n', '<leader>f', function() vim.lsp.buf.format({ async = true }) end, bufopts)
  vim.keymap.set('n', '[d', vim.diagnostic.goto_prev, bufopts)
  vim.keymap.set('n', ']d', vim.diagnostic.goto_next, bufopts)
  vim.keymap.set('n', '<leader>d', vim.diagnostic.open_float, bufopts)

  -- Testing Keybindings
  vim.keymap.set('n', '<leader>tr', function() vim.cmd('!lowlight test ' .. vim.fn.expand('%')) end, vim.tbl_extend('force', bufopts, { desc = 'Run tests in file' }))
  vim.keymap.set('n', '<leader>tf', function() vim.cmd('!lowlight test ' .. vim.fn.expand('%')) end, vim.tbl_extend('force', bufopts, { desc = 'Run tests in file' }))
  vim.keymap.set('n', '<leader>ta', function() vim.cmd('!lowlight test') end, vim.tbl_extend('force', bufopts, { desc = 'Run all tests' }))

  -- Native Inlay Hints (Neovim 0.10+)
  if vim.lsp.inlay_hint then
    vim.lsp.inlay_hint.enable(true, { bufnr = bufnr })
  end

  -- Code Lens (displays '▶ Run Test' above @test functions)
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

vim.opt_local.tabstop = 4
vim.opt_local.shiftwidth = 4
vim.opt_local.expandtab = true
vim.opt_local.commentstring = '// %s'
EOF
echo "  ✓ Installed after/ftplugin/lowlight.lua"

# 3. If init.lua does not exist, provide default init.lua
if [ ! -f "${NVIM_CONFIG}/init.lua" ]; then
  cp -f "${SCRIPT_DIR}/init.lua" "${NVIM_CONFIG}/init.lua"
  echo "  ✓ Created default ${NVIM_CONFIG}/init.lua"
fi

# 4. Verify lowlight binary in PATH
if ! command -v lowlight &> /dev/null; then
  echo "  ! 'lowlight' binary not found in PATH."
  echo "    Symlinking ${REPO_DIR}/lowlight -> ${HOME}/.cargo/bin/lowlight"
  mkdir -p "${HOME}/.cargo/bin"
  ln -sf "${REPO_DIR}/lowlight" "${HOME}/.cargo/bin/lowlight"
fi

echo "=== Neovim Lowlight LSP setup complete! ==="
