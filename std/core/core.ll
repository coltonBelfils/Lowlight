// std/core — Standard core prelude automatically imported into every Lowlight module.
//
// Aggregates foundational primitives ([`std/core/primitives`]), language contract
// interfaces ([`std/core/interfaces`]), error handling ([`std/core/error`],
// [`std/core/result`]), collections ([`std/core/list`], [`std/core/array`],
// [`std/core/set`], [`std/core/map`]), intervals ([`std/core/range`]), and
// hashing ([`std/core/hash`]).

// =============================================================================
// Standard Core Inline Test Suite
// =============================================================================

@test("core primitive string conversions and hash")
bind test_core_primitives = (): {
	assert(0.to_string() == "0", "0 to_string")
	assert(42.to_string() == "42", "42 to_string")
	assert((-17).to_string() == "-17", "-17 to_string")
	assert(true.to_string() == "true", "true to_string")
	assert(false.to_string() == "false", "false to_string")
	assert('A'.to_string() == "A", "'A' to_string")
	assert(u8(255).to_string() == "255", "u8 to_string")
	assert(u16(65535).to_string() == "65535", "u16 to_string")
	assert(u32(4294967295).to_string() == "4294967295", "u32 to_string")
	assert(i8(-128).to_string() == "-128", "i8 to_string")
	assert(i16(-32768).to_string() == "-32768", "i16 to_string")
	assert(i32(-2147483648).to_string() == "-2147483648", "i32 to_string")
	assert("hello".hash() == "hello".hash(), "string hash deterministic")
	assert("hello".hash() != "world".hash(), "string hash diff")
	assert(42.hash() == 42.hash(), "int hash deterministic")
	assert(true.hash() != false.hash(), "bool hash diff")
	assert('x'.hash() == 'x'.hash(), "char hash deterministic")
}

@test("core comparable and math helpers")
bind test_core_comparable_and_math = (): {
	assert(10.cmp(20) == -1, "10 < 20")
	assert(20.cmp(10) == 1, "20 > 10")
	assert(10.cmp(10) == 0, "10 == 10")
	assert('a'.cmp('b') == -1, "'a' < 'b'")
	assert("apple".cmp("banana") == -1, "apple < banana")
	assert("zebra".cmp("apple") == 1, "zebra > apple")
	assert("same".cmp("same") == 0, "same == same")

	assert(min[int](10, 20) == 10, "min(10, 20)")
	assert(min[int](30, 25) == 25, "min(30, 25)")
	assert(max[int](10, 20) == 20, "max(10, 20)")
	assert(max[int](30, 25) == 30, "max(30, 25)")
	assert(abs(-42) == 42, "abs(-42)")
	assert(abs(42) == 42, "abs(42)")
	assert(abs(0) == 0, "abs(0)")
	assert(clamp[int](5, 10, 20) == 10, "clamp lower")
	assert(clamp[int](15, 10, 20) == 15, "clamp within")
	assert(clamp[int](25, 10, 20) == 20, "clamp upper")
}

@test("core Result[T] construction and unwrapping")
bind test_core_result = (): {
	let r_ok = Result[int].ok(100)
	assert(r_ok.is_ok(), "is_ok")
	assert(not r_ok.is_err(), "not is_err")
	assert(r_ok.copy().unwrap_or(0) == 100, "unwrap_or on ok")
	assert(r_ok.copy().into_ok() == 100, "into_ok on ok")
	assert(r_ok.peek_err() == none, "peek_err on ok")
	let matched_int = match(r_ok.copy()):
		when Result.Ok(v): v
		default Result.Err(_): -1
	assert(matched_int == 100, "match Result.Ok")
	assert(r_ok.unwrap() == 100, "unwrap on ok")

	let r_str = Result[string].Ok("hello_" + "world")
	let matched_str = match(own r_str):
		when Result.Ok(own s): s
		default Result.Err(_): ""
	assert(matched_str == "hello_world", "match Result.Ok on Resource string")

	let r_err = Result[int].fail_msg("failure")
	assert(r_err.is_err(), "is_err")
	assert(not r_err.is_ok(), "not is_ok")
	assert(r_err.copy().peek_err() != none, "peek_err on err")
	let r_wrapped = r_err.copy().wrap_err("outer context").trace_err()
	assert(r_wrapped.copy().unwrap_err().summary() == "outer context: failure", "wrap_err summary")
	assert(r_wrapped.copy().unwrap_err().frames().len() == 2, "wrap_err + trace_err frames")
	assert(r_err.unwrap_or(999) == 999, "unwrap_or on err")
}

@test("core Error creation, wrapping, sentinels, and trace")
bind test_core_error = (): {
	let sentinel_nf = Error.coded("not_found", "resource not found")
	let e0 = Error.coded("not_found", "missing config.toml")
	let e1 = e0.wrap("loading config")
	let e2 = e1.trace()
	let e3 = e2.wrap("booting server")

	assert(e3.message() == "booting server", "outermost message")
	assert(e3.summary() == "booting server: loading config: missing config.toml", "summary chain")
	assert(e3.root().message() == "missing config.toml", "root message")
	assert(e3.has_code("not_found"), "has_code walks chain")
	assert(e3.code() == "not_found", "code walks chain")
	assert(e3.is(sentinel_nf), "is matches sentinel code across wrap layers")
	assert(not e3.is(Error.coded("timeout", "timed out")), "is rejects different code")
	assert(e3.is(Error.new("loading config")), "is matches intermediate layer message without code")

	if(e3.cause() as c1): {
		assert(c1.message() == "loading config", "cause returns direct wrapped error")
		assert(c1.frames().len() == 2, "traced layer has 2 frames")
	} else: panic("expected e3.cause() to be present")

	let formatted = e3.to_string()
	assert(formatted.contains("[not_found]"), "to_string includes code")
	assert(
		formatted.contains("booting server: loading config: missing config.toml"),
		"to_string includes summary",
	)
	assert(formatted.contains("trace:"), "to_string includes trace section")
	assert(formatted.contains("core.ll:"), "to_string includes caller file location")

	let e3_copy = e3.copy()
	assert(e3 == e3_copy, "Error equality matches identical copies")
	assert(e3 != e1, "Error equality distinguishes different errors")
}

