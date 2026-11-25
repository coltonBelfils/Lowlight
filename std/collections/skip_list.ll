// SkipList[K, V]: an ordered map built from linked lists.
//
// A sorted linked list with extra "express lane" lists above it that skip over elements, chosen at
// random. Search, insert, and remove take O(log n) on average, like a balanced tree, with simpler
// code. Useful as an ordered map and in concurrent settings; an alternative to `BTreeMap`.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Skip_list
// - https://opendatastructures.org/ods-python/4_Skiplists.html
