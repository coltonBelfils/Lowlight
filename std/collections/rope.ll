// Rope: a string stored as a tree of pieces.
//
// A large string held as a binary tree of smaller strings, so inserting or deleting text in the
// middle, concatenating, and splitting take O(log n) instead of copying the whole string. Useful
// for text editors and anything that edits very large strings.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Rope_(data_structure)
