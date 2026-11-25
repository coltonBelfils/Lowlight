// Title: Files
// Category: Standard library
// Order: 70

import "std/fs"

bind main = fn(): {
	let path = "/tmp/lowlight_example.txt"

	// File operations return results: the outside world can fail. Writing has no value,
	// so it returns a `Status`, and its chain is a statement.
	fs.write_file(path, "line one\nline two\n"):
		default err(own e): {
			println(e.summary())
			func <-
		}
	fs.append_file(path, "line three\n"): default panic

	let text = fs.read_text(path):
		when not_found(_): {
			println("the file vanished")
			func <-
		}
		default err(own e): {
			println(e.summary())
			func <-
		}
	for(i, line in text.trim().split("\n")): println("${i + 1}: $line")

	println(fs.exists(path))

	// Removing tells a missing file apart from other failures.
	fs.remove_file(path):
		when not_found(_): println("already gone")
		default panic
	println(fs.exists(path))

	// A missing file is its own failure, so it's easy to handle on its own.
	let config = fs.read_text("/no/such/file"):
		when not_found(_): "defaults"
		default panic
	println(config)
}
