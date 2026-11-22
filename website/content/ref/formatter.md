---
title: Formatting
summary: lowlight fmt, the standard format, and its settings.
order: 1001
group: Tools
---

# Formatting

Lowlight has one standard format, produced by `lowlight fmt`. Formatted code looks the same everywhere, and code reviews never discuss spacing.

```bash
lowlight fmt -w .              # format every file in place
lowlight fmt --check .         # exit with status 1 if anything is unformatted (for CI)
lowlight fmt file.ll           # print the formatted file
```

Editors format with the same engine through the language server, and every code action and generated snippet produces exactly what `lowlight fmt` would.

## What the formatter does

- Indents with tabs (by default), one level per block or body.
- Puts a single-expression body on the header's line when it fits (`bind f = fn(x: int) int: x + 1`), and on the next line, indented, when it doesn't.
- Normalizes spacing around operators, after commas and colons, and inside brackets.
- Breaks long argument lists, literals, and parameter lists one item per line, with the trailing comma that multi-line lists require.
- Keeps your comments and the blank lines that separate groups of statements.

## Settings

There are three settings, and only when you need them:

| Setting | Default | Meaning |
|---|---|---|
| `indent_style` | `"tabs"` | `"tabs"` or `"spaces"` |
| `tab_width` | `4` | display width of a tab; number of spaces when indenting with spaces |
| `line_width` | `100` | the column at which long lines break; `0` disables wrapping |

Set them for a project in `lowlight.toml`:

```toml
[format]
indent_style = "spaces"
tab_width = 2
line_width = 90
```

or for a directory tree with a `.lowlightfmt` file, which takes precedence over the manifest:

```text
indent_style = spaces
tab_width = 2
```

(`.lowlightfmt` may also be a JSON object with the same keys.) Command-line flags override both: `--indent tabs|spaces`, `--tab-width N`, `--line-width N`.

## Style beyond formatting

Naming, structure, and idioms are covered by [Effective Lowlight](/guides/effective-lowlight.html).
