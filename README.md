# Lowlight Programming Language (v1.0)

**Lowlight** is a lightweight, expression-oriented, low-level systems programming language that compiles directly to pure **LLVM IR** (default) or portable **ISO C11** (and native binaries via LLVM/Clang or your system C compiler).

It is designed around a deliberate balance:
* **Lightweight to read and write**: No lifetime annotations (`'a`), clean delimiter separation (`{ ... }` for code blocks, `[ ... ]` for data literals and type definitions), optional semicolons, expression-oriented `if` / `for` / `match` / `{}` blocks, and uniform method call syntax (UFCS).
* **Genuinely low-level systems power**: Zero garbage collection, zero reference counting, single ownership with deterministic LIFO `Drop` destructors, monomorphized generics, stack arrays (`[N: T]` / `Array[N, T]`), dynamic vectors (`[T]` / `List[T]`), lexical region bump allocators (`Arena` + `ref[T]`) for safe pointer-speed graphs without GC, packed structs, raw pointers (`ptr[T]`) inside `unsafe { ... }`, and zero-overhead C ABI FFI (`extern`).

> [!NOTE]
> **Branch Organization**:
> * **`auto-v1-prototype`**: Contains the complete, finalized v1.0 specification ([`SPECIFICATION.md`](./SPECIFICATION.md)), style guide ([`CONVENTIONS.md`](./CONVENTIONS.md)), implementation guide ([`IMPLEMENTATION.md`](./IMPLEMENTATION.md)), full changelog ([`VERSION_HISTORY.md`](./VERSION_HISTORY.md)), Go reference compiler, `std/` library, showcase examples, and strict ASan/UBSan + zero-leak test suite.
> * **`main`**: Preserved untouched at Draft v0.5 / Critique v0.7 (`f82b473`) so open design questions can later be revisited step-by-step from scratch.

---

## Quickstart

### Prerequisites
* **Go 1.22+** (zero external Go dependencies; uses only the Go standard library)
* **Clang / LLVM** (for direct LLVM IR compilation) or any **ISO C11** compiler (`cc`, `clang`, or `gcc`)

### Build the Compiler
```sh
go build -o lowlight ./cmd/lowlight
```

### Run Showcase Programs
```sh
./lowlight run examples/hello.ll
./lowlight run examples/control_flow_unwinds.ll
./lowlight run examples/ownership_and_borrows.ll
./lowlight run examples/interfaces_shapes.ll
./lowlight run examples/generics_and_pool.ll
./lowlight run examples/arena_graph.ll
./lowlight run examples/unsafe_and_ffi.ll
./lowlight run examples/mini_lexer_eval.ll
./lowlight run examples/http_server_and_client.ll
```

Verify **zero heap memory leaks** at exit on any program by setting `LOWLIGHT_CHECK_LEAKS=1`:
```sh
LOWLIGHT_CHECK_LEAKS=1 ./lowlight run examples/http_server_and_client.ll
```

---

## Documentation

The full documentation (a tour, tutorials, guides, Lowlight by Example, the specification, the standard library reference, and the implementation guide) is a static website generated from this repository:

```sh
go run ./cmd/lowlight-site -o docs
open docs/index.html
```

API documentation for your own project comes from `lowlight doc`.

---

## CLI Reference

```text
Usage:
  lowlight build [-o <bin>] [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] <file.ll>
  lowlight run   [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] <file.ll> [-- <args...>]
  lowlight test  [--backend <llvm|c11>] [--target <triple>] [--release] [--cc <compiler>] [<path...>]
  lowlight emit-llvm [-o <file.ll>] [--target <triple>] [--release] <file.ll>
  lowlight emit-c [-o <file.c>] [--target <triple>] [--release] <file.ll>
  lowlight check <file.ll>
  lowlight lsp   [--stdio] [--log <file>]
  lowlight version
  lowlight help

Flags:
  -o <path>        Output executable, LLVM IR, or C11 source path
  --backend <kind> Codegen backend: "llvm" (default) or "c11" (ISO C11 backend)
  --target <triple> Target triple for cross-compilation (e.g. "aarch64-unknown-linux-gnu")
  --release        Optimize (-O2); bounds, division, and shift checks stay on
  --cc <compiler>  C/LLVM compiler executable (default: $LOWLIGHT_CC, $CC, or clang/cc)
  --stdio          Run Language Server Protocol (LSP 3.17) over stdin/stdout
  --log <file>     Append JSON-RPC LSP trace logs to <file>
```

