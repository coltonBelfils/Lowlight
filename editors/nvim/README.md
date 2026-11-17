# Lowlight Neovim Setup

Complete Neovim IDE support for the Lowlight systems programming language, featuring:
- **Real-Time LSP Diagnostics**: Type errors, ownership/borrow checks, and exhaustiveness checks via `lowlight lsp --stdio`.
- **Exhaustive Pattern Matching Quick-Fixes**: One-click generation of missing enum variants (`<leader>ca`).
- **Signature Help**: Auto-display parameter names and types on functions, UFCS methods, and enum variant constructors (`<C-k>`).
- **Inlay Hints**: Automatic inline display of inferred variable types and call argument names (Neovim 0.10+ native).
- **Go To Definition (`gd`) & Hover (`K`)**.
- **Rename Symbol (`<leader>rn`)**.
- **Document Formatting (`<leader>f`)**.
- **Full Syntax Highlighting** for keywords, types, string interpolation (`$...`), operators (`<-`, `?.`, `??`, `&`), and comments.

---

## 1. Quick Install

Run the automated installer script:

```bash
./editors/nvim/install.sh
```

This installs:
1. `~/.config/nvim/ftdetect/lowlight.vim` (detects `.ll` files)
2. `~/.config/nvim/syntax/lowlight.vim` (syntax highlighting)
3. `~/.config/nvim/after/ftplugin/lowlight.lua` (zero-conflict LSP attachment for any Neovim setup)
4. Symlinks `lowlight` compiler binary to `~/.cargo/bin/lowlight` (or ensures it is on your `$PATH`).

---

## 2. Keybindings Reference

| Shortcut | Action | Description |
|---|---|---|
| `K` | **Hover** | Show Markdown documentation, type signatures, and descriptions |
| `gd` | **Go to Definition** | Jump to symbol definition (or doc link ``[`Symbol`]``) |
| `gD` | **Go to Declaration** | Jump to declaration signature or extern declaration |
| `gy` | **Go to Type Definition** | Jump to the type definition of the variable/expression under cursor |
| `gi` | **Go to Implementation** | Jump directly to type implementation, or interface implementations |
| `gr` | **Find References** | Cross-file reference search across the workspace |
| `gS` | **Workspace Symbols** | Search workspace symbols across all `.ll` files |
| `<leader>ca` | **Code Actions** | Quick-fixes, generators, and refactorings (`extract`, `flip`, `{}` toggle) |
| `<leader>rn` | **Rename Symbol** | Safe cross-file / scope-isolated symbol rename |
| `<C-k>` | **Signature Help** | Trigger parameter info, descriptions, and active parameter indicator |
| `<leader>f` | **Format Document** | Format buffer using canonical Lowlight style (`\t` tabs, `.lowlightfmt`) |
| `[d` / `]d` | **Prev / Next Diagnostic** | Jump between compiler warnings and errors |
| `<leader>d` | **Line Diagnostic** | Open floating window with full diagnostic explanation and hints |
| `<leader>cl` | **Run CodeLens** | Trigger inline CodeLens at cursor (`▶ Run` main, `▶ Run Test`, or Implementations dropdown) |
| `<leader>rr` | **Run File** | Run `main` in current file (`lowlight run %`) |
| `<leader>tr` | **Run Test** | Run tests in current file via `lowlight test %` |
| `<leader>tf` | **Run File Tests** | Run all tests in the current file |
| `<leader>ta` | **Run All Tests** | Run entire test suite across the project (`lowlight test`) |

---

## 3. Editor-Agnostic Interface Discovery & Dropdown Navigation

The Lowlight Language Server Protocol (LSP) works identically across Neovim, VS Code, and other editors:
- **Interface Implementations Dropdown**: In Neovim, triggering the implementation CodeLens (via `<leader>cl` or `gi`) opens an interactive dropdown menu powered by `vim.ui.select`. It integrates seamlessly with whatever picker you use (built-in Neovim float menu, `telescope.nvim`, `fzf-lua`, or `dressing.nvim`).
- **Inline Run Buttons**: CodeLenses are placed above `main` (`▶ Run`) and `@test` functions (`▶ Run Test`). Running the CodeLens executes the file or test directly in your terminal buffer.

---

## 3. Using with Existing Plugin Managers

### LazyVim / Kickstart / Custom Config
Because the installer places LSP attachment logic into `~/.config/nvim/after/ftplugin/lowlight.lua`, it works automatically with any existing Neovim configuration without modifying your plugin manager files.

### Using with `nvim-lspconfig` (Optional)
If you prefer managing all servers strictly through `nvim-lspconfig`, you can add:

```lua
local lspconfig = require('lspconfig')
local configs = require('lspconfig.configs')

if not configs.lowlight then
  configs.lowlight = {
    default_config = {
      cmd = { 'lowlight', 'lsp', '--stdio' },
      filetypes = { 'lowlight' },
      root_dir = function(fname)
        return require('lspconfig.util').root_pattern('std', '.git', 'go.mod')(fname) or vim.fn.getcwd()
      end,
      settings = {},
    },
  }
end

lspconfig.lowlight.setup({})
```
