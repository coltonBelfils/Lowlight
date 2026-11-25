// BloomFilter[T]: a probabilistic set.
//
// Answers "is this element in the set?" with "definitely not" or "probably yes", using a bit array
// and several hash functions, in far less memory than storing the elements. Elements can't be
// removed or listed. Useful to skip expensive lookups (disk, network) for things that aren't there.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Bloom_filter
