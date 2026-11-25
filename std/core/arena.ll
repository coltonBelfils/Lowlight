// std/core/arena — [`Arena`], the region allocator behind `ref[T]`.
//
// The checker's rules that keep a `ref[T]` from outliving its arena stay in the compiler; this
// file is only the allocator.

// One block of an arena's memory.
define _ArenaChunk struct [
	data: ptr[u8],
	cap: int,
]

// A value in an arena that has a destructor: where it is, and the function that drops it.
define _ArenaDrop struct [
	at: ptr[u8],
	run: fn(ptr[u8]),
]

// A region allocator: [`Arena.alloc`] moves a value into the arena and returns a `ref[T]` to it,
// and the arena frees everything at once when it is dropped or [`Arena.reset`]. The checker
// guarantees no `ref[T]` outlives its arena.
//
// Values with a destructor are dropped newest first. Memory comes in chunks, each twice the size
// of the last; the first isn't allocated until the first `alloc`.
//
// # Example
// ```lowlight
// let arena = Arena.new()
// let node = arena.alloc(Node[value: 1, next: none])
// println(node.value)
// ```
@lang("arena")
define Arena struct [
	// Chunks, oldest first. Allocations come from the last one.
	_chunks: [_ArenaChunk],
	// Addresses in the last chunk: its start, the next free byte, and its end (all `0` before the
	// first chunk).
	_start: int,
	_next: int,
	_end: int,
	// Bytes handed out from the chunks before the last one.
	_done: int,
	// Values to drop, in allocation order.
	_drops: [_ArenaDrop],
	// The size of the first chunk.
	_first_cap: int,
]

// Constructs an empty [`Arena`]; its first chunk will hold 4096 bytes.
bind Arena.new = fn() Arena: Arena.with_capacity(4096)

// Constructs an empty [`Arena`] whose first chunk will hold `bytes` bytes (at least 256).
bind Arena.with_capacity = fn(bytes: int) Arena: Arena[
	_chunks: [],
	_start: 0,
	_next: 0,
	_end: 0,
	_done: 0,
	_drops: [],
	_first_cap: max(bytes, 256),
]

// Returns `size` bytes aligned to `align` (a power of two), starting a new chunk if they don't
// fit in the current one.
@inline
bind Arena._bump = fn(&self, size: int, align: int) ptr[u8]: {
	let at = self._next + align - 1 & ~(align - 1)
	if(at + size > self._end): {
		self._new_chunk(size + align)
		at = self._next + align - 1 & ~(align - 1)
	}
	self._next = at + size
	unsafe ptr_cast[u8](at)
}

// Starts a chunk with room for at least `need` bytes: twice the last chunk's size, or the first
// chunk's size.
@noinline
bind Arena._new_chunk = fn(&self, need: int): {
	let n = self._chunks.len()
	let cap = max(if(n > 0): self._chunks[n - 1].cap * 2 else: self._first_cap, need * 2)
	let data = unsafe alloc_raw[u8](cap)
	self._chunks.push(_ArenaChunk[data: data, cap: cap])
	self._done += self._next - self._start
	self._start = int(unsafe ptr_addr(data))
	self._next = self._start
	self._end = self._start + cap
}

// Moves `value` into the arena and returns a reference to it, valid as long as the arena (and
// until [`Arena.reset`]).
bind Arena.alloc[T] = fn(&self, value: own T) ref[T]: {
	let at = self._bump(max(type.size_of[T](), 1), max(type.align_of[T](), 1))
	unsafe ptr_write(ptr_cast[T](at), own value)
	if(not type.is_plain[T]()):
		self._drops.push(_ArenaDrop[
			at: at,
			run: fn(p: ptr[u8]): {
				let _ = unsafe ptr_read(ptr_cast[T](p))
			},
		])
	unsafe ref_from_ptr(ptr_cast[T](at))
}

// Drops the values that have destructors, newest first.
bind Arena._run_drops = fn(&self): {
	let i = self._drops.len() - 1
	while(i >= 0): {
		let d = self._drops[i]
		let run = d.run
		run(d.at)
		i -= 1
	}
	self._drops.clear()
}

// Drops everything in the arena and makes its memory available again, keeping the largest chunk
// for reuse.
bind Arena.reset = fn(&self): {
	self._run_drops()
	let n = self._chunks.len()
	if(n == 0): func <-
	for(i in 0..n - 1): unsafe free_raw(self._chunks[i].data)
	let last = self._chunks[n - 1]
	self._chunks.clear()
	self._chunks.push(last)
	self._start = int(unsafe ptr_addr(last.data))
	self._next = self._start
	self._end = self._start + last.cap
	self._done = 0
}

// Returns the number of bytes handed out since the arena was created or last reset, including
// alignment padding.
bind Arena.bytes_used = fn(self) int: self._done + self._next - self._start

// Drops everything in the arena and frees its memory.
bind Arena.drop = fn(&self): {
	self._run_drops()
	for(ch in self._chunks): unsafe free_raw(ch.data)
	self._chunks.clear()
}

@test("Arena allocates, drops values newest first, and resets")
bind test_arena = fn(): {
	let a = Arena.new()
	assert(a.bytes_used() == 0, "a new arena has used nothing")
	{
		let x = a.alloc(_ArenaTestBox[n: 41])
		let s = a.alloc("hello" + " arena")
		assert(x.n + 1 == 42 and s.len() == 11, "refs read their values")
		assert(a.bytes_used() >= 24, "bytes_used counts allocations")
	}
	a.reset()
	assert(a.bytes_used() == 0, "reset frees everything")
	{
		// Many allocations span several chunks.
		let total = 0
		for(i in 0..5000): {
			let r = a.alloc(_ArenaTestBox[n: i])
			total += r.n
		}
		assert(total == 12497500, "values survive chunk growth")
	}
	let small = Arena.with_capacity(16)
	{
		let words: [string] = []
		for(i in 0..100): {
			let w = small.alloc("w$i")
			words.push(w.copy())
		}
		assert(words.len() == 100 and words[99] == "w99", "resource values in many chunks")
	}
}

@test_only
define _ArenaTestBox struct [
	n: int
]
