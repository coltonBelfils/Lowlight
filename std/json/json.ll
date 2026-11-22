//This file is completely written by hand by me. It is very much a work in progress and no decision about the language or code should at all be made based on this file. Please leave it as is. Feel free to comment on it though.

/*
Json.to_string
to_json
Jsonable (maybe a better name is needed)
Json type
Json.eq()
 */

import "std/chars"
import "std/strconv"
import "std/strings"
import "std/test"

@derive(copyable)
define Json enum [
	empty,
	null,
	string: string,
	bool: bool,
	number: Number,
	array: [Json],
	object: #[string: Json],
]

bind List[Json].eq = (self, other: List[Json]) bool: {
	if(self.len() != other.len()): <- false

	while(i, v in self): {
		if(self[i] != other[i]): while <- false
	} else: true
}

// bind List[Json].eq = (self, other: List[Json]) bool:
// 	if(self.len() != other.len()): false
// 	else: (while(i, v in self): if(self[i] != other[i]): while <- false) ?? true

// bind Map[string, Json].eq = (self, other: Map[string, Json]) bool: {
//     if(self.len() != other.len()): <- false

//     while(i in self): {
//         if((not other.contains(i.key)) or i.value != other[i.key]): while <- false
//     } else: true
// }

bind Json.eq = (self, other: Json) bool:
	match(self, other):
		when empty, empty: true
		when null, null: true
		when string(s), string(os): s == os
		when bool(b), bool(ob): b == ob
		when number(n), number(on): n == on
		when array(a), array(oa): a == oa
		when object(o), object(oo): {
			if(o.len() != oo.len()): <- false

			while(i in o): {
				if(not oo.contains(i.key) or i.value != oo[i.key]): while <- false
			} else: true
		}
		default: false

@derive(Equatable)
define Number enum [
	int: int,
	float: float,
]

bind Number.to_string = (self) string:
	match(self):
		when Number.int(i): i.to_string()
		default Number.float(f): f.to_string()

define Jsonable interface [
	to_json: (self) Json
]

bind Json.to_string = (json: Json) string:
	match(json):
		when empty: ""
		when null: "null"
		when string(s): "\"${s.to_string()}\""
		when bool(b): b.to_string()
		when number(n): n.to_string()
		/*match(n):
		when Number.int(i): i.to_string()
		default Number.float(f): f.to_string()*/
		when array(a): {
			let out = "["

			for(i in a):
				out += {
					i.to_string() + ", "
				}

			if(out == "["): match <- "[]"

			assert(out.len() > 1)
			out = out[0..out.len() - 2]

			out += "]"

			out
		}
		default object(o): {
			let out = "{"

			for(i in o):
				out += {
					"\n\"" + i.key.to_string() + "\": " + i.value.to_string() + ","
				}

			if(out == "{"): <- "{}"

			assert(out.len() > 0)
			out = out[0..out.len() - 1]

			out = out.replace_all("\n", "\n\t")

			out += "\n}"

			out
		}