* **Debug builds (default)**: Compile with `-g -O0` and deterministic runtime bounds/shift checks with Lowlight `.ll:line` panic locations.
* **Release builds (`--release`)**: Compile with `-O2`. Bounds, division, and shift checks stay on, so release builds trap exactly like debug builds.

---

## VS Code Extension & Language Server (LSP)

Lowlight ships with a built-in, zero-dependency **LSP 3.17** server (`lowlight lsp --stdio`) and a ready-to-drop-in **VS Code / Cursor extension** ([`editors/vscode/`](./editors/vscode)):

* **Real-time diagnostics** as you type (syntax, types, generics, Model 2A ownership/moves/borrows, `Arena` escapes, and `?T` / `name?` optional rules).
* **Context-aware autocomplete** for `.` / `?.` fields & UFCS methods (with auto-`?` insertion on optional chains), `Struct[...]` remaining fields, `: Type` annotations, `import "..."` modules, narrowed `if (x?)` locals, intrinsics, and snippets.
* **Hover inspector**, **cross-module Go-to-Definition**, **UFCS signature help**, **document outline symbols**, **8 quick-fix code actions** (`Cmd+.`), TextMate syntax highlighting, and an editor **Run (`▶`)** button.

### Drop-In Install into VS Code
* **From VS Code UI**: Press `Cmd+Shift+P` → **`Extensions: Install from VSIX...`** → select `editors/vscode/lowlight-vscode-1.0.0.vsix` (or regenerate anytime with `python3 editors/vscode/package_vsix.py`).
* **One-line installer**:
  ```sh
  ./editors/vscode/install.sh
  ```

---

## Language Highlights

### 1. Explicit Mutability & Call-Site Transparent Ownership (Model 2A)
* `bind` is deeply immutable; `let` is mutable.
* Parameters **borrow read-only** (`x: T`) by default, **borrow mutably** (`x: &T`, requiring `&` at direct call sites so mutations are never hidden), or **consume ownership** (`x: own T`, requiring `own` when moving a named Resource variable).

```lowlight
define Buffer struct [
    tag: string,
    bytes: [u8],
]

bind Buffer.drop = fn(&self) -> {
    println("closing ${self.tag} (${self.bytes.len()} bytes)")
}

bind append_header = fn(buf: &Buffer, magic: u8) -> {
    buf.bytes.push(magic)
}

bind main = fn() -> {
    let pkt = Buffer[tag: "net", bytes: [0x01, 0x02]]
    append_header(&pkt, 0xFF) // `&` makes mutation visible at the call site
} // `pkt` deterministically runs `Buffer.drop`, then frees `bytes` and `tag`
```

### 2. Visual Optional Safety (`?T` and `name?`)
Every variable, parameter, or struct field of type `?T` **must** end with `?` (`user?`). Any identifier without `?` (`user`) is guaranteed at a glance to be non-null:

```lowlight
bind parse_port = fn(raw: string): ?int -> {
    let n = parse_int(raw) ?? func <- none
    if (n > 0 and n <= 65535) n else none
}

bind main = fn() -> {
    let port? = parse_port("8080")
    if (port?) {
        println("Listening on $port") // `port` (no `?`) is narrowed to `int`!
    }
}
```

### 3. Expression Control Flow & Targeted Unwinding (`<-`)
`{ ... }`, `if`, `for`, `while`, and `match` are expressions, and `target <- [val]` unwinds cleanly across any lexical scope while running all intermediate `Drop` destructors in reverse declaration order:
* `<- val` (enclosing `{}` block), `if <- val`, `match <- val`
* `for <- val` (break loop with value), `next <-` / `next <- val` (continue loop)
* `label <- val` / `next label <-` (labeled loops & blocks)
* `func <- val` (early function/lambda return)

### 4. Cyclic Graphs Without GC: `Arena` & `ref[T]` + Generational `Pool[T]`
* **`Arena` & `ref[T]`**: Bump-allocates graph/AST nodes (`let n = arena.alloc(Node[...])`), runs destructors on any heap fields inside arena objects when `arena` resets or drops, and statically prevents `ref[T]` from escaping the arena's lexical scope.
* **`std/pool.ll`**: Provides generic `Pool[T]` and `Handle[T]` (`index` + `generation`) for dynamic entity graphs with $O(1)$ slot reuse and safe stale-handle detection.

