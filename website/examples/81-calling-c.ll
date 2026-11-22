// Title: Calling C
// Category: Systems
// Order: 81

// `extern` declares a C function; calling it is `unsafe`.
extern "string.h"
extern strlen: fn(s: ptr[u8]) u64

extern "math.h"
extern hypot: fn(x: f64, y: f64) f64

// Wrap foreign calls in safe functions.
bind c_len = fn(s: string) int: int(unsafe strlen(s.c_str()))

bind main = fn(): {
	println(c_len("lowlight"))
	println(unsafe hypot(3.0, 4.0))
}