@test("test_json_to_string")
bind test_json_to_string = (): {
	//tests from ai:

	// primitives
	test.assert_eq(Json.empty.to_string(), "")
	test.assert_eq(Json.bool(false).to_string(), "false")
	test.assert_eq(Json.string("").to_string(), "\"\"")
	test.assert_eq(Json.string("hello world").to_string(), "\"hello world\"")
	test.assert_eq(Json.string("123").to_string(), "\"123\"")
	test.assert_eq(Json.string("true").to_string(), "\"true\"")
	test.assert_eq(Json.string("a: b, [c], {d}").to_string(), "\"a: b, [c], {d}\"")
	test.assert_eq(Json.string("'single'").to_string(), "\"'single'\"")
	test.assert_eq(Json.number(Number.int(0)).to_string(), "0")
	test.assert_eq(Json.number(Number.int(-1)).to_string(), "-1")
	test.assert_eq(Json.number(Number.int(-42)).to_string(), "-42")
	test.assert_eq(Json.number(Number.int(1000000)).to_string(), "1000000")
	test.assert_eq(Json.number(Number.float(0.0)).to_string(), "0")
	test.assert_eq(Json.number(Number.float(0.5)).to_string(), "0.5")
	test.assert_eq(Json.number(Number.float(-1.5)).to_string(), "-1.5")
	test.assert_eq(Json.number(Number.float(123.456)).to_string(), "123.456")

	// array tests
	test.assert_eq(Json.array([Json.string("")]).to_string(), "[\"\"]")
	test.assert_eq(Json.array([Json.bool(true)]).to_string(), "[true]")
	test.assert_eq(Json.array([Json.bool(false)]).to_string(), "[false]")
	test.assert_eq(Json.array([Json.number(Number.int(0))]).to_string(), "[0]")
	test.assert_eq(Json.array([Json.number(Number.float(-2.5))]).to_string(), "[-2.5]")
	test.assert_eq(Json.array([Json.array([])]).to_string(), "[[]]")
	test.assert_eq(Json.array([Json.object(#[])]).to_string(), "[{}]")
	test.assert_eq(
		Json.array([Json.bool(true), Json.bool(false), Json.null]).to_string(),
		"[true, false, null]",
	)
	test.assert_eq(
		Json.array([Json.array([]), Json.array([]), Json.array([])]).to_string(),
		"[[], [], []]",
	)
	test.assert_eq(
		Json.array([Json.array([Json.array([])])]).to_string(),
		"[[[]]]",
	)
	test.assert_eq(
		Json
			.array([
				Json.array([Json.number(Number.int(1)), Json.number(Number.int(2))]),
				Json.array([Json.number(Number.int(3)), Json.number(Number.int(4))]),
			])
			.to_string(),
		"[[1, 2], [3, 4]]",
	)
	test.assert_eq(
		Json
			.array([
				Json.object(#["a": Json.number(Number.int(1))]),
				Json.object(#["b": Json.number(Number.int(2))]),
			])
			.to_string(),
		"[{\n\t\"a\": 1\n}, {\n\t\"b\": 2\n}]",
	)

	// object tests
	test.assert_eq(
		Json.object(#["": Json.string("empty key")]).to_string(),
		"{\n\t\"\": \"empty key\"\n}",
	)
	test.assert_eq(
		Json.object(#["flag": Json.bool(false)]).to_string(),
		"{\n\t\"flag\": false\n}",
	)
	test.assert_eq(
		Json.object(#["num": Json.number(Number.int(-99))]).to_string(),
		"{\n\t\"num\": -99\n}",
	)
	test.assert_eq(
		Json.object(#["pi": Json.number(Number.float(3.14))]).to_string(),
		"{\n\t\"pi\": 3.14\n}",
	)
	test.assert_eq(
		Json.object(#["arr": Json.array([])]).to_string(),
		"{\n\t\"arr\": []\n}",
	)
	test.assert_eq(
		Json.object(#["obj": Json.object(#[])]).to_string(),
		"{\n\t\"obj\": {}\n}",
	)
	test.assert_eq(
		Json
			.object(#[
				"a": Json.array([]),
				"b": Json.object(#[]),
				"c": Json.string(""),
			])
			.to_string(),
		"{\n\t\"a\": [],\n\t\"b\": {},\n\t\"c\": \"\"\n}",
	)
	test.assert_eq(
		Json
			.object(#[
				"items": Json.array([
					Json.object(#["id": Json.number(Number.int(1))]),
					Json.object(#["id": Json.number(Number.int(2))]),
				])
			])
			.to_string(),
		"{\n\t\"items\": [{\n\t\t\"id\": 1\n\t}, {\n\t\t\"id\": 2\n\t}]\n}",
	)
	test.assert_eq(
		Json
			.object(#[
				"a": Json.object(#[
					"b": Json.object(#[
						"c": Json.object(#[
							"d": Json.number(Number.int(42))
						])
					])
				])
			])
			.to_string(),
		"{\n\t\"a\": {\n\t\t\"b\": {\n\t\t\t\"c\": {\n\t\t\t\t\"d\": 42\n\t\t\t}\n\t\t}\n\t}\n}",
	)

	//my tests

	//Json.empty
	test.assert_eq(Json.null.to_string(), "null")
	test.assert_eq(Json.string("abc").to_string(), "\"abc\"")
	test.assert_eq(Json.bool(true).to_string(), "true")
	test.assert_eq(Json.bool(true).to_string(), "true")
	test.assert_eq(Json.number(Number.int(1)).to_string(), "1")
	test.assert_eq(Json.number(Number.float(1.0)).to_string(), "1")
	test.assert_eq(Json.number(Number.float(1.5)).to_string(), "1.5")
	test.assert_eq(Json.array([]).to_string(), "[]")
	test.assert_eq(Json.array([Json.null]).to_string(), "[null]")
	test.assert_eq(Json.array([Json.null, Json.null]).to_string(), "[null, null]")
	test.assert_eq(Json.array([Json.null, Json.string("abc")]).to_string(), "[null, \"abc\"]")
	test.assert_eq(
		Json
			.array([
				Json.null,
				Json.string("abc"),
				Json.bool(false),
				Json.number(Number.int(5)),
				Json.number(Number.float(3.4)),
				Json.array([Json.null]),
			])
			.to_string(),
		"[null, \"abc\", false, 5, 3.4, [null]]",
	)

	//object tests
	test.assert_eq(Json.object(#[]).to_string(), "{}")
	test.assert_eq(Json.object(#["abc": Json.null]).to_string(), "{\n\t\"abc\": null\n}")
	test.assert_eq(
		Json
			.object(#[
				"abc": Json.null,
				"def": Json.object(#[
					"ghi": Json.array([
						Json.null,
						Json.string("abc"),
						Json.bool(false),
						Json.number(Number.int(5)),
						Json.number(Number.float(3.4)),
						Json.array([Json.null]),
					]),
					"jkl": Json.object(#[
						"mno": Json.bool(true),
						"pqr": Json.string("value"),
					]),
				]),
			])
			.to_string(),
		"{\n\t\"abc\": null,\n\t\"def\": {\n\t\t\"ghi\": [null, \"abc\", false, 5, 3.4, [null]],\n\t\t\"jkl\": {\n\t\t\t\"mno\": true,\n\t\t\t\"pqr\": \"value\"\n\t\t}\n\t}\n}",
	)
}

//bind string.to_json = (self): Json Json.string(self.copy())

// NOTE (Audit Comment):
// 1. In `string.to_json` below, the return type is `Result[Json, string]`, so:
//    - Line 118-119 (`when 't'` / `when 'f'`) and line 123 need `Result.ok(...)`
//      (e.g. `func <- Result.ok(Json.bool(true))` and `Result.ok(out)`).
// 2. In `to_json_helper`, `str` is an `own string` parameter (immutable by default unless
//    copied to a `let` binding), and on line 137 `str[1..str.len()-2]` drops the last 2
//    characters instead of 1 (`str[1..str.len()-1]` strips only the closing quote).
bind string.to_json = (self) Result[Json]: {
	let in_str = self.trim()

	if(in_str.len() == 0): func <- value(Json.empty)

	let idx = 0
	value(_to_json_helper(self, &idx): default err(own e): <- err(e.wrap("something went wrong when parsing the json string.")))
}

bind _to_json_helper = (str: string, idx: &int) Result[Json]: {
	if(str.len() <= idx): <- Result.fail_msg("idx is greater than the json string length")

	while(char(str[idx]).is_whitespace()): idx += 1

	match(char(str[idx])):
		when '{': _parse_json_object(str, &idx)
		when '[': _parse_json_array(str, &idx)
		when '"', '\'': _parse_json_string(str, &idx)
		when 't', 'f': _parse_json_bool(str, &idx)
		when in '0'..='9', '-': _parse_json_number(str, &idx)
		when 'n': _parse_json_null(str, &idx)
		default: Result.fail_msg("invalid json")
}

bind _parse_json_string = (str: string, idx: &int) Result[Json]: {
		define quote enum [
		single,
		double,
	]

	let q = if(char(str[idx]) == '"'): quote.double
	else if(char(str[idx]) == '\''): quote.single
	else: <- err(Error.new("Json string expected to start with \" or \'"))

	let res = ""

	idx = idx + 1
	while(idx < str.len()): {
		if(char(str[idx]) == '\\' and idx + 1 < str.len()):
			match(q):
				when quote.single:
					if(char(str[idx]) == '\''): {
						res += string(char(str[idx]))
						idx = idx + 1
						res += string(char(str[idx]))
						idx = idx + 1
						next <-
					}
				default quote.double:
					if(char(str[idx]) == '\"'): {
						res += string(char(str[idx]))
						idx = idx + 1
						res += string(char(str[idx]))
						idx = idx + 1
						next <-
					}

		match(q):
			when quote.single:
				if(char(str[idx]) == '\''): {
					idx = idx + 1
					func <- value(Json.string(res))
				}
			default quote.double:
				if(char(str[idx]) == '\"'): {
					idx = idx + 1
					func <- value(Json.string(res))
				}

		res += string(char(str[idx]))
		idx = idx + 1
	}

	err(Error.new("Json endquote never reached"))
}

bind _parse_json_bool = (s: string, idx: &int) Result[Json]:
	match(char(s[idx])):
		when 't': {
			if(idx + 3 >= s.len()): func <- Result.fail_msg(
				"Json ended while parsing boolean true value"
			)
			if(s[idx..idx + 4] != "true"): <- Result.fail_msg("Invalid boolean value")
			idx = idx + 4
			value(Json.bool(true))
		}
		when 'f': {
			if(idx + 4 >= s.len()): func <- Result.fail_msg(
				"Json ended while parsing boolean false value"
			)
			if(s[idx..idx + 5] != "false"): <- Result.fail_msg("Invalid boolean value")
			idx = idx + 5
			value(Json.bool(false))
		}
		default: Result.fail_msg("expected bool")

bind _parse_json_null = (s: string, idx: &int) Result[Json]: {
	if(idx + 3 >= s.len()): func <- Result.fail_msg("Json ended while parsing null value")
	if(s[idx..idx + 4] == "null"): <- value(Json.null)
	println(s[idx..idx + 4])
	idx = idx + 4
	Result.fail_msg("Invalid null value")
}

bind _parse_json_number = (str: string, idx: &int) Result[Json]: {
	let dot = false
	let neg = false

	let num = ""

	while(idx < str.len()): {
		match(char(str[idx])):
			when is_digit(): num += string(char(str[idx]))
			when '.': {
				if(dot): func <- Result.fail_msg("Invalid number value")
				dot = true
				num += string(char(str[idx]))
			}
			when '-': {
				if(neg): func <- Result.fail_msg("Invalid number value")
				neg = true
			}
			default: while <-

		idx = idx + 1
	}

	if(dot): {
		let fl? = num.to_float()

		if(fl?): func <- value(Json.number(Number.float(if(neg): -fl else: fl)))
	} else: {
		let i? = num.to_int()

		if(i?): func <- value(Json.number(Number.int(if(neg): -i else: i)))
	}

	Result.fail_msg("Invalid number value")
}

bind _parse_json_array = (str: string, idx: &int) Result[Json]: {
	if(idx >= str.len()): <- Result.fail_msg(
		"idx is out of bounds of the string str.len(): ${str.len()} idx: $idx"
	)
	if(char(str[idx]) != '['): <- Result.fail_msg("Json array expected to start with [")

	let items: [Json] = []

	idx += 1

	while(char(str[idx]).is_whitespace()): idx += 1

	if(char(str[idx]) == ']'): <- value(Json.array([]))

	while(idx < str.len()): {
		let t = _to_json_helper(str, &idx):
			default err(own e): func <- err(e.wrap(""))

		while(char(str[idx]).is_whitespace()): idx += 1

		match(char(str[idx])):
			when ',': {}
			when ']': while <-
			default: func <- Result.fail_msg(
				"Invalid Json array. Expected , or ]. Got: ${char(str[idx])}"
			)

		idx = idx + 1
		while(char(str[idx]).is_whitespace()): idx += 1
	}

	idx += 1

	value(Json.array(items))
}

bind _parse_json_object = (str: string, idx: &int) Result[Json]: {
	if(idx >= str.len()): <- Result.fail_msg(
		"idx is out of bounds of the string str.len(): ${str.len()} idx: $idx"
	)
	if(char(str[idx]) != '{'): <- Result.fail_msg("Json object expected to start with {")

	let items: #[string: Json] = #[]

	idx += 1

	while(char(str[idx]).is_whitespace()): idx += 1

	if(char(str[idx]) == '}'): <- value(Json.object(#[]))

	while(idx < str.len()): {
		let key_raw = _parse_json_string(str, &idx):
			default err(own e): func <- err(e.wrap("failed to parse Jason object member key"))
		let key = key_raw.to_string()

		key = key[1..]
		key = key[..key.len() - 1] // this doesn't seem ideal. There needs to be a string functon for this

		while(char(str[idx]).is_whitespace()): idx += 1

		if(char(str[idx]) != ':'): func <- Result.fail_msg("failed to parse Jason object :")
		idx += 1

		while(char(str[idx]).is_whitespace()): idx += 1

		match(_to_json_helper(str, &idx)):
			when value(own o): items.push(MapEntry[key: key, value: o])
			default err(own e): func <- err(
				e.wrap("failed to parse Jason object member value")
			)

		match(char(str[idx])):
			when ',': {}
			when '}': while <-
			default: func <- Result.fail_msg(
				"Invalid Json object. Expected , or }. Got: ${char(str[idx])}"
			)

		idx = idx + 1
		while(char(str[idx]).is_whitespace()): idx += 1
	}

	idx += 1

	value(Json.object(items))
}

bind Number.to_json = (self) Json: Json.number(self)

@test("tests for string.to_json")
bind string_to_json_test = ():
	test.assert_all_ok_eq_named[Json](
		[
			+[
				"empty string",
				Json.empty,
				``.to_json(),
			],
			+[
				"double quote string",
				Json.string("abc"),
				`"abc"`.to_json(),
			],
			+[
				"single quote string",
				Json.string("abc"),
				`'abc'`.to_json(),
			],
			+[
				"double quote empty string",
				Json.string(""),
				`""`.to_json(),
			],
			+[
				"single quote empty string",
				Json.string(""),
				`''`.to_json(),
			],
			+[
				"single quote in double quote",
				Json.string("'"),
				`"'"`.to_json(),
			],
			+[
				"double quote in single quote",
				Json.string(`"`),
				`'"'`.to_json(),
			],
			+[
				"true",
				Json.bool(true),
				`true`.to_json(),
			],
			+[
				"false",
				Json.bool(false),
				`false`.to_json(),
			],
			+[
				"null",
				Json.null,
				`null`.to_json(),
			],
			+[
				"5",
				Json.number(Number.int(5)),
				`5`.to_json(),
			],
			+[
				"5.5",
				Json.number(Number.float(5.5)),
				`5.5`.to_json(),
			],
			+[
				"empty array",
				Json.array([]),
				`[]`.to_json(),
			],
			+[
				"full array",
				Json.array([Json.string("abc"), Json.number(Number.int(123)), Json.bool(false)]),
				`["abc", 123, false]`.to_json(),
			],
			+[
				"nested array",
				Json.array([
					Json.string("abc"),
					Json.number(Number.int(123)),
					Json.bool(false),
					Json.array([Json.string("abc"), Json.number(Number.int(123)), Json.bool(false)]),
				]),
				`["abc", 123, false, ["abc", 123, false]]`.to_json(),
			],
			+[
				"empty object",
				Json.object(#[]),
				`{}`.to_json(),
			],
			+[
				"object",
				Json.object(#["abc": Json.bool(true)]),
				`{"abc": true}`.to_json(),
			],
			+[
				"full object",
				Json.object(#[
					"abc": Json.number(Number.int(123)),
					"xyz": Json.number(Number.float(5.3)),
					"def": Json.object(#[
						"abc": Json.array([
							Json.number(Number.int(1)),
							Json.number(Number.int(2)),
							Json.number(Number.int(3)),
							Json.string("abc"),
							Json.bool(true),
						])
					]),
				]),
				`{"abc": 123, "xyz": 5.3, "def": {"abc": [1, 2, 3, "abc", true]}}`.to_json(),
			],
			// tests from ai:

			// whitespace handling
			+[
				"whitespace only spaces",
				Json.empty,
				"     ".to_json(),
			],
			+[
				"whitespace only mixed",
				Json.empty,
				" \t\n\r ".to_json(),
			],
			+[
				"leading and trailing whitespace around string",
				Json.string("abc"),
				"  \t \"abc\" \n ".to_json(),
			],
			+[
				"leading and trailing whitespace around bool",
				Json.bool(true),
				"\n\t true \t\n".to_json(),
			],
			+[
				"leading and trailing whitespace around null",
				Json.null,
				"   null   ".to_json(),
			],
			+[
				"leading and trailing whitespace around number",
				Json.number(Number.int(42)),
				"  42  ".to_json(),
			],
			+[
				"whitespace inside empty array",
				Json.array([]),
				"[ \t\n ]".to_json(),
			],
			+[
				"whitespace inside empty object",
				Json.object(#[]),
				"{ \t\n }".to_json(),
			],
			// strings
			+[
				"string with spaces",
				Json.string("hello world"),
				`"hello world"`.to_json(),
			],
			+[
				"string containing digits",
				Json.string("12345"),
				`"12345"`.to_json(),
			],
			+[
				"string containing keywords",
				Json.string("true false null"),
				`"true false null"`.to_json(),
			],
			+[
				"string containing json delimiters",
				Json.string("a: b, [1, 2], {x: y}"),
				`"a: b, [1, 2], {x: y}"`.to_json(),
			],
			+[
				"string with escaped double quotes",
				Json.string(`hello \"world\"`),
				`"hello \"world\""`.to_json(),
			],
			+[
				"string with escaped single quotes",
				Json.string(`hello \'world\'`),
				`'hello \'world\''`.to_json(),
			],
			+[
				"string with special symbols",
				Json.string("!@#\$%^&*()_+-=/?"),
				`"!@#\$%^&*()_+-=/?"`.to_json(),
			],
			// numbers
			+[
				"zero int",
				Json.number(Number.int(0)),
				`0`.to_json(),
			],
			+[
				"multi digit int",
				Json.number(Number.int(987654)),
				`987654`.to_json(),
			],
			+[
				"negative int",
				Json.number(Number.int(-42)),
				`-42`.to_json(),
			],
			+[
				"zero float",
				Json.number(Number.float(0.0)),
				`0.0`.to_json(),
			],
			+[
				"small float",
				Json.number(Number.float(0.005)),
				`0.005`.to_json(),
			],
			+[
				"multi digit float",
				Json.number(Number.float(123.456)),
				`123.456`.to_json(),
			],
			+[
				"negative float",
				Json.number(Number.float(-3.14)),
				`-3.14`.to_json(),
			],
			// arrays
			+[
				"single element array null",
				Json.array([Json.null]),
				`[null]`.to_json(),
			],
			+[
				"single element array string",
				Json.array([Json.string("only")]),
				`["only"]`.to_json(),
			],
			+[
				"single element array bool",
				Json.array([Json.bool(true)]),
				`[true]`.to_json(),
			],
			+[
				"single element array int",
				Json.array([Json.number(Number.int(7))]),
				`[7]`.to_json(),
			],
			+[
				"single element array float",
				Json.array([Json.number(Number.float(2.5))]),
				`[2.5]`.to_json(),
			],
			+[
				"array with no spaces",
				Json.array([
					Json.number(Number.int(1)),
					Json.number(Number.int(2)),
					Json.number(Number.int(3)),
				]),
				`[1,2,3]`.to_json(),
			],
			+[
				"array with extra whitespace and newlines",
				Json.array([
					Json.string("a"),
					Json.string("b"),
					Json.string("c"),
				]),
				"[\n\t\"a\" ,\n\t\"b\" ,\n\t\"c\"\n]".to_json(),
			],
			+[
				"array of nulls and bools",
				Json.array([Json.null, Json.null, Json.bool(true), Json.bool(false)]),
				`[null, null, true, false]`.to_json(),
			],
			+[
				"array with mixed quote styles",
				Json.array([Json.string("double"), Json.string("single")]),
				`["double", 'single']`.to_json(),
			],
			+[
				"nested empty arrays",
				Json.array([Json.array([]), Json.array([Json.array([])])]),
				`[[], [[]]]`.to_json(),
			],
			+[
				"2d array of ints",
				Json.array([
					Json.array([Json.number(Number.int(1)), Json.number(Number.int(2))]),
					Json.array([Json.number(Number.int(3)), Json.number(Number.int(4))]),
				]),
				`[[1, 2], [3, 4]]`.to_json(),
			],
			+[
				"array of objects",
				Json.array([
					Json.object(#["a": Json.number(Number.int(1))]),
					Json.object(#["b": Json.string("two")]),
					Json.object(#[]),
				]),
				`[{"a": 1}, {"b": "two"}, {}]`.to_json(),
			],
			// objects
			+[
				"object with empty string key",
				Json.object(#["": Json.string("val")]),
				`{"": "val"}`.to_json(),
			],
			+[
				"object with single quoted key and value",
				Json.object(#["key": Json.string("val")]),
				`{'key': 'val'}`.to_json(),
			],
			+[
				"object with null value",
				Json.object(#["a": Json.null]),
				`{"a": null}`.to_json(),
			],
			+[
				"object with multiple null and bool values",
				Json.object(#[
					"a": Json.null,
					"b": Json.bool(true),
					"c": Json.bool(false),
					"d": Json.null,
				]),
				`{"a": null, "b": true, "c": false, "d": null}`.to_json(),
			],
			+[
				"object with no spaces",
				Json.object(#[
					"a": Json.number(Number.int(1)),
					"b": Json.string("two"),
					"c": Json.array([Json.bool(true), Json.bool(false)]),
				]),
				`{"a":1,"b":"two","c":[true,false]}`.to_json(),
			],
			+[
				"object with spaces before colon and commas",
				Json.object(#[
					"a": Json.number(Number.int(1)),
					"b": Json.number(Number.int(2)),
				]),
				`{ "a" : 1 , "b" : 2 }`.to_json(),
			],
			+[
				"object with empty array and empty object values",
				Json.object(#[
					"arr": Json.array([]),
					"obj": Json.object(#[]),
				]),
				`{"arr": [], "obj": {}}`.to_json(),
			],
			+[
				"object containing array of objects",
				Json.object(#[
					"users": Json.array([
						Json.object(#[
							"id": Json.number(Number.int(1)),
							"active": Json.bool(true),
						]),
						Json.object(#[
							"id": Json.number(Number.int(2)),
							"active": Json.bool(false),
						]),
					])
				]),
				`{"users": [{"id": 1, "active": true}, {"id": 2, "active": false}]}`.to_json(),
			],
			+[
				"pretty printed multiline object",
				Json.object(#[
					"name": Json.string("test"),
					"count": Json.number(Number.int(3)),
					"nested": Json.object(#[
						"flag": Json.bool(false),
						"list": Json.array([Json.number(Number.int(1)), Json.number(Number.int(2))]),
					]),
				]),
				"{\n\t\"name\": \"test\",\n\t\"count\": 3,\n\t\"nested\": {\n\t\t\"flag\": false,\n\t\t\"list\": [1, 2]\n\t}\n}".to_json(),
			],
			+[
				"deeply nested object",
				Json.object(#[
					"a": Json.object(#[
						"b": Json.object(#[
							"c": Json.object(#[
								"d": Json.string("deep")
							])
						])
					])
				]),
				`{"a": {"b": {"c": {"d": "deep"}}}}`.to_json(),
			],
			+[
				"round trip from Json.to_string",
				Json.object(#[
					"a": Json.string("hello"),
					"b": Json.array([
						Json.number(Number.int(1)),
						Json.number(Number.float(2.5)),
						Json.bool(true),
						Json.null,
					]),
					"c": Json.object(#["x": Json.bool(false)]),
				]),
				Json
					.object(#[
						"a": Json.string("hello"),
						"b": Json.array([
							Json.number(Number.int(1)),
							Json.number(Number.float(2.5)),
							Json.bool(true),
							Json.null,
						]),
						"c": Json.object(#["x": Json.bool(false)]),
					])
					.to_string()
					.to_json(),
			],
		]
	)

bind bool.to_json = (self) Json: Json.bool(self)

bind int.to_json = (self) Json: Json.number(Number.int(self))

bind char.to_json = (self) Json: Json.string(self.to_string())

bind float.to_json = (self) Json: Json.string(self.to_string())

bind u8.to_json = (self) Json: Json.string(self.to_string())

bind u16.to_json = (self) Json: Json.string(self.to_string())

bind u32.to_json = (self) Json: Json.string(self.to_string())

bind u64.to_json = (self) Json: Json.string(self.to_string())

bind i8.to_json = (self) Json: Json.string(self.to_string())

bind i16.to_json = (self) Json: Json.string(self.to_string())

bind i32.to_json = (self) Json: Json.string(self.to_string())

// bind list.to_json = (self): Json

// bind map.to_json = (self): Json
