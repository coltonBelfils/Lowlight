# Lowlight Language Support for VS Code

Official **Lowlight v1.0** language extension powered by the native Go `lowlight lsp` server (zero external Node/npm dependencies).

## Features

- **Real-Time Diagnostics** (as you type, no save required):
  - Lexer & parser syntax errors (including multiline trailing-comma checks and string interpolation)
  - Full type checking, generics monomorphization, and interface structural/method satisfaction
  - **Model 2A Ownership & Borrow Safety**: use-after-move, `&` mutable borrow exclusivity, `bind` deep immutability, and `Arena` `ref[T]` lexical escape detection
  - **Visual Optional Safety**: `?T` / `name?` identifier rules, `if (x?)` flow narrowing, and `?.` optional chaining
- **Context-Aware Autocomplete**:
  - `.` and `?.` member fields, methods (UFCS & generic templates), enum variants (`Color.Red`), imported module exports (`math.lerp`), `Arena.alloc` / `reset`, and built-in slice/map/string methods (with automatic `?` insertion on optional chains!)
  - `Struct[...]` remaining field completion
  - `: Type` annotations (primitives, structs, enums, interfaces, `Arena`, `ref[T]`, `ptr[T]`)
  - `import "..."` standard library (`std/math`, `std/fs`, `std/pool`) and workspace `.ll` modules
  - Scope-aware locals (including narrowed `if (x?)` bindings), parameters, functions, intrinsics, keywords, and snippets
- **Hover Inspector**: Displays concrete inferred types, `bind` vs `let` mutability, Plain vs Resource (`Drop`) status, and function/method signatures
- **Cross-Module Go-to-Definition (`F12` / `Cmd+Click`)**: Jump to structs, enums, interfaces, fields, functions, methods, parameters, and local bindings across `.ll` files and `std/`
- **UFCS-Aware Signature Help (`(` / `,`)**: Active parameter highlighting for both `f(a, b)` and `a.f(b)` method calls
- **Outline & Breadcrumbs (`Cmd+Shift+O`)**: Hierarchical symbols for structs, fields, enums, variants, interfaces, functions, and methods
- **One-Click Quick Fixes (`Cmd+.` / `Ctrl+.`)**:
  - Insert missing `&` on mutable borrow arguments
  - Insert `own ` or `.copy()` when passing Resource values
  - Fix missing or unexpected `?` on optional/non-optional identifiers
  - Replace `!x` with `not x`
  - Insert missing trailing `,` in multiline lists
  - Insert `?` before `.` for optional chaining (`x?.field`)
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
