---
title: "Tutorial: a word-frequency tool"
nav: A command-line tool
summary: Build a command-line program that reads a file, counts words with a map, sorts them, and prints a table.
order: 1
group: Tutorials
---

# Tutorial: a word-frequency tool

You'll write `wordfreq`, a command-line tool that prints the most common words in a text file:

```text
$ wordfreq notes.txt 3
the          4
lowlight     2
memory       2
```

It touches most everyday parts of Lowlight: strings, maps, lists, sorting, reading files, command-line arguments, exit codes, and tests.

## 1. Set up a project

```bash
lowlight new wordfreq
cd wordfreq
```

Replace `main.ll` with the pieces below as you go.

## 2. Split text into words

Words are runs of letters and digits; everything else separates them. Build each word one character at a time:

```lowlight
bind words = fn(text: string) [string]: {
	let out: [string] = []
	let cur = ""
	for(c in text.chars()): {
		if(c.is_alnum()): cur.push_char(c)
		else if(not cur.is_empty()): {
			out.push(cur.to_lower())
			cur.clear()
		}
	}
	if(not cur.is_empty()): out.push(cur.to_lower())
	out
}

bind main = fn(): println(words("The cat, the hat; THE end."))
```

```output
[the, cat, the, hat, the, end]
```

`text.chars()` decodes the UTF-8 string into characters. `cur` is a `let` string that grows with `push_char`; `to_lower()` returns a new string, which `push` moves into the list.

## 3. Count them

A map from word to count does the counting:

```lowlight fragment
bind count = fn(ws: [string]) #[string: int]: {
	let counts: #[string: int] = #[]
	for(w in ws): {
		let n = counts[w] ?? 0
		counts[w.copy()] = n + 1
	}
	counts
}
```

- `counts[w]` is an optional, since the word may not be there yet; `?? 0` supplies the default.
- The loop variable `w` *borrows* each element of `ws`. A map must own its keys, so inserting one stores a copy, `w.copy()`. The compiler would point this out if you forgot.

## 4. Sort by count

To sort, turn the map into a list of entries and sort that. A `for` loop with a result type builds the list:

```lowlight fragment
import "std/sort"

bind top = fn(counts: #[string: int], n: int) [MapEntry[string, int]]: {
	let entries = for(word, c in counts) [MapEntry[string, int]]: MapEntry[key: word.copy(), value: c]
	entries.sort_by(fn(a: MapEntry[string, int], b: MapEntry[string, int]) int:
		if(a.value != b.value): b.value - a.value
		else: a.key.cmp(b.key))
	let k = min(n, entries.len())
	for(i in 0..k) [MapEntry[string, int]]: entries[i].copy()
}
```

- `MapEntry[K, V]` is the prelude's key/value struct.
- `sort_by`, from `std/sort`, takes a comparison function returning a negative number, zero, or a positive number. Ties are broken alphabetically so the output is stable.
- The final loop collects the first `k` entries. Each one is copied because `entries` keeps ownership of its own.

Try the pieces together:

```lowlight
import "std/sort"
import "std/strfmt"

bind words = fn(text: string) [string]: {
	let out: [string] = []
	let cur = ""
	for(c in text.chars()): {
		if(c.is_alnum()): cur.push_char(c)
		else if(not cur.is_empty()): {
			out.push(cur.to_lower())
			cur.clear()
		}
	}
	if(not cur.is_empty()): out.push(cur.to_lower())
	out
}

bind count = fn(ws: [string]) #[string: int]: {
	let counts: #[string: int] = #[]
	for(w in ws): {
		let n = counts[w] ?? 0
		counts[w.copy()] = n + 1
	}
	counts
}

bind top = fn(counts: #[string: int], n: int) [MapEntry[string, int]]: {
	let entries = for(word, c in counts) [MapEntry[string, int]]: MapEntry[key: word.copy(), value: c]
	entries.sort_by(fn(a: MapEntry[string, int], b: MapEntry[string, int]) int:
		if(a.value != b.value): b.value - a.value
		else: a.key.cmp(b.key))
	let k = min(n, entries.len())
	for(i in 0..k) [MapEntry[string, int]]: entries[i].copy()
}

bind report = fn(text: string, n: int): {
	for(e in top(count(words(text)), n)): println("${e.key.pad_right(12)} ${e.value}")
}

bind main = fn(): report("Lowlight frees memory deterministically. The memory is freed when the owner's scope ends; the owner is the variable. Lowlight has no collector, so the program is predictable.", 4)
```

