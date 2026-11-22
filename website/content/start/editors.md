---
title: Editor setup
nav: Editors
summary: Set up VS Code, Neovim, or a JetBrains IDE with the Lowlight language server.
order: 4
---

# Editor setup

Every Lowlight editor integration is powered by the same **language server**, built into the compiler: `lowlight lsp --stdio`. Editors get the compiler's own diagnostics, so what your editor reports is exactly what `lowlight check` reports.

## What the language server provides

- **Diagnostics as you type**: syntax, types, ownership and borrowing, exhaustiveness, and the `?`/`!` naming rules, underlined precisely (only the part of an expression that is wrong).
- **Completion** for members, methods, enum variants, result members, imports, and struct fields.
- **Hover** documentation rendered from doc comments, with links to other symbols.
- **Navigation**: go to definition, type definition, implementation, find references, workspace symbols, rename.
- **Quick fixes**: add a missing `&` or `own`, rename a variable to end in `?` or `!`, add missing `match` arms or struct fields, implement an interface, add an import, and more.
- **Generators and refactorings**: `Type.new` constructors, doc comment skeletons, `@test` scaffolds, extract variable or function, flip `if`/`else`.
- **Formatting** identical to `lowlight fmt`, inlay hints for inferred types and parameter names, semantic highlighting, and run/test code lenses.

## VS Code and Cursor

The extension lives in `editors/vscode/`. Install it with:

```bash
./editors/vscode/install.sh
```

or from the Command Palette: **Extensions: Install from VSIX…** and choose `editors/vscode/lowlight-vscode-*.vsix`.

The extension finds the `lowlight` executable on your `PATH` (or in the open workspace). Settings:

| Setting | Default | Meaning |
|---|---|---|
| `lowlight.serverPath` | `""` | path to the `lowlight` executable |
| `lowlight.stdlibPath` | `""` | directory containing `std/` |
| `lowlight.format.indent` | `"tabs"` | `"tabs"` or `"spaces"`, when no `.lowlightfmt` is present |
| `lowlight.format.tabWidth` | `4` | indent width |
| `lowlight.format.lineWidth` | `100` | maximum line width |
| `lowlight.inlayHints.types` · `.parameters` | `true` | show inferred types and parameter names |
| `lowlight.trace` | `false` | log language server traffic |

The **▶ Run** button in the editor title bar runs the current file, and the Testing panel lists and runs every `@test`.

## Neovim

```bash
./editors/nvim/install.sh
```

The installer adds file detection, syntax highlighting, formatter-matching indentation, and an `ftplugin` that attaches the language server in any Neovim configuration (0.10 or newer recommended). A [neotest](https://github.com/nvim-neotest/neotest) adapter in `editors/nvim/lua/neotest-lowlight` runs tests from the editor.

| Keys | Action |
|---|---|
| `K` | hover documentation |
| `gd` · `gD` · `gy` · `gi` | definition · declaration · type definition · implementation |
| `gr` · `gS` | references · workspace symbols |
| `<leader>ca` | code actions (quick fixes, generators, refactorings) |
| `<leader>rn` | rename |
| `<leader>f` | format |
| `<C-k>` | signature help |
| `[d` · `]d` · `<leader>d` | previous, next, and current diagnostic |
| `<leader>rr` | run the current file |

## JetBrains IDEs

The plugin in `editors/jetbrains/` works in IntelliJ IDEA, CLion, GoLand, RustRover, WebStorm, PyCharm, and Android Studio (2024.1 or newer). It uses the IDE's built-in LSP support when available, and otherwise the LSP4IJ plugin.

```bash
./editors/jetbrains/install.sh
```

or build it with `./gradlew buildPlugin` in `editors/jetbrains` and use **Install Plugin from Disk…**. Configure it under **Settings › Languages & Frameworks › Lowlight**. The **Tools › Lowlight** menu runs files and tests, formats, and restarts the server; tests appear in the IDE's test runner with clickable failure locations.

## Other editors

Any editor with LSP support works. Configure it to start:

```bash
lowlight lsp --stdio
```

for files ending in `.ll`. Add `--log /tmp/lowlight-lsp.log` to record the protocol traffic when something goes wrong.
