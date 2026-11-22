---
title: Contributing
summary: How the repository is organized, how to build and test the compiler, and how to work on the language, library, and website.
order: 5
---

# Contributing

## Repository layout

| Path | Contains |
|---|---|
| `cmd/lowlight/` | the `lowlight` command |
| `cmd/lowlight-site/` | the website generator |
| `internal/` | the compiler, tools, and language server ([Internals](/internals/index.html)) |
| `std/` | the standard library, written in Lowlight |
| `tests/` | end-to-end tests |
| `examples/` | larger showcase programs |
| `editors/` | VS Code, Neovim, and JetBrains integrations |
| `benchmarks/` | benchmarks with C and Go equivalents |
| `website/` | the website's hand-written content and examples |
| `SPECIFICATION.md` | the language definition |
| `CONVENTIONS.md` | the style guide |
| `IMPLEMENTATION.md` | how the compiler and runtime are built |
| `TODO.md` | known issues and planned work |

## Building and testing

```bash
go build -o lowlight ./cmd/lowlight
go test ./...
```

The test suite covers the lexer, parser, semantic analysis, code generation (with both backends, under AddressSanitizer and leak checking), the standard library, the language server, the formatter, the documentation generator, and this website. Tests run in parallel; a full run takes about a minute.

Lowlight's own test files (`*_test.ll`) run with `lowlight test`, for example:

```bash
./lowlight test std/strings
```

## Changing the language

The specification is the source of truth. A language change updates, together:

1. `SPECIFICATION.md`: the rule, in the right chapter, and the grammar in Appendix A if syntax changes;
2. the compiler, with tests in `tests/` and the affected `internal/` packages;
3. the language server's completion, hover, and quick fixes, if they involve the change;
4. `CONVENTIONS.md`, if idiomatic style changes;
5. the editor syntax files in `editors/`, if keywords or syntax change;
6. the website's tour and guides, where they cover the feature.

Examples in the specification are runnable, and so are examples on this website: if a change breaks one, the test suite says which.

## Changing the standard library

Standard library packages are ordinary Lowlight in `std/`. Document every exported declaration with a doc comment whose first sentence stands alone (it becomes the summary in indexes and search). Follow the failure conventions: fallible operations return results or optionals, and their panicking twins are named `must_*`. Add tests in the package's `_test.ll` file.

## The website

The site is built by:

```bash
go run ./cmd/lowlight-site -o docs
```

and written to `docs/`. Open `docs/index.html` in a browser; the site works directly from disk.

| Source | Becomes |
|---|---|
| `website/content/**/*.md` | the home page, Get Started, Tour, Tutorials, Guides, Reference tools, and About pages |
| `website/examples/*.ll` (and `.out`) | Lowlight by Example |
| `SPECIFICATION.md` | the specification, one page per chapter |
| `CONVENTIONS.md` | Effective Lowlight |
| `IMPLEMENTATION.md` | Internals, one page per section |
| `VERSION_HISTORY.md` | Version history |
| doc comments in `std/` | the package reference |

**Content pages** are Markdown with a front matter block:

```text
---
title: Ownership and borrowing
nav: Ownership          (shorter sidebar label, optional)
summary: One sentence for search results and link previews.
order: 10               (position within the section)
group: Core ideas       (sidebar group, optional)
---
```

The section is the page's directory. Links may point to other pages by site path (`/guides/errors.html`), to repository Markdown files (`SPECIFICATION.md#61-bodies`, resolved to the right page), or to standard library symbols with [`List.push`]-style references.

**Code blocks** tagged `lowlight` that contain `bind main` are compiled and run by `go test ./tests -run TestWebsite`. Put the expected output in an ```` ```output ```` block right after the program, and the test compares it exactly. Mark a block `no-run` (` ```lowlight no-run `) to only type-check it, such as a server or a program that reads files; `fragment` for an excerpt that isn't a complete program; and `panics` for a program that is supposed to panic (its output block then includes the panic message).

**Examples** are `.ll` files with a header and prose comments:

```lowlight fragment
// Title: Hello, world
// Category: Basics
// Order: 1

// Prose for the left column, in Markdown, written as comments at column 0.
// The code that follows appears on the right.
bind main = fn(): println("hello, world")
```

Regenerate their expected output after changing them:

```bash
go run ./cmd/lowlight-site -update-examples -o docs
```

`go run ./cmd/lowlight-site -strict` fails on any broken internal link or anchor, and the test suite builds the site the same way.

The generator lives in `internal/site` (pages, theme, search, link checking, and the package reference) and `internal/markdown` (a dependency-free Markdown renderer). `lowlight doc` uses the same package-reference pages for any Lowlight project.
