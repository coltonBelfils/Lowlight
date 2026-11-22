---
title: Your first project
nav: First project
summary: Create a Lowlight project with packages, tests, and dependencies.
order: 3
---

# Your first project

A single `.ll` file runs with no setup. Once a program grows past one file, make it a **project**: a directory with a `lowlight.toml` manifest at its root.

## Create a project

```bash
lowlight new wordy
cd wordy
lowlight run
```

`lowlight new` creates:

```text
wordy/
├── lowlight.toml     # the manifest
├── main.ll           # the root package; it has `main`, so it is a program
└── wordy_test.ll     # tests for the root package
```

The manifest names the project and lists its dependencies:

```toml
[package]
name = "wordy"
version = "0.1.0"
edition = "2026"

[dependencies]
```

Use `lowlight new name --lib` for a library (a package with no `main`).

## Packages are directories

Every directory of `.ll` files is a **package**. All files in a directory share one namespace, so they can use each other's declarations without imports. A subdirectory is a separate package that you import by its path, starting with the project name:

```text
wordy/
├── lowlight.toml
├── main.ll
└── text/
    ├── count.ll
    └── count_test.ll
```

```lowlight fragment
// text/count.ll

// Counts the words in `s`, splitting on whitespace.
bind word_count = (s: string) int: {
	let n = 0
	let in_word = false
	for(ch in s.chars()): {
		if(ch.is_whitespace()): in_word = false
		else if(not in_word): {
			in_word = true
			n += 1
		}
	}
	n
}

// A helper only this package can use: the leading `_` makes it private.
bind _is_letter = (c: char) bool: c.is_alpha()
```

```lowlight fragment
// main.ll
import "wordy/text"

bind main = (): println(text.word_count("the quick brown fox"))
```

An imported package is used through the last segment of its path: `text.word_count`. Write `import "wordy/text" as t` to choose another name.

Names are **public by default**. A name that begins with `_` is private to its package, including struct fields and enum variants. There is no `pub` keyword and no header file.

## Tests

Test files end in `_test.ll` and live beside the code. They belong to the same package, so they can test private helpers too:

```lowlight fragment
// text/count_test.ll
import "std/test"

@test("counts words separated by any whitespace")
bind test_word_count = (): {
	test.assert_eq(word_count("a  b\tc\n"), 3)
	test.assert_eq(word_count(""), 0)
}
```

```bash
lowlight test               # every package in the project
lowlight test ./text        # one package
lowlight test -run count    # tests whose name matches
```

Test files are never part of a normal build.

## Dependencies

Add a dependency with `lowlight add`. A dependency can be a local directory, an archive URL with its SHA-256 hash, or a version from a package index:

```bash
lowlight add shared --path ../shared
lowlight add jsonx --url https://example.org/jsonx-1.2.0.tar.gz
lowlight add http_ext --index https://acme.dev/pkgs/http_ext.toml --version "^0.3"
```

`lowlight add` records the dependency in `lowlight.toml` and pins exact versions and hashes in `lowlight.lock`. Commit both files. Builds never touch the network: `lowlight fetch` downloads what the lockfile lists, and `lowlight update` moves to newer allowed versions.

Read [Packages and dependencies](/guides/packages.html) for workspaces, vendoring, overrides, and platform-specific settings, and [the manifest reference](/spec/packages-manifests-and-dependencies.html) for every field.

## Next

[Set up your editor →](editors.html)