@test("core List[T] operations")
bind test_core_list = (): {
	let list = List[int].new()
	assert(list.len() == 0, "initial len 0")
	assert(list.is_empty(), "initial is_empty")

	list.push(10)
	list.push(20)
	list.push(30)
	assert(list.len() == 3, "len after pushes")
	assert(not list.is_empty(), "not is_empty after push")
	assert(list.get(0) == 10, "get(0)")
	assert(list.get(1) == 20, "get(1)")
	assert(list.get(2) == 30, "get(2)")
	assert(list.get(3) == none, "get(3) out of bounds")

	let sum = 0
	let it = list.iter()
	for(true): {
		let next? = it.next()
		if(next? as val): sum += val else: for <-
	}
	assert(sum == 60, "list iteration sum")

	let copy_list = list.copy()
	assert(copy_list.len() == 3, "copy len")
	assert(copy_list.get(1) == 20, "copy get")

	let p? = list.pop()
	assert(p? == 30, "pop last")
	assert(list.len() == 2, "len after pop")

	list.clear()
	assert(list.len() == 0, "len after clear")
	assert(list.is_empty(), "is_empty after clear")

	let neg_cap = List[int].with_capacity(-5)
	assert(neg_cap.len() == 0, "negative with_capacity len is 0")
	assert(neg_cap.cap() == 0, "negative with_capacity clamps cap to 0")
	assert(neg_cap.is_empty(), "negative with_capacity is empty")
	neg_cap.push(42)
	assert(neg_cap.len() == 1 and neg_cap.cap() >= 1, "push after negative with_capacity")
	assert(neg_cap.get(0) == 42, "get after negative with_capacity push")

	let zero_cap = List[int].with_capacity(0)
	assert(zero_cap.len() == 0 and zero_cap.cap() == 0, "zero with_capacity")
	zero_cap.push(7)
	assert(zero_cap.get(0) == 7, "push after zero with_capacity")

	let pos_cap = List[int].with_capacity(4)
	assert(pos_cap.len() == 0 and pos_cap.cap() == 4, "positive with_capacity preserves cap")
}

@test("core Set[T] operations")
bind test_core_set = (): {
	let s = Set[string].new()
	assert(s.len() == 0, "set initial len")
	assert(s.is_empty(), "set initial is_empty")

	assert(s.insert("first"), "insert first")
	assert(s.insert("second"), "insert second")
	assert(not s.insert("first"), "insert duplicate returns false")
	assert(s.len() == 2, "set len after insertions")

	assert(s.contains("first"), "contains first")
	assert(s.contains("second"), "contains second")
	assert(not s.contains("third"), "does not contain third")

	let count = 0
	let sit = s.iter()
	for(true): {
		let next? = sit.next()
		if(next? as item): {
			assert(item == "first" or item == "second", "valid item in iter")
			count += 1
		} else: for <-
	}
	assert(count == 2, "iterated count")

	let s_copy = s.copy()
	assert(s_copy.len() == 2, "s_copy len")
	assert(s_copy.contains("first"), "s_copy contains")

	assert(s.remove("first"), "remove first")
	assert(not s.contains("first"), "contains after remove")
	assert(s.len() == 1, "len after remove")
	assert(not s.remove("first"), "remove again returns false")

	s.clear()
	assert(s.len() == 0, "len after clear")
	assert(s.is_empty(), "is_empty after clear")
}

@test("core Map[K, V] operations")
bind test_core_map = (): {
	let m = Map[string, int].new()
	assert(m.len() == 0, "map initial len")
	assert(m.is_empty(), "map initial is_empty")

	m.insert("k1", 100)
	m.insert("k2", 200)
	assert(m.len() == 2, "map len after inserts")
	assert(m.contains("k1"), "map contains k1")
	assert(m.contains("k2"), "map contains k2")
	assert(not m.contains("k3"), "map does not contain k3")

	assert(m.get("k1") == 100, "get k1")
	assert(m.get("k2") == 200, "get k2")
	assert(m.get("k3") == none, "get k3 is none")

	// Overwrite existing key
	m.insert("k1", 150)
	assert(m.len() == 2, "len after overwrite")
	assert(m.get("k1") == 150, "updated value")

	let keys = m.keys()
	assert(keys.len() == 2, "keys len")
	let vals = m.values()
	assert(vals.len() == 2, "vals len")

	let sum = 0
	let mit = m.iter()
	for(true): {
		let next? = mit.next()
		if(next? as entry): sum += entry.value else: for <-
	}
	assert(sum == 350, "map iteration sum")

	let m_copy = m.copy()
	assert(m_copy.len() == 2, "copy len")
	assert(m_copy.get("k1") == 150, "copy get")

	let rem? = m.remove("k1")
	assert(rem? == 150, "remove k1 returns removed value")
	assert(not m.contains("k1"), "does not contain k1")
	assert(m.len() == 1, "len after remove")
	let rem2? = m.remove("k1")
	assert(rem2? == none, "remove non-existent returns none")

	m.clear()
	assert(m.len() == 0, "len after clear")
	assert(m.is_empty(), "is_empty after clear")
}
