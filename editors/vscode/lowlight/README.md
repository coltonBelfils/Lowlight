# Lowlight Language Support for VS Code

Official **Lowlight v1.0** language extension powered by the native Go `lowlight lsp` server (zero external Node/npm dependencies).

## Features

- **Real-Time Diagnostics** (as you type, no save required):
  - Lexer & parser syntax errors (including multiline trailing-comma checks and string interpolation)
  - Full type checking, generics monomorphization, and interface structural/method satisfaction
  - **Model 2A Ownership & Borrow Safety**: use-after-move, `&` mutable borrow exclusivity, `bind` deep immutability, and `Arena` `ref[T]` lexical escape detection
  - **Visual Optional Safety**: `?T` / `name?` identifier rules, `if(x?)` flow narrowing, and `?.` optional chaining
- **Context-Aware Autocomplete & Rendered Docs**:
  - `.` and `?.` member fields, methods (UFCS & generic templates), enum variants (`Color.Red`), imported module exports (`math.lerp`), `Arena.alloc` / `reset`, and built-in slice/map/string methods (with automatic `?` insertion on optional chains!)
  - Rich Markdown docs with ``[`Symbol`]`` intra-doc links, `# Arguments`/`# Returns`/`# Errors`/`# Panics` sections, and clickable `[View source]` links across hover, completion, and signature help
- **Semantic Tokens, References & Cross-File Navigation**:
  - AST semantic highlighting (`declaration`, `readonly`, `documentation`, `defaultLibrary` modifiers)
  - Cross-file Find All References (`Shift+F12`), Go to Type Definition, Go to Declaration, Go to Implementation (`Cmd+F12`), Workspace Symbol Search (`Cmd+T`), Folding & Selection Ranges, and scope-isolated / cross-file Rename (`F2`)
- **Code Actions, Generators & Refactorings (`Cmd+.` / `Ctrl+.`)**:
  - Quick fixes for `&`, `own`/`.copy()`, `?`, `!` -> `not `, trailing commas, `?.`, missing `match` arms, top-level `let` -> `bind`, `unsafe`, `@must_use`, missing struct fields, missing interface methods, and auto-imports
  - Code generators (`Implement <Iface> for <Type>`, `<Type>.new`, Iteration B doc skeleton, `@test` scaffolding for functions and methods)
  - Refactorings (extract variable, extract function, toggle `{ ... }` single-expr block, flip `if/else`, add explicit `: Type`)
- **Canonical Formatting (`Shift+Alt+F`)**:
  - Built-in AST formatter respecting `.lowlightfmt` or VS Code `lowlight.format.*` settings (`\t` tabs, 100-char line width, `if(`/single-expression block unwrapping)
- **Run Button (`▶`)**: Run the active `.ll` file directly from the editor title bar (`Lowlight: Run Current File`)
- **Rich TextMate Highlighting & Snippets**: Full syntax coloring for `?` optional identifiers, `${...}` string interpolation, `<-` targeted unwinds, `Arena`/`own`/`unsafe`, and built-in code snippets

## Commands

| Command | Description |
| :--- | :--- |
| `Lowlight: Run Current File` | Compiles and runs the active `.ll` file in the integrated terminal |
| `Lowlight: Restart Language Server` | Restarts the `lowlight lsp --stdio` background process |
| `Lowlight: Show Language Server Output` | Opens the Lowlight Language Server log panel |

## Extension Settings

| Setting | Default | Description |
| :--- | :--- | :--- |
| `lowlight.serverPath` | `""` | Custom path to the `lowlight` binary (auto-detects bundled `bin/lowlight`, workspace `./lowlight`, or `PATH` when empty) |
| `lowlight.stdlibPath` | `""` | Custom directory containing `std/*.ll` (uses bundled `std/` or workspace `std/` when empty) |
| `lowlight.trace` | `false` | Log raw JSON-RPC LSP traffic to the Output panel |
| `lowlight.format.indent` | `"tabs"` | Default indentation (`"tabs"` or `"spaces"`) when `.lowlightfmt` is absent |
| `lowlight.format.tabWidth` | `4` | Visual tab width / spaces-per-indent when `.lowlightfmt` is absent |
| `lowlight.format.lineWidth` | `100` | Maximum formatter line width when `.lowlightfmt` is absent |
| `lowlight.inlayHints.types` | `true` | Display inferred type inlay hints |
| `lowlight.inlayHints.parameters` | `true` | Display parameter name inlay hints on calls |