```output
the          5
is           3
lowlight     2
memory       2
```

`pad_right` comes from `std/strfmt`, which adds formatting methods to `string`.

## 5. Read the file and the arguments

The real program reads a file named on the command line. `fs.read_text` returns a `Result[string]`, because opening a file can fail for reasons outside the program's control. A result chain handles the failure where it happens:

```lowlight no-run
import "std/fs"
import "std/sort"
import "std/strfmt"

bind words = fn(text: string) [string]: {
	let out: [string] = []
	let cur = ""
	for(c in text.chars()): {
		if(c.is_alnum()): cur.push_char(c)
		else if(not cur.is_empty()): {
			out.push(cur.to_lower())
			cur.clear()
		}
	}
	if(not cur.is_empty()): out.push(cur.to_lower())
	out
}

bind count = fn(ws: [string]) #[string: int]: {
	let counts: #[string: int] = #[]
	for(w in ws): {
		let n = counts[w] ?? 0
		counts[w.copy()] = n + 1
	}
	counts
}

bind top = fn(counts: #[string: int], n: int) [MapEntry[string, int]]: {
	let entries = for(word, c in counts) [MapEntry[string, int]]: MapEntry[key: word.copy(), value: c]
	entries.sort_by(fn(a: MapEntry[string, int], b: MapEntry[string, int]) int:
		if(a.value != b.value): b.value - a.value
		else: a.key.cmp(b.key))
	let k = min(n, entries.len())
	for(i in 0..k) [MapEntry[string, int]]: entries[i].copy()
}

bind main = fn() int: {
	let argv = args()
	if(argv.len() < 2 or argv.len() > 3): {
		eprintln("usage: wordfreq <file> [count]")
		func <- 2
	}
	let n = if(argv.len() == 3): parse_int(argv[2]) ?? 10 else: 10
	let text = fs.read_text(argv[1]):
		default err(own e): {
			eprintln("wordfreq: ${e.wrap("reading ${argv[1]}").summary()}")
			func <- 1
		}
	for(e in top(count(words(text)), n)): println("${e.key.pad_right(12)} ${e.value}")
	0
}
```

- `main` returns an `int`, which becomes the exit status: `2` for a usage error, `1` when the file can't be read.
- `e.wrap(...)` adds context to the error; `summary()` renders the chain on one line, such as `reading notes.txt: no such file or directory`.
- `parse_int(argv[2]) ?? 10` falls back to 10 when the count isn't a number.

Run it:

```bash
lowlight run -- notes.txt 3
lowlight build -o wordfreq --release
```

## 6. Test it

Add `wordfreq_test.ll` beside `main.ll`:

```lowlight fragment
import "std/test"

@test("splits on punctuation and lowercases")
bind test_words = fn(): test.assert_eq(words("Hi, hi!"), ["hi", "hi"])

@test("orders by count, then alphabetically")
bind test_top = fn(): {
	let t = top(count(["b", "a", "b", "c", "a"]), 2)
	test.assert_eq(t.len(), 2)
	test.assert_eq(t[0].key, "a")
	test.assert_eq(t[1].key, "b")
}
```

```bash
lowlight test
```

## What you practiced

- Strings: iterating characters, building strings, case conversion, padding.
- Collections: a map for counting, `MapEntry`, collecting loops, sorting with a comparison function.
- Ownership in practice: loop variables borrow, so storing them takes `.copy()`.
- Failure handling at the boundary: a result chain turns a file error into a message and an exit code.

## Exercises

1. Accept several files and count words across all of them.
2. Add a `--min-length N` option that ignores short words.
3. Read from standard input when no file is given. (Look at `std/os` and `std/fs`.)
