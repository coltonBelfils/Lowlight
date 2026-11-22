---
title: Documentation comments
summary: Writing doc comments and generating API documentation with lowlight doc.
order: 1002
group: Tools
---

# Documentation comments

## Writing doc comments

A comment directly above a declaration documents it. A comment at the top of a file, separated from the first declaration by a blank line, documents the package.

```lowlight fragment
// Package geometry provides points, vectors, and shapes in two dimensions.

// A point in the plane. See [`Point.distance`] for measuring between points.
define Point struct [
	// Horizontal coordinate.
	x: float,
	// Vertical coordinate.
	y: float,
]

// Returns the straight-line distance between `self` and `other`.
//
// # Examples
//
// ```lowlight
// let d = Point[x: 0.0, y: 0.0].distance(Point[x: 3.0, y: 4.0])   // 5.0
// ```
bind Point.distance = (self, other: Point) float: ...
```

Doc comments are Markdown:

- The **first sentence** is the summary shown in indexes, search results, and completion lists. Make it stand alone.
- **Links to symbols** are written in brackets and backticks: [`Point`], [`Point.distance`], [`fs.read_file`], [`List.push`]. They resolve the way names do in code: the current package first, then imports and the prelude. `[the docs][`Point`]` sets the link text.
- **Sections** use headings. Common ones are `# Arguments`, `# Returns`, `# Errors` (the failures a result can hold), `# Panics`, and `# Examples`.
- **Code blocks** fenced with ```` ```lowlight ```` are highlighted.

Private declarations (names starting with `_`) and test code are never documented.

## Generating documentation

```bash
lowlight doc                    # HTML for the current project, into ./docs
lowlight doc -o site/api        # choose the output directory
lowlight doc --format md        # Markdown instead of HTML
lowlight doc --check-links      # report unresolved [`Symbol`] links, without writing anything
```

The HTML output has one page per package: an overview from the package comment, an index, and every constant, function, type (with fields, variants, and methods), and method added to types from other packages, each linking to its highlighted source. Pages work offline, include search (press <kbd>/</kbd>), and follow your system's light or dark mode.

Run `lowlight doc --check-links` in CI to keep doc links from going stale.

## In the editor

The language server renders the same doc comments in hover cards and completion details, with clickable symbol links. Its code actions can insert a doc comment skeleton for a function, with sections for its parameters, result, and failures.

## This website

The standard library pages on this site are produced by the same generator from the comments in `std/`. The rest of the site is built by `go run ./cmd/lowlight-site` from the repository; see [Contributing](/about/contributing.html#the-website).
