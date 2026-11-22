---
title: Installing Lowlight
nav: Install
summary: Build the Lowlight compiler from source and verify the installation.
order: 1
---

# Installing Lowlight

The Lowlight toolchain is a single executable, `lowlight`, that compiles, runs, tests, formats, and documents code, manages dependencies, and serves as a language server for editors. It is written in Go with no third-party dependencies.

## Requirements

| Tool | Why | Check with |
|---|---|---|
| **Go 1.22 or newer** | builds the `lowlight` executable | `go version` |
| **Clang** (recommended) or any C11 compiler | turns generated LLVM IR or C into native code | `clang --version` |
| **Git** | fetches the source | `git --version` |

On macOS, `xcode-select --install` provides Clang and Git. On Debian or Ubuntu, `sudo apt install clang git golang`. On Windows, use WSL.

## Build the compiler

```bash
git clone <lowlight-repository-url> lowlight
cd lowlight
go build -o lowlight ./cmd/lowlight
```

This produces a `lowlight` executable in the repository root. The standard library is the `std/` directory beside it; the compiler finds it automatically when the executable lives in the repository.

## Put it on your PATH

Either add the repository directory to your `PATH`:

```bash
export PATH="$HOME/lowlight:$PATH"
```

or copy the executable elsewhere and tell it where the standard library lives:

```bash
cp lowlight /usr/local/bin/
export LOWLIGHT_ROOT="$HOME/lowlight"
```

`LOWLIGHT_ROOT` must name the directory that **contains** `std/`. Add the `export` lines to your shell profile (`~/.zshrc`, `~/.bashrc`) to make them permanent.

## Verify

```bash
lowlight version
lowlight run examples/hello.ll
```

If `run` fails with an error about missing standard library files, `LOWLIGHT_ROOT` is not set to the right directory.

## Choosing a C compiler

By default, `lowlight` uses `$LOWLIGHT_CC`, then `$CC`, then `clang`, then `cc`. Override it per command with `--cc`:

```bash
lowlight run --cc gcc --backend c11 hello.ll
```

The default **LLVM backend** emits LLVM IR and needs Clang. The **C11 backend** (`--backend c11`) emits a single portable C file and works with any C11 compiler. Both produce the same program behavior.

## Updating

```bash
cd ~/lowlight
git pull
go build -o lowlight ./cmd/lowlight
```

## Next

[Write your first program →](hello-world.html)
