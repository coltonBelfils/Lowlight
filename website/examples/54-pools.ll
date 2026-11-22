// Title: Pools and handles
// Category: Ownership
// Order: 54

import "std/pool"

define Task struct [
	title: string,
	done: bool,
]

// A Pool stores values addressed by generational handles. A handle
// to a freed value is detected instead of reaching a new value.
bind main = (): {
	let tasks = pool.Pool[Task].new()
	let write = tasks.alloc(Task[title: "write", done: false])
	let test = tasks.alloc(Task[title: "test", done: false])

	tasks.with_mut(write, (t: &Task): { t.done = true })
	println(tasks.get(write)?.done ?? false)

	tasks.free(test)
	println(tasks.get(test)?.title ?? "freed")
	println(tasks.len())
}
