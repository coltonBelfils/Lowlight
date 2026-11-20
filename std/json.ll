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

define Json enum [
	Empty,
	Null,
	String: string,
	Bool: bool,
	Number: Number,
	Array: [Json],
	Object: #[string: Json],
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
		when Json.Empty, Json.Empty: true
		when Json.Null, Json.Null: true
		when Json.String(s), Json.String(os): s == os
		when Json.Bool(b), Json.Bool(ob): b == ob
		when Json.Number(n), Json.Number(on): n == on
		when Json.Array(a), Json.Array(oa): a == oa
		when Json.Object(o), Json.Object(oo): {
			if(o.len() != oo.len()): <- false

			while(i in o): {
				if(not oo.contains(i.key) or i.value != oo[i.key]): while <- false
			} else: true
		}
		default: false

define Number enum [
	Int: int,
	Float: float,
]

define Jsonable interface [
	to_json: (self) Json
]

bind Json.to_string = (json: Json) string:
	match(json):
		when Json.Empty: ""
		when Json.Null: "null"
		when Json.String(s): "\"${s.to_string()}\""
		when Json.Bool(b): b.to_string()
		when Json.Number(n):
			match(n):
				when Number.Int(i): i.to_string()
				default Number.Float(f): f.to_string()
		when Json.Array(a): {
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
		default Json.Object(o): {
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
	test.assert_eq(Json.Empty.to_string(), "")
	test.assert_eq(Json.Bool(false).to_string(), "false")
	test.assert_eq(Json.String("").to_string(), "\"\"")
	test.assert_eq(Json.String("hello world").to_string(), "\"hello world\"")
	test.assert_eq(Json.String("123").to_string(), "\"123\"")
	test.assert_eq(Json.String("true").to_string(), "\"true\"")
	test.assert_eq(Json.String("a: b, [c], {d}").to_string(), "\"a: b, [c], {d}\"")
	test.assert_eq(Json.String("'single'").to_string(), "\"'single'\"")
	test.assert_eq(Json.Number(Number.Int(0)).to_string(), "0")
	test.assert_eq(Json.Number(Number.Int(-1)).to_string(), "-1")
	test.assert_eq(Json.Number(Number.Int(-42)).to_string(), "-42")
	test.assert_eq(Json.Number(Number.Int(1000000)).to_string(), "1000000")
	test.assert_eq(Json.Number(Number.Float(0.0)).to_string(), "0")
	test.assert_eq(Json.Number(Number.Float(0.5)).to_string(), "0.5")
	test.assert_eq(Json.Number(Number.Float(-1.5)).to_string(), "-1.5")
	test.assert_eq(Json.Number(Number.Float(123.456)).to_string(), "123.456")

	// array tests
	test.assert_eq(Json.Array([Json.String("")]).to_string(), "[\"\"]")
	test.assert_eq(Json.Array([Json.Bool(true)]).to_string(), "[true]")
	test.assert_eq(Json.Array([Json.Bool(false)]).to_string(), "[false]")
	test.assert_eq(Json.Array([Json.Number(Number.Int(0))]).to_string(), "[0]")
	test.assert_eq(Json.Array([Json.Number(Number.Float(-2.5))]).to_string(), "[-2.5]")
	test.assert_eq(Json.Array([Json.Array([])]).to_string(), "[[]]")
	test.assert_eq(Json.Array([Json.Object(#[])]).to_string(), "[{}]")
	test.assert_eq(
		Json.Array([Json.Bool(true), Json.Bool(false), Json.Null]).to_string(),
		"[true, false, null]",
	)
	test.assert_eq(
		Json.Array([Json.Array([]), Json.Array([]), Json.Array([])]).to_string(),
		"[[], [], []]",
	)
	test.assert_eq(
		Json.Array([Json.Array([Json.Array([])])]).to_string(),
		"[[[]]]",
	)
	test.assert_eq(
		Json
			.Array([
				Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Int(2))]),
				Json.Array([Json.Number(Number.Int(3)), Json.Number(Number.Int(4))]),
			])
			.to_string(),
		"[[1, 2], [3, 4]]",
	)
	test.assert_eq(
		Json
			.Array([
				Json.Object(#["a": Json.Number(Number.Int(1))]),
				Json.Object(#["b": Json.Number(Number.Int(2))]),
			])
			.to_string(),
		"[{\n\t\"a\": 1\n}, {\n\t\"b\": 2\n}]",
	)

	// object tests
	test.assert_eq(
		Json.Object(#["": Json.String("empty key")]).to_string(),
		"{\n\t\"\": \"empty key\"\n}",
	)
	test.assert_eq(
		Json.Object(#["flag": Json.Bool(false)]).to_string(),
		"{\n\t\"flag\": false\n}",
	)
	test.assert_eq(
		Json.Object(#["num": Json.Number(Number.Int(-99))]).to_string(),
		"{\n\t\"num\": -99\n}",
	)
	test.assert_eq(
		Json.Object(#["pi": Json.Number(Number.Float(3.14))]).to_string(),
		"{\n\t\"pi\": 3.14\n}",
	)
	test.assert_eq(
		Json.Object(#["arr": Json.Array([])]).to_string(),
		"{\n\t\"arr\": []\n}",
	)
	test.assert_eq(
		Json.Object(#["obj": Json.Object(#[])]).to_string(),
		"{\n\t\"obj\": {}\n}",
	)
	test.assert_eq(
		Json
			.Object(#[
				"a": Json.Array([]),
				"b": Json.Object(#[]),
				"c": Json.String(""),
			])
			.to_string(),
		"{\n\t\"a\": [],\n\t\"b\": {},\n\t\"c\": \"\"\n}",
	)
	test.assert_eq(
		Json
			.Object(#[
				"items": Json.Array([
					Json.Object(#["id": Json.Number(Number.Int(1))]),
					Json.Object(#["id": Json.Number(Number.Int(2))]),
				])
			])
			.to_string(),
		"{\n\t\"items\": [{\n\t\t\"id\": 1\n\t}, {\n\t\t\"id\": 2\n\t}]\n}",
	)
	test.assert_eq(
		Json
			.Object(#[
				"a": Json.Object(#[
					"b": Json.Object(#[
						"c": Json.Object(#[
							"d": Json.Number(Number.Int(42))
						])
					])
				])
			])
			.to_string(),
		"{\n\t\"a\": {\n\t\t\"b\": {\n\t\t\t\"c\": {\n\t\t\t\t\"d\": 42\n\t\t\t}\n\t\t}\n\t}\n}",
	)

	//my tests

	//Json.Empty
	test.assert_eq(Json.Null.to_string(), "null")
	test.assert_eq(Json.String("abc").to_string(), "\"abc\"")
	test.assert_eq(Json.Bool(true).to_string(), "true")
	test.assert_eq(Json.Bool(true).to_string(), "true")
	test.assert_eq(Json.Number(Number.Int(1)).to_string(), "1")
	test.assert_eq(Json.Number(Number.Float(1.0)).to_string(), "1")
	test.assert_eq(Json.Number(Number.Float(1.5)).to_string(), "1.5")
	test.assert_eq(Json.Array([]).to_string(), "[]")
	test.assert_eq(Json.Array([Json.Null]).to_string(), "[null]")
	test.assert_eq(Json.Array([Json.Null, Json.Null]).to_string(), "[null, null]")
	test.assert_eq(Json.Array([Json.Null, Json.String("abc")]).to_string(), "[null, \"abc\"]")
	test.assert_eq(
		Json
			.Array([
				Json.Null,
				Json.String("abc"),
				Json.Bool(false),
				Json.Number(Number.Int(5)),
				Json.Number(Number.Float(3.4)),
				Json.Array([Json.Null]),
			])
			.to_string(),
		"[null, \"abc\", false, 5, 3.4, [null]]",
	)

	//object tests
	test.assert_eq(Json.Object(#[]).to_string(), "{}")
	test.assert_eq(Json.Object(#["abc": Json.Null]).to_string(), "{\n\t\"abc\": null\n}")
	test.assert_eq(
		Json
			.Object(#[
				"abc": Json.Null,
				"def": Json.Object(#[
					"ghi": Json.Array([
						Json.Null,
						Json.String("abc"),
						Json.Bool(false),
						Json.Number(Number.Int(5)),
						Json.Number(Number.Float(3.4)),
						Json.Array([Json.Null]),
					]),
					"jkl": Json.Object(#[
						"mno": Json.Bool(true),
						"pqr": Json.String("value"),
					]),
				]),
			])
			.to_string(),
		"{\n\t\"abc\": null,\n\t\"def\": {\n\t\t\"ghi\": [null, \"abc\", false, 5, 3.4, [null]],\n\t\t\"jkl\": {\n\t\t\t\"mno\": true,\n\t\t\t\"pqr\": \"value\"\n\t\t}\n\t}\n}",
	)
}

//bind string.to_json = (self): Json Json.String(self.copy())

// NOTE (Audit Comment):
// 1. In `string.to_json` below, the return type is `Result[Json, string]`, so:
//    - Line 118-119 (`when 't'` / `when 'f'`) and line 123 need `Result.ok(...)`
//      (e.g. `func <- Result.ok(Json.Bool(true))` and `Result.ok(out)`).
// 2. In `to_json_helper`, `str` is an `own string` parameter (immutable by default unless
//    copied to a `let` binding), and on line 137 `str[1..str.len()-2]` drops the last 2
//    characters instead of 1 (`str[1..str.len()-1]` strips only the closing quote).
bind string.to_json = (self) Result[Json]: {
	let in_str = self.trim()

	if(in_str.len() == 0): func <- Result.ok(Json.Empty)

	let idx = 0
	_to_json_helper(self, &idx)
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
	else: <- Result.fail(Error.new("Json string expected to start with \" or \'"))

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
					func <- Result.ok(Json.String(res))
				}
			default quote.double:
				if(char(str[idx]) == '\"'): {
					idx = idx + 1
					func <- Result.ok(Json.String(res))
				}

		res += string(char(str[idx]))
		idx = idx + 1
	}

	Result.fail(Error.new("Json endquote never reached"))
}

bind _parse_json_bool = (s: string, idx: &int) Result[Json]:
	match(char(s[idx])):
		when 't': {
			if(idx + 3 >= s.len()):
				func <- Result.fail_msg("Json ended while parsing boolean true value")
			if(s[idx..idx + 4] != "true"): <- Result.fail_msg("Invalid boolean value")
			idx = idx + 4
			Result.ok(Json.Bool(true))
		}
		when 'f': {
			if(idx + 4 >= s.len()):
				func <- Result.fail_msg("Json ended while parsing boolean false value")
			if(s[idx..idx + 5] != "false"): <- Result.fail_msg("Invalid boolean value")
			idx = idx + 5
			Result.ok(Json.Bool(false))
		}
		default: Result.fail_msg("expected bool")

bind _parse_json_null = (s: string, idx: &int) Result[Json]: {
	if(idx + 3 >= s.len()): func <- Result.fail_msg("Json ended while parsing null value")
	if(s[idx..idx + 4] == "null"): <- Result.ok(Json.Null)
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

		if(fl?): func <- Result.Ok(Json.Number(Number.Float(if(neg): -fl else: fl)))
	} else: {
		let i? = num.to_int()

		if(i?): func <- Result.Ok(Json.Number(Number.Int(if(neg): -i else: i)))
	}

	Result.fail_msg("Invalid number value")
}

bind _parse_json_array = (str: string, idx: &int) Result[Json]: {
	if(idx >= str.len()):
		<- Result.fail_msg("idx is out of bounds of the string str.len(): ${str.len()} idx: $idx")
	if(char(str[idx]) != '['): <- Result.fail_msg("Json array expected to start with [")

	let items: [Json] = []

	idx += 1

	while(char(str[idx]).is_whitespace()): idx += 1

	if(char(str[idx]) == ']'): <- Result.Ok(Json.Array([]))

	while(idx < str.len()): {
		let t = _to_json_helper(str, &idx)
		match(own t):
			when Result.Ok(own o): items.push(own o)
			default Result.Err(e): func <- Result.Err(e.wrap("failed to parse Jason array member"))

		while(char(str[idx]).is_whitespace()): idx += 1

		match(char(str[idx])):
			when ',': {}
			when ']': while <-
			default:
				func <- Result.fail_msg(
					"Invalid Json array. Expected , or ]. Got: ${char(str[idx])}"
				)

		idx = idx + 1
		while(char(str[idx]).is_whitespace()): idx += 1
	}

	idx += 1

	Result.Ok(Json.Array(items))
}

bind _parse_json_object = (str: string, idx: &int) Result[Json]: {
	if(idx >= str.len()):
		<- Result.fail_msg("idx is out of bounds of the string str.len(): ${str.len()} idx: $idx")
	if(char(str[idx]) != '{'): <- Result.fail_msg("Json object expected to start with {")

	let items: #[string: Json] = #[]

	idx += 1

	while(char(str[idx]).is_whitespace()): idx += 1

	if(char(str[idx]) == '}'): <- Result.Ok(Json.Object(#[]))

	while(idx < str.len()): {
		let key = match(_parse_json_string(str, &idx)):
			when Result.Ok(own o): o.to_string()
			default Result.Err(e):
				func <- Result.Err(e.wrap("failed to parse Jason object member key"))
		key = key[1..]
		key = key[..key.len() - 1] // this doesn't seem ideal. There needs to be a string functon for this

		while(char(str[idx]).is_whitespace()): idx += 1

		if(char(str[idx]) != ':'): func <- Result.fail_msg("failed to parse Jason object :")
		idx += 1

		while(char(str[idx]).is_whitespace()): idx += 1

		let v = _to_json_helper(str, &idx)
		match(own v):
			when Result.Ok(own o): items.push(MapEntry[key: key, value: o])
			default Result.Err(e):
				func <- Result.Err(e.wrap("failed to parse Jason object member value"))

		match(char(str[idx])):
			when ',': {}
			when '}': while <-
			default:
				func <- Result.fail_msg(
					"Invalid Json object. Expected , or }. Got: ${char(str[idx])}"
				)

		idx = idx + 1
		while(char(str[idx]).is_whitespace()): idx += 1
	}

	idx += 1

	Result.Ok(Json.Object(items))
}

bind Number.to_json = (self) Json: Json.Number(self)

@test("tests for string.to_json")
bind string_to_json_test = ():
	test.assert_all_ok_eq_named[Json]([
		+[
			"empty string",
			Json.Empty,
			``.to_json(),
		],
		+[
			"double quote string",
			Json.String("abc"),
			`"abc"`.to_json(),
		],
		+[
			"single quote string",
			Json.String("abc"),
			`'abc'`.to_json(),
		],
		+[
			"double quote empty string",
			Json.String(""),
			`""`.to_json(),
		],
		+[
			"single quote empty string",
			Json.String(""),
			`''`.to_json(),
		],
		+[
			"single quote in double quote",
			Json.String("'"),
			`"'"`.to_json(),
		],
		+[
			"double quote in single quote",
			Json.String(`"`),
			`'"'`.to_json(),
		],
		+[
			"true",
			Json.Bool(true),
			`true`.to_json(),
		],
		+[
			"false",
			Json.Bool(false),
			`false`.to_json(),
		],
		+[
			"null",
			Json.Null,
			`null`.to_json(),
		],
		+[
			"5",
			Json.Number(Number.Int(5)),
			`5`.to_json(),
		],
		+[
			"5.5",
			Json.Number(Number.Float(5.5)),
			`5.5`.to_json(),
		],
		+[
			"empty array",
			Json.Array([]),
			`[]`.to_json(),
		],
		+[
			"full array",
			Json.Array([Json.String("abc"), Json.Number(Number.Int(123)), Json.Bool(false)]),
			`["abc", 123, false]`.to_json(),
		],
		+[
			"nested array",
			Json.Array([
				Json.String("abc"),
				Json.Number(Number.Int(123)),
				Json.Bool(false),
				Json.Array([Json.String("abc"), Json.Number(Number.Int(123)), Json.Bool(false)]),
			]),
			`["abc", 123, false, ["abc", 123, false]]`.to_json(),
		],
		+[
			"empty object",
			Json.Object(#[]),
			`{}`.to_json(),
		],
		+[
			"object",
			Json.Object(#["abc": Json.Bool(true)]),
			`{"abc": true}`.to_json(),
		],
		+[
			"full object",
			Json.Object(#[
				"abc": Json.Number(Number.Int(123)),
				"xyz": Json.Number(Number.Float(5.3)),
				"def": Json.Object(#[
					"abc": Json.Array([
						Json.Number(Number.Int(1)),
						Json.Number(Number.Int(2)),
						Json.Number(Number.Int(3)),
						Json.String("abc"),
						Json.Bool(true),
					])
				]),
			]),
			`{"abc": 123, "xyz": 5.3, "def": {"abc": [1, 2, 3, "abc", true]}}`.to_json(),
		],

		// tests from ai:

		// whitespace handling
		+[
			"whitespace only spaces",
			Json.Empty,
			"     ".to_json(),
		],
		+[
			"whitespace only mixed",
			Json.Empty,
			" \t\n\r ".to_json(),
		],
		+[
			"leading and trailing whitespace around string",
			Json.String("abc"),
			"  \t \"abc\" \n ".to_json(),
		],
		+[
			"leading and trailing whitespace around bool",
			Json.Bool(true),
			"\n\t true \t\n".to_json(),
		],
		+[
			"leading and trailing whitespace around null",
			Json.Null,
			"   null   ".to_json(),
		],
		+[
			"leading and trailing whitespace around number",
			Json.Number(Number.Int(42)),
			"  42  ".to_json(),
		],
		+[
			"whitespace inside empty array",
			Json.Array([]),
			"[ \t\n ]".to_json(),
		],
		+[
			"whitespace inside empty object",
			Json.Object(#[]),
			"{ \t\n }".to_json(),
		],

		// strings
		+[
			"string with spaces",
			Json.String("hello world"),
			`"hello world"`.to_json(),
		],
		+[
			"string containing digits",
			Json.String("12345"),
			`"12345"`.to_json(),
		],
		+[
			"string containing keywords",
			Json.String("true false null"),
			`"true false null"`.to_json(),
		],
		+[
			"string containing json delimiters",
			Json.String("a: b, [1, 2], {x: y}"),
			`"a: b, [1, 2], {x: y}"`.to_json(),
		],
		+[
			"string with escaped double quotes",
			Json.String(`hello \"world\"`),
			`"hello \"world\""`.to_json(),
		],
		+[
			"string with escaped single quotes",
			Json.String(`hello \'world\'`),
			`'hello \'world\''`.to_json(),
		],
		+[
			"string with special symbols",
			Json.String("!@#\$%^&*()_+-=/?"),
			`"!@#\$%^&*()_+-=/?"`.to_json(),
		],

		// numbers
		+[
			"zero int",
			Json.Number(Number.Int(0)),
			`0`.to_json(),
		],
		+[
			"multi digit int",
			Json.Number(Number.Int(987654)),
			`987654`.to_json(),
		],
		+[
			"negative int",
			Json.Number(Number.Int(-42)),
			`-42`.to_json(),
		],
		+[
			"zero float",
			Json.Number(Number.Float(0.0)),
			`0.0`.to_json(),
		],
		+[
			"small float",
			Json.Number(Number.Float(0.005)),
			`0.005`.to_json(),
		],
		+[
			"multi digit float",
			Json.Number(Number.Float(123.456)),
			`123.456`.to_json(),
		],
		+[
			"negative float",
			Json.Number(Number.Float(-3.14)),
			`-3.14`.to_json(),
		],

		// arrays
		+[
			"single element array null",
			Json.Array([Json.Null]),
			`[null]`.to_json(),
		],
		+[
			"single element array string",
			Json.Array([Json.String("only")]),
			`["only"]`.to_json(),
		],
		+[
			"single element array bool",
			Json.Array([Json.Bool(true)]),
			`[true]`.to_json(),
		],
		+[
			"single element array int",
			Json.Array([Json.Number(Number.Int(7))]),
			`[7]`.to_json(),
		],
		+[
			"single element array float",
			Json.Array([Json.Number(Number.Float(2.5))]),
			`[2.5]`.to_json(),
		],
		+[
			"array with no spaces",
			Json.Array([
				Json.Number(Number.Int(1)),
				Json.Number(Number.Int(2)),
				Json.Number(Number.Int(3)),
			]),
			`[1,2,3]`.to_json(),
		],
		+[
			"array with extra whitespace and newlines",
			Json.Array([
				Json.String("a"),
				Json.String("b"),
				Json.String("c"),
			]),
			"[\n\t\"a\" ,\n\t\"b\" ,\n\t\"c\"\n]".to_json(),
		],
		+[
			"array of nulls and bools",
			Json.Array([Json.Null, Json.Null, Json.Bool(true), Json.Bool(false)]),
			`[null, null, true, false]`.to_json(),
		],
		+[
			"array with mixed quote styles",
			Json.Array([Json.String("double"), Json.String("single")]),
			`["double", 'single']`.to_json(),
		],
		+[
			"nested empty arrays",
			Json.Array([Json.Array([]), Json.Array([Json.Array([])])]),
			`[[], [[]]]`.to_json(),
		],
		+[
			"2d array of ints",
			Json.Array([
				Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Int(2))]),
				Json.Array([Json.Number(Number.Int(3)), Json.Number(Number.Int(4))]),
			]),
			`[[1, 2], [3, 4]]`.to_json(),
		],
		+[
			"array of objects",
			Json.Array([
				Json.Object(#["a": Json.Number(Number.Int(1))]),
				Json.Object(#["b": Json.String("two")]),
				Json.Object(#[]),
			]),
			`[{"a": 1}, {"b": "two"}, {}]`.to_json(),
		],

		// objects
		+[
			"object with empty string key",
			Json.Object(#["": Json.String("val")]),
			`{"": "val"}`.to_json(),
		],
		+[
			"object with single quoted key and value",
			Json.Object(#["key": Json.String("val")]),
			`{'key': 'val'}`.to_json(),
		],
		+[
			"object with null value",
			Json.Object(#["a": Json.Null]),
			`{"a": null}`.to_json(),
		],
		+[
			"object with multiple null and bool values",
			Json.Object(#[
				"a": Json.Null,
				"b": Json.Bool(true),
				"c": Json.Bool(false),
				"d": Json.Null,
			]),
			`{"a": null, "b": true, "c": false, "d": null}`.to_json(),
		],
		+[
			"object with no spaces",
			Json.Object(#[
				"a": Json.Number(Number.Int(1)),
				"b": Json.String("two"),
				"c": Json.Array([Json.Bool(true), Json.Bool(false)]),
			]),
			`{"a":1,"b":"two","c":[true,false]}`.to_json(),
		],
		+[
			"object with spaces before colon and commas",
			Json.Object(#[
				"a": Json.Number(Number.Int(1)),
				"b": Json.Number(Number.Int(2)),
			]),
			`{ "a" : 1 , "b" : 2 }`.to_json(),
		],
		+[
			"object with empty array and empty object values",
			Json.Object(#[
				"arr": Json.Array([]),
				"obj": Json.Object(#[]),
			]),
			`{"arr": [], "obj": {}}`.to_json(),
		],
		+[
			"object containing array of objects",
			Json.Object(#[
				"users": Json.Array([
					Json.Object(#[
						"id": Json.Number(Number.Int(1)),
						"active": Json.Bool(true),
					]),
					Json.Object(#[
						"id": Json.Number(Number.Int(2)),
						"active": Json.Bool(false),
					]),
				]),
			]),
			`{"users": [{"id": 1, "active": true}, {"id": 2, "active": false}]}`.to_json(),
		],
		+[
			"pretty printed multiline object",
			Json.Object(#[
				"name": Json.String("test"),
				"count": Json.Number(Number.Int(3)),
				"nested": Json.Object(#[
					"flag": Json.Bool(false),
					"list": Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Int(2))]),
				]),
			]),
			"{\n\t\"name\": \"test\",\n\t\"count\": 3,\n\t\"nested\": {\n\t\t\"flag\": false,\n\t\t\"list\": [1, 2]\n\t}\n}".to_json(),
		],
		+[
			"deeply nested object",
			Json.Object(#[
				"a": Json.Object(#[
					"b": Json.Object(#[
						"c": Json.Object(#[
							"d": Json.String("deep"),
						]),
					]),
				]),
			]),
			`{"a": {"b": {"c": {"d": "deep"}}}}`.to_json(),
		],
		+[
			"round trip from Json.to_string",
			Json.Object(#[
				"a": Json.String("hello"),
				"b": Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Float(2.5)), Json.Bool(true), Json.Null]),
				"c": Json.Object(#["x": Json.Bool(false)]),
			]),
			Json
				.Object(#[
					"a": Json.String("hello"),
					"b": Json.Array([Json.Number(Number.Int(1)), Json.Number(Number.Float(2.5)), Json.Bool(true), Json.Null]),
					"c": Json.Object(#["x": Json.Bool(false)]),
				])
				.to_string()
				.to_json(),
		],
	])

bind bool.to_json = (self) Json: Json.Bool(self)

bind int.to_json = (self) Json: Json.Number(Number.Int(self))

bind char.to_json = (self) Json: Json.String(self.to_string())

bind float.to_json = (self) Json: Json.String(self.to_string())

bind u8.to_json = (self) Json: Json.String(self.to_string())

bind u16.to_json = (self) Json: Json.String(self.to_string())

bind u32.to_json = (self) Json: Json.String(self.to_string())

bind u64.to_json = (self) Json: Json.String(self.to_string())

bind i8.to_json = (self) Json: Json.String(self.to_string())

bind i16.to_json = (self) Json: Json.String(self.to_string())

bind i32.to_json = (self) Json: Json.String(self.to_string())

// bind list.to_json = (self): Json

// bind map.to_json = (self): Json
