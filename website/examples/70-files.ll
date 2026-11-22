// Title: Files
// Category: Standard library
// Order: 70

import "std/fs"

bind main = fn(): {
	let path = "/tmp/lowlight_example.txt"

	// File operations return results: the outside world can fail.
	let ok = fs.write_file(path, "line one\nline two\n"):
		default err(own e): {
			println(e.summary())
			func <-
		}
	fs.append_file(path, "line three\n") ?? false

	let text = fs.read_text(path):
		default err(own e): {
			println(e.summary())
			func <-
		}
	for(i, line in text.trim().split("\n")): println("${i + 1}: $line")

	println(fs.exists(path))
	fs.remove_file(path)
	println(fs.exists(path))

	// Missing files are ordinary failures.
	let missing! = fs.read_text("/no/such/file")
	println(missing!.is_err())
}
