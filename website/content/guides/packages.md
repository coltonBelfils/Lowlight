---
title: Packages and dependencies
summary: Projects, imports, visibility, the manifest, lockfiles, workspaces, vendoring, and platform-specific settings.
order: 20
group: Working with code
---

# Packages and dependencies

## Files, packages, projects

- A **file** (`.ll`) can be compiled and run on its own: `lowlight run tool.ll`.
- A **package** is all the `.ll` files in one directory. They share a namespace: declarations in one file are visible in the others without imports. A package with a `main` function is a program; one without is a library.
- A **project** is a directory tree with a `lowlight.toml` at its root. Each subdirectory is a package named by its path: the directory `myapp/lexer/` is the package `myapp/lexer`.

```text
myapp/
├── lowlight.toml
├── lowlight.lock
├── main.ll            # package "myapp"
├── lexer/             # package "myapp/lexer"
│   ├── scanner.ll
│   └── scanner_test.ll
├── cmd/tool/main.ll   # a second program: lowlight build ./cmd/tool
└── tests/             # black-box test packages
```

Prefer single-file `comptime if(comp.TARGET...)` ([Compile-time evaluation](/guides/comptime.html#target-selection-and-cross-compilation)) for conditional platform code so every platform's implementation lives together and can be verified with `lowlight check --all-targets`. Filename suffixes (`net_linux.ll`, `net_darwin.ll`, `net_windows.ll`, `net_unix.ll`, or `simd_arm64.ll`) remain supported for splitting larger platform-specific files.

## Imports and names

```lowlight fragment
import "std/fs"              // the standard library
import "myapp/lexer"         // a package in this project
import "jsonx"               // a dependency
import "myapp/lexer" as lx   // with a different name
```

Imported names are always qualified (`fs.read_file`, `lexer.Token`), so you can tell where every name comes from. Importing a package also brings in the methods it adds to types defined elsewhere, which is how `std/sort` gives lists a `sort` method.

Packages may not import each other in a cycle. Within a package, declarations may refer to each other in any order.

## Visibility

A name is **exported** unless it starts with `_`. This applies to functions, types, constants, methods, struct fields, enum variants, and interface methods.

- A struct with a private field can't be built with a literal, or have that field read, outside its package. Export a constructor to control how values are made.
- An interface with a private method is **sealed**: only its own package can implement it.
- Methods added to another package's type can't see that type's private members.

## The manifest

```toml
[package]
name = "myapp"
version = "0.2.0"
edition = "2026"

[dependencies]
shared = { path = "../shared" }
jsonx = { url = "https://example.org/jsonx-1.2.0.tar.gz", hash = "sha256:9a3f..." }
http_ext = { index = "https://acme.dev/pkgs/http_ext.toml", version = "^0.3" }

[dev-dependencies]
benchkit = { path = "../benchkit" }

[build]
backend = "llvm"
c_libs = ["m"]

[format]
indent_style = "tabs"
line_width = 100
```

| Dependency source | Written | Use for |
|---|---|---|
| local path | `{ path = "../shared" }` | code in the same repository or checkout |
| archive | `{ url = "...", hash = "sha256:..." }` | a specific release, pinned by content |
| index | `{ index = "...", version = "^1.2" }` | versioned packages with resolution |

`[dev-dependencies]` are available only to tests. The [manifest reference](/spec/packages-manifests-and-dependencies.html#142-the-manifest) lists every table and field.

## Commands

```bash
lowlight add jsonx --url https://example.org/jsonx-1.2.0.tar.gz   # computes the hash
lowlight add shared --path ../shared
lowlight remove jsonx
lowlight fetch           # download exactly what lowlight.lock lists
lowlight update          # move to the newest versions the constraints allow
lowlight update jsonx    # just one
lowlight tree            # show the dependency graph
lowlight vendor          # copy all dependencies into ./vendor
```

## Reproducible builds

- **`lowlight.lock`** records the exact version, URL, and SHA-256 hash of every dependency. Commit it.
- **Builds never use the network.** `build`, `run`, `test`, and `check` read only the local cache (`~/.lowlight/cache`, or `$LOWLIGHT_CACHE`) and `vendor/`. If something is missing, they tell you to run `lowlight fetch`.
- **`--locked`** fails if the lockfile doesn't match the manifest, which is what you want in CI. `--offline` forbids network access even for `fetch` and `update`.
- Every downloaded archive is checked against its hash before it is unpacked.

## Version resolution

Lowlight uses **minimal version selection**: from all the constraints on a dependency in the whole build, it picks the *smallest* version that satisfies them all. A build never silently moves to a newer version because one was published; only `lowlight update` does that. There is exactly one version of each dependency in a build.

Constraints use SemVer operators: `^1.2` (compatible with 1.2), `~1.2.3` (patch updates), `=1.2.3`, comparisons (`>=1.0, <2.0`), and `*`.

## Overrides, workspaces, and vendoring

- **`[overrides]`** in the root manifest replaces a dependency everywhere in the graph, including transitively, with a local path or a different archive. Use it to test a fix to a dependency.
- **`[workspace]`** groups several packages in one repository, with one lockfile and shared overrides: `members = ["libs/*", "cmd/server"]`. Select a member with `-p name`, or operate on all with `--workspace`.
- **`vendor/`**: when present, it is authoritative, and nothing is fetched. `lowlight vendor` creates it.

## Platform-specific settings

```toml
[target.linux]
c_libs = ["ssl", "crypto"]

[target.darwin.dependencies]
cocoa_bindings = { path = "../cocoa" }

[target."aarch64-unknown-linux-gnu"]
c_flags = ["-mcpu=neoverse-n1"]
```

Selectors are an OS (`linux`, `darwin`, `windows`), `unix`, an architecture (`amd64`, `arm64`, `riscv64`), or a full target triple. Cross-compile with `--target <triple>`.

## Hosting packages

An index is a single static TOML file listing versions, archive URLs, hashes, and each version's dependencies, so any web server or object store can host packages. No registry service is needed. See [the index format](/spec/packages-manifests-and-dependencies.html#145-package-indexes).
