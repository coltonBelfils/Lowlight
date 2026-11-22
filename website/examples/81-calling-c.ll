// Title: Calling C
// Category: Systems
// Order: 81

// `extern` declares a C function; calling it is `unsafe`.
extern "string.h"
extern strlen: (s: ptr[u8]) u64

extern "math.h"
extern hypot: (x: f64, y: f64) f64

// Wrap foreign calls in safe functions.
bind c_len = (s: string) int: int(unsafe strlen(s.c_str()))

bind main = (): {
	println(c_len("lowlight"))
	println(unsafe hypot(3.0, 4.0))
}
