// LinkedList[T]: a doubly linked list.
//
// Each element lives in its own node that points to the next and previous nodes, so inserting or
// removing at a known position takes O(1) time without shifting anything. Useful for queues and
// LRU caches that splice elements around, and as a classic exercise in ownership: in Lowlight the
// nodes have to be owned somewhere (a list of nodes addressed by index, a `Pool`, or an `Arena`).
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Doubly_linked_list
// - https://opendatastructures.org/ods-python/3_2_DLList_Doubly_Linked_Li.html
