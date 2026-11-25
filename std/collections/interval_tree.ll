// IntervalTree[T]: a set of intervals searchable by overlap.
//
// Stores intervals (`lo..hi`) so that "which intervals contain this point?" or "which overlap this
// range?" takes O(log n + matches). Useful for calendars and scheduling, genome ranges, text
// editor decorations, and collision broad phases.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Interval_tree
