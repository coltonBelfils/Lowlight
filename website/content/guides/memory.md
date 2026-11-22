---
title: "Memory: arenas and pools"
nav: Memory, arenas, and pools
summary: Building graphs, trees, and other shared structures without a garbage collector.
order: 14
group: Core ideas
---

# Memory: arenas and pools

Ownership gives every value exactly one owner. That fits most data: a list owns its elements, a struct owns its fields. Some structures don't fit that shape: a graph where nodes point at each other, a tree whose children point to their parents, a cache shared by many parts of a program. For these, Lowlight has two tools.

| | `Arena` and `ref[T]` | `Pool[T]` and `Handle[T]` |
|---|---|---|
| Lifetime | everything is freed together | each value is freed individually |
| Access | direct, like a pointer | a lookup through a handle |
| Stale access | impossible: checked at compile time | detected at run time (`get` returns `none`) |
| Best for | data built in a phase and discarded together: parse trees, graphs, per-request data | long-lived collections of objects that come and go: entities, connections, nodes in an editor |

## Arenas

An `Arena` owns many values. `arena.alloc(v)` moves `v` into the arena and returns a `ref[T]`, a plain, copyable reference used like the value itself:

```lowlight
define Node struct [
	name: string,
	edges: [ref[Node]],
]

bind link = (from: ref[Node], to: ref[Node]): from.edges.push(to)

bind main = (): {
	let arena = Arena.new()
	let a = arena.alloc(Node[name: "a", edges: []])
	let b = arena.alloc(Node[name: "b", edges: []])
	link(a, b)
	link(b, a)              // a cycle: fine in an arena
	for(n in a.edges): println("${a.name} -> ${n.name}")
	for(n in b.edges): println("${b.name} -> ${n.name}")
}
```

```output
a -> b
b -> a
```

When the arena is destroyed (or `reset()`), every value in it is destroyed, in reverse order of allocation. Allocation is a pointer bump, so arenas are also the fastest way to create many small values.

### How references stay valid

A `ref[T]` must never outlive its arena. The compiler guarantees it with three rules:

1. Allocating from an arena **pins** it for the rest of that scope: it can't be reset, reassigned, or moved while references from that scope might exist.
2. A `ref[T]` can't leave the scope of its arena: it can't be returned or stored in a longer-lived value.
3. A function that receives the arena as `&Arena` may return references allocated from it, because the caller owns the arena:

```lowlight fragment
bind Node.make = (arena: &Arena, name: string) ref[Node]:
	arena.alloc(Node[name: name.copy(), edges: []])
```

Use nested blocks to delimit phases. After the block that allocated, the arena is unpinned and can be reset for the next phase.

## Pools

`std/pool` provides `Pool[T]`, a slot array whose values are addressed by **generational handles**. A handle records both a slot and that slot's generation, so a handle to a freed value never accidentally reaches a new value that reused the slot:

```lowlight
import "std/pool"

define Player struct [
	name: string,
	score: int,
]

bind main = (): {
	let players = pool.Pool[Player].new()
	let ada = players.alloc(Player[name: "Ada", score: 0])
	let bob = players.alloc(Player[name: "Bob", score: 0])
	players.with_mut(ada, (p: &Player): { p.score += 10 })
	println(players.get(ada)?.score ?? -1)
	players.free(bob)
	println(players.has(bob))
	let cy = players.alloc(Player[name: "Cy", score: 3])   // may reuse Bob's slot
	println(players.get(bob)?.name ?? "stale handle")
	println(players.len())
}
```

```output
10
false
stale handle
2
```

Handles are plain values: store them in other structs, put them in maps, copy them freely. That makes pools a good fit for structures with back-references, such as a tree whose nodes store their parent's handle.

## Choosing

- **Most data needs neither.** Lists, maps, and structs that own their parts cover the majority of programs.
- **Building something in one go and throwing it away together?** Use an arena.
- **Objects created and destroyed independently, referenced from many places?** Use a pool.
- **Truly manual control?** `unsafe` and raw pointers are available; see [Unsafe code and C](unsafe-ffi.html).
