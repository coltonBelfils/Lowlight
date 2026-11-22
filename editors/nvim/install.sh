#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Installing Lowlight Neovim Support ==="

NVIM_CONFIG="${HOME}/.config/nvim"
mkdir -p "${NVIM_CONFIG}/ftdetect"
mkdir -p "${NVIM_CONFIG}/syntax"
mkdir -p "${NVIM_CONFIG}/plugin"

# 1. Install ftdetect & syntax
cp -f "${SCRIPT_DIR}/ftdetect/lowlight.vim" "${NVIM_CONFIG}/ftdetect/lowlight.vim"
cp -f "${SCRIPT_DIR}/syntax/lowlight.vim" "${NVIM_CONFIG}/syntax/lowlight.vim"
echo "  ✓ Installed ftdetect/lowlight.vim and syntax/lowlight.vim"

# 2. Install plugin/lowlight.lua directly from editors/nvim/init.lua and neotest-lowlight adapter
rm -f "${NVIM_CONFIG}/after/ftplugin/lowlight.lua"
cp -f "${SCRIPT_DIR}/init.lua" "${NVIM_CONFIG}/plugin/lowlight.lua"
mkdir -p "${NVIM_CONFIG}/lua/neotest-lowlight"
cp -f "${SCRIPT_DIR}/lua/neotest-lowlight/init.lua" "${NVIM_CONFIG}/lua/neotest-lowlight/init.lua"
echo "  ✓ Installed plugin/lowlight.lua and lua/neotest-lowlight/init.lua"

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
