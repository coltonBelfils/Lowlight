---
title: The lowlight command
nav: Command-line tool
summary: Every subcommand and flag of the lowlight command.
order: 1000
group: Tools
---

# The `lowlight` command

```text
lowlight <command> [flags] [arguments]
```

Run `lowlight help` for a summary. Inside a project, commands act on the current package by default; outside one, they take a file or directory.

## Building and running

| Command | Does |
|---|---|
| `lowlight run [path] [-- args...]` | compile and run a program; arguments after `--` go to the program |
| `lowlight build [-o out] [path]` | compile an executable (named after the file or package by default) |
| `lowlight check [path]` | report errors without generating code (`--all-targets` checks every canonical OS/arch target) |
| `lowlight expand [-o file.ll] [path]` | print readable Lowlight after `const` evaluation, `comptime` unrolling, and `@derive` expansion |
| `lowlight test [paths...]` | compile and run tests ([Testing](/guides/testing.html)) |
| `lowlight emit-llvm [-o file.ll] [path]` | write the generated LLVM IR |
| `lowlight emit-c [-o file.c] [path]` | write the generated C11 source |

Flags shared by these commands:

| Flag | Meaning |
|---|---|
| `--release` | optimize (`-O2`); safety checks stay on |
| `--backend llvm\|c11` | choose the code generator (default `llvm`) |
| `--target <triple>` | cross-compile, for example `aarch64-unknown-linux-gnu` |
| `--all-targets` | check every canonical platform target (`lowlight check`) |
| `--cc <compiler>` | the C or LLVM compiler to invoke (default `$LOWLIGHT_CC`, `$CC`, `clang`, then `cc`) |
| `-p, --package <name>` | select a workspace member |
| `--workspace` | act on every workspace member (`build`, `test`, `check`) |
| `--locked` | require `lowlight.lock` to match `lowlight.toml` |
| `--offline` | forbid network access |

`test` also accepts `-run <regex>` (only matching tests), `--list` (list without running), and `--json` (one JSON event per line, for tools).

## Projects and dependencies

| Command | Does |
|---|---|
| `lowlight new <dir> [--lib\|--bin] [--name <n>]` | create a project in a new directory |
| `lowlight init [--lib\|--bin] [--name <n>] [dir]` | create a project in an existing directory |
| `lowlight add <name> --path <dir>` | add a local dependency |
| `lowlight add <name> --url <url> [--hash sha256:...]` | add an archive dependency (computes the hash if omitted) |
| `lowlight add <name> --index <url> --version <constraint>` | add a versioned dependency from an index |
| `lowlight remove <name>` | remove a dependency |
| `lowlight fetch` | download what the lockfile lists |
| `lowlight update [names...]` | re-resolve to newer allowed versions |
| `lowlight vendor` | copy dependencies into `vendor/` |
| `lowlight tree` | print the dependency graph |

`add` and `remove` take `--dev` for dev-dependencies and `--override` for `[overrides]`. They preserve the manifest's comments and layout.

## Tools

| Command | Does |
|---|---|
| `lowlight fmt [-w] [--check] [paths...]` | format source ([Formatting](formatter.html)) |
| `lowlight doc [-o dir] [--format html\|md] [--check-links] [paths...]` | generate API documentation ([Documentation comments](documentation.html)) |
| `lowlight lsp --stdio [--log file]` | run the language server for editors |
| `lowlight version` | print the version |

## Environment variables

| Variable | Meaning |
|---|---|
| `LOWLIGHT_ROOT` | the directory containing `std/`, when it can't be found next to the executable |
| `LOWLIGHT_CC` | the default C compiler |
| `LOWLIGHT_BACKEND` | the default backend (`llvm` or `c11`) |
| `LOWLIGHT_CACHE` | the dependency and build cache (default `~/.lowlight/cache`) |
| `LOWLIGHT_CHECK_LEAKS=1` | make a program verify at exit that it freed every allocation |

## Exit status

`build`, `check`, `test`, and the other commands exit with status 0 on success and 1 on any error or test failure. `run` exits with the program's own status.
