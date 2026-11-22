# Lowlight Neovim Setup

Complete Neovim IDE support for the Lowlight systems programming language, featuring:
- **Real-Time LSP Diagnostics**: Type errors, ownership/borrow checks, and exhaustiveness checks via `lowlight lsp --stdio`.
- **Exhaustive Pattern Matching Quick-Fixes**: One-click generation of missing enum variants (`<leader>ca`).
- **Signature Help**: Auto-display parameter names and types on functions, UFCS methods, and enum variant constructors (`<C-k>`).
- **Inlay Hints**: Automatic inline display of inferred variable types and call argument names (Neovim 0.10+ native).
- **Go To Definition (`gd`) & Hover (`K`)**.
- **Rename Symbol (`<leader>rn`)**.
- **Document Formatting (`<leader>f`)**.
- **Full Syntax Highlighting** for keywords, types, string interpolation (`$...`), operators (`<-`, `?.`, `??`, `&`), comments, `!` result identifiers, `define Name result [...]`, and `default panic`.
- **Formatter-Matching Indentation**: `indentexpr` reproduces `lowlight fmt` indentation, so `=` re-indents formatted code unchanged and typing indents bodies after `:` headers (`if(x):`, `bind f = () int:`, `else:`, `when X:`, `load(path):`), aligns `else` with its `if`, and returns to the statement level after a single-expression body, `}`, or a chain's `default` arm. Enter inside a `//` comment continues it.

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
| `<leader>tr` | **Run Nearest Test** | Run the `@test` nearest the cursor (or current file) asynchronously via `lowlight test --json` with inline virtual text, failure diagnostics, and quickfix navigation |
| `<leader>tf` | **Run File Tests** | Run all tests in the current file asynchronously via `lowlight test --json` |
| `<leader>ta` | **Run All Tests** | Run entire test suite across the project with quickfix + trace navigation |
| `<leader>tt` | **Pick & Run Test** | Discover all `@test` cases (`lowlight test --list --json`) and run one interactively |
| `<leader>to` | **Test Output** | Open the formatted test output split (`gF` / `<CR>` jumps directly to `file:line:col`) |

---

## 3. Native Test Runner & `neotest-lowlight`

Out of the box (no extra plugins required), running tests (`<leader>tr` / `<leader>tf` / `<leader>ta` / `:LowlightTestPick`) streams `lowlight test --json` asynchronously, annotates `@test` lines with inline `✓ <duration>` / `✗ failed` virtual text, places `vim.diagnostic` errors on failing assertions, populates the quickfix list with failure and stack trace locations (`at file.ll:line:col`), and lets you press `gF` on any link in `:LowlightTestOutput`.

If you use **neotest**, a native `neotest-lowlight` adapter is included and installed automatically to `lua/neotest-lowlight`:

```lua
require('neotest').setup({
  adapters = {
    require('neotest-lowlight'),
  },
})
```

---

## 4. Editor-Agnostic Interface Discovery & Dropdown Navigation

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
