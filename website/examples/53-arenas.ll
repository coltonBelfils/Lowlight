// Title: Arenas
// Category: Ownership
// Order: 53

// An Arena owns many values and frees them together. `alloc`
// returns a `ref[T]`, a plain reference that may form cycles.
define Node struct [
	name: string,
	next: [ref[Node]],
]

bind main = (): {
	let arena = Arena.new()
	let a = arena.alloc(Node[name: "a", next: []])
	let b = arena.alloc(Node[name: "b", next: []])
	let c = arena.alloc(Node[name: "c", next: []])
	a.next.push(b)
	b.next.push(c)
	c.next.push(a)

	// Walk the cycle a few steps.
	let cur = a
	for(i in 0..5): {
		print("${cur.name} ")
		cur = cur.next[0]
	}
	println()
	// The compiler guarantees no `ref` outlives its arena.
}
