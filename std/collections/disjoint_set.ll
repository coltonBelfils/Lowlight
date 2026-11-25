// DisjointSet: union-find over the integers 0..n.
//
// Tracks a partition of elements into groups, with two near-constant-time operations: merge the
// groups of two elements (`union`), and find which group an element is in (`find`). Useful for
// connected components, Kruskal's minimum spanning tree, and grouping equivalent things.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Disjoint-set_data_structure
// - https://cp-algorithms.com/data_structures/disjoint_set_union.html
