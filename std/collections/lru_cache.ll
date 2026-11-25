// LruCache[K, V]: a bounded cache that evicts the least recently used entry.
//
// A map with a fixed capacity: when it is full, inserting evicts the entry that was used longest ago.
// Usually a hash map plus a linked list ordered by use, so both lookup and eviction are O(1).
// Useful for memoizing expensive results, caching files or responses, and bounding memory.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Cache_replacement_policies
