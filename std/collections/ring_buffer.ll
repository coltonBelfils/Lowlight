// RingBuffer[T]: a fixed-capacity circular buffer.
//
// A buffer of fixed size whose end wraps around to its start. Once full, pushing a new element
// overwrites the oldest one (or is refused). Unlike `Deque`, it never grows, so memory use is
// bounded: good for the last N log lines, audio and network streams, and producer/consumer queues.
//
// I'm implementing this one myself. Please leave this file as is.
//
// Further reading:
// - https://en.wikipedia.org/wiki/Circular_buffer
