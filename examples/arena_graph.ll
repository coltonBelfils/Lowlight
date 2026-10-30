// examples/arena_graph.ll
// Showcases Lowlight's lexical region bump allocator (`Arena` and `ref[T]`):
// - True pointer-speed cyclic graphs without `unsafe` or GC
// - Compile-time region escape protection
// - Automatic cleanup of Resource fields (e.g. `string`, `[ref[GraphNode]]`) inside arena objects
// - O(1) bulk reuse via `arena.reset()` across phases

define GraphNode struct [
    id: int,
    label: string,
    edges: [ref[GraphNode]],
]

bind GraphNode.alloc = (arena: &Arena, id: int, label: string): ref[GraphNode] {
    arena.alloc(GraphNode[
        id: id,
        label: label.copy(),
        edges: [],
    ])
}

bind connect = (from: ref[GraphNode], to: ref[GraphNode]) {
    from.edges.push(to)
}

// Breadth-first reachability over a cyclic `ref[GraphNode]` graph!
bind bfs_order = (start: ref[GraphNode]): [string] {
    let visited = #[start.id]
    let queue: [ref[GraphNode]] = [start]
    let order: [string] = []
    let head = 0

    for (head < queue.len()) {
        let cur = queue[head]
        head += 1
        order.push(cur.label.copy())

        for (next_node in cur.edges) {
            if (not visited.contains(next_node.id)) {
                visited.insert(next_node.id)
                queue.push(next_node)
            }
        }
    }
    order
}

bind main = () {
    let arena = Arena.with_capacity(2048)

    // Phase 1: Build a directed cyclic graph A <-> B -> C -> A
    let a = GraphNode.alloc(&arena, 1, "Alpha")
    let b = GraphNode.alloc(&arena, 2, "Beta")
    let c = GraphNode.alloc(&arena, 3, "Gamma")
    let d = GraphNode.alloc(&arena, 4, "Delta")

    connect(a, b)
    connect(b, a) // Direct cycle!
    connect(b, c)
    connect(c, a) // Back-edge cycle!
    connect(c, d)

    let visited_names = bfs_order(a)
    let path_str = visited_names.join(" -> ")
    assert(path_str == "Alpha -> Beta -> Gamma -> Delta")
    let phase1_bytes = arena.bytes_used()
    assert(phase1_bytes > 0)

    // Phase 2: Reset arena in O(1) — runs destructors on `label` strings & `edges` vectors,
    // then reuses the underlying arena slab for a brand new graph!
    arena.reset()
    assert(arena.bytes_used() == 0)

    let n1 = GraphNode.alloc(&arena, 10, "Start")
    let n2 = GraphNode.alloc(&arena, 20, "Finish")
    connect(n1, n2)
    connect(n2, n1)

    let phase2_str = bfs_order(n1).join(" -> ")
    assert(phase2_str == "Start -> Finish")

    println("arena_graph: phase1='$path_str' ($phase1_bytes bytes), phase2='$phase2_str'")
}