### 5. Low-Level `unsafe`, Raw Pointers & C ABI FFI
```lowlight
extern "string.h"
extern memcmp as c_memcmp: fn(a: ptr[u8], b: ptr[u8], n: u64): i32

define packed WireHeader struct [
    magic: u16,
    version: u8,
    flags: u8,
    payload_len: u32,
]
```

---

## Repository Structure (Compiler Blueprint)

The Go compiler is structured with clean, explicit data structures and zero external dependencies so it serves as a direct 1-to-1 blueprint for porting to C and eventual self-hosting in Lowlight:

| Path | Description |
| :--- | :--- |
| [`SPECIFICATION.md`](./SPECIFICATION.md) | Authoritative Lowlight v1.0 Language Specification |
| [`CONVENTIONS.md`](./CONVENTIONS.md) | Idiomatic Lowlight Style Guide & Naming Conventions |
| [`IMPLEMENTATION.md`](./IMPLEMENTATION.md) | Architecture, Runtime, LLVM IR & ISO C11 Backends, and Toolchain Guide |
| [`TODO.md`](./TODO.md) | Comprehensive Language, Stdlib & Self-Hosting Master Roadmap |
| [`README.md`](./README.md) | Quickstart, CLI reference, language tour & compiler overview |
| [`VERSION_HISTORY.md`](./VERSION_HISTORY.md) | Complete version history, archive index, and changelog |
| [`cmd/lowlight/`](./cmd/lowlight) | CLI entrypoint (`build`, `run`, `test`, `emit-llvm`, `emit-c`, `check`, `lsp`, `version`) |
| [`internal/diag/`](./internal/diag) | Source spans, structured error codes, and caret diagnostics with hints |
| [`internal/token/`](./internal/token) & [`internal/lexer/`](./internal/lexer) | Lexer (`name?` optional idents, string interpolation, contextual Go-style semicolon insertion) |
| [`internal/ast/`](./internal/ast) & [`internal/parser/`](./internal/parser) | Recursive-descent + Pratt expression parser with multiline trailing-comma enforcement |
| [`internal/types/`](./internal/types) | Type representations, Plain vs. Resource classification, unified `List[T]` / `Array[T]` hooks |
| [`internal/sema/`](./internal/sema) | Multi-module imports, monomorphization, Model 2A move/drop tracking, borrow exclusivity, `Arena` escape analysis, tolerant IDE check mode |
| [`internal/lsp/`](./internal/lsp) | Zero-dependency LSP 3.17 server (live diagnostics, autocomplete, hover, definition, signature help, outline, quick fixes) |
| [`internal/ir/`](./internal/ir) | Mid-Level IR (MIR) lowering with explicit scope unwind drop edges, closure environments, and vtables |
| [`internal/codegen/`](./internal/codegen) | Direct LLVM IR generator (`llvmgen.go`) + ISO C11 emitter (`cgen.go`) + embedded UB-free runtime header |
| [`internal/driver/`](./internal/driver) | End-to-end compilation, cross-compilation target plumbing, and LLVM/C11 toolchain driver |
| [`editors/vscode/`](./editors/vscode) | Zero-dependency drop-in VS Code extension, `.vsix` packager, and 1-command installer |
| [`std/`](./std) | Standard library: [`core/`](./std/core) (prelude, `List[T]`, `Array[T]`), [`math.ll`](./std/math.ll), [`fs.ll`](./std/fs.ll), [`pool.ll`](./std/pool.ll), [`net.ll`](./std/net.ll), [`http.ll`](./std/http.ll) |
| [`examples/`](./examples) | 9 end-to-end showcase programs covering every language subsystem |
| [`tests/`](./tests) | Strict test suites covering both LLVM and C11 backends, ASan/UBSan & zero-leak verification |

---

## Running the Test Suite

```sh
go test -v ./...
```

The test suite runs:
1. Lexer unit tests (unwind `<-` vs `< -`, string interpolation, auto-semicolon rules).
2. Parser unit tests (full grammar, trailing-comma rules, unwind placement restrictions).
3. Semantic safety tests (use-after-move, `bind` deep immutability, `&` borrow conflicts, `Arena` escapes, `name?` rules, exhaustiveness, `unsafe` gating).
4. LSP end-to-end tests (JSON-RPC framing, real-time syntax/ownership/optional diagnostics, all autocomplete contexts, hover, cross-file go-to-definition, UFCS signature help, document symbols, and quick fixes).
5. Strict end-to-end native compilation & execution across both LLVM and C11 backends under **`-std=c11 -Wall -Wextra -Werror -fsanitize=address,undefined`** and **`LOWLIGHT_CHECK_LEAKS=1`**.
