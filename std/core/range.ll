// std/core/range — Half-open (`start..end`) and closed (`start..=end`) interval types.
//
// Backs range literals (`0..10`, `'a'..='z'`), loop iteration (`for(i in 0..10)`),
// and containment tests (`when in 0..=255`).

import "./interfaces.ll"

// Interval from `start` to `end`, either half-open (`..`) or closed (`..=`).
//
// Implements [`Containable`], [`Iterable`], and [`Printable`].
//
// # Example
// ```lowlight
// let total = 0
// for(i in 1..=5): total += i
// assert(total == 15)
// ```
@lang("range")
define Range[T] struct [
	// Lower bound of the interval (inclusive).
	start: T,
	// Upper bound of the interval (exclusive unless `inclusive` is `true`).
	end: T,
	// Whether `end` itself is included in the interval (`..=`).
	inclusive: bool,
]

// Reports whether `item` falls inside the range bounds.
bind Range[T].contains = (self, item: T) bool: {
	if(self.inclusive): func <- item >= self.start and item <= self.end
	item >= self.start and item < self.end
}

// Reports whether the range contains zero elements (`start >= end` for `..`, or `start > end` for `..=`).
bind Range[T].is_empty = (self) bool: {
	if(self.inclusive): func <- self.start > self.end
	self.start >= self.end
}

// Returns the number of discrete steps contained in the range, or `0` if empty.
bind Range[T].len = (self) int: {
	if(self.inclusive): {
		if(self.start > self.end): func <- 0
		func <- int(u64(self.end) - u64(self.start) + u64(1))
	}
	if(self.start >= self.end): func <- 0
	int(u64(self.end) - u64(self.start))
}

// Formats the range using Lowlight literal syntax (`"start..end"` or `"start..=end"`).
bind Range[T].to_string = (self) string: {
	if(self.inclusive): func <- "${self.start}..=${self.end}"
	"${self.start}..${self.end}"
}

// Forward [`Iterator`] stepping sequentially through values in a [`Range`].
define RangeIterator[T] struct [
	// Next candidate value to yield.
	cur: T,
	// Terminal bound copied from the source range.
	end: T,
	// Whether `end` is inclusive (`..=`).
	inclusive: bool,
	// Whether the iterator has finished yielding elements.
	done: bool,
]

// Advances the iterator and returns the next value in the range, or `none` when finished.
bind RangeIterator[T].next = (&self) ?T: {
	if(self.done): func <- none
	if(not self.inclusive and self.cur >= self.end): {
		self.done = true
		func <- none
	}
	if(self.inclusive and self.cur > self.end): {
		self.done = true
		func <- none
	}
	let val = self.cur
	if(self.cur == self.end): self.done = true else: self.cur = T(u64(self.cur) + u64(1))
	val
}

// Returns a forward [`RangeIterator`] yielding values from `start` toward `end`.
bind Range[T].iter = (self) Iterator[T]: {
	let done = if(self.inclusive): self.start > self.end else: self.start >= self.end
	RangeIterator[T][
		cur: self.start,
		end: self.end,
		inclusive: self.inclusive,
		done: done,
	]
}
