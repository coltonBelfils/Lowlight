//This file is completely written by hand by me. Please leave it as is. Feel free to comment on it though

/*
Json.to_string
to_json
Jsonable (maybe a better name is needed)
Json type
Json.eq()
*/

import "std/test.ll"
import "std/strings.ll"
import "std/chars.ll"
import "std/strconv.ll"

define Json enum [
    Empty,
    Null,
    String: string,
    Bool: bool,
    Number: Number,
    Array: [Json],
    Object: #[string: Json],
]

define Number enum [
    Int: int,
    Float: float,
]

define Jsonable interface [
    to_json: (self): Json,
]

bind Json.to_string = (json: Json): string -> {
    match (json)
    when Json.Empty: ""
    when Json.Null: "null"
    when Json.String(s): "\"${s.to_string()}\""
    when Json.Bool(b): b.to_string()
    when Json.Number(n): match (n)
    when Number.Int(i): i.to_string()
    else Number.Float(f): f.to_string()
    when Json.Array(a): {
        let out = "["

        for(i in a) out += {i.to_string() + ", "}

        if (out == "[") match <- "[]"

        assert(out.len() > 1)
        out = out[0..out.len()-2]

        out += "]"

        out
    }
    else Json.Object(o): {
        let out = "{"

        for(i in o) out += {"\n\"" + i.key.to_string() + "\": " + i.value.to_string() + ","}

        if (out == "{") <- "{}"

        assert(out.len() > 0)
        out = out[0..out.len()-1]

        out = out.replace_all("\n", "\n\t")

        out += "\n}"

        out
    }
}

@test("test_json_to_string")
bind test_json_to_string = () -> {
    //Json.Empty
    assert_eq(Json.Null.to_string(), "null")
    assert_eq(Json.String("abc").to_string(), "\"abc\"")
    assert_eq(Json.Bool(true).to_string(), "true")
    assert_eq(Json.Bool(true).to_string(), "true")
    assert_eq(Json.Number(Number.Int(1)).to_string(), "1")
    assert_eq(Json.Number(Number.Float(1.0)).to_string(), "1")
    assert_eq(Json.Number(Number.Float(1.5)).to_string(), "1.5")
    assert_eq(Json.Array([]).to_string(), "[]")
    assert_eq(Json.Array([Json.Null]).to_string(), "[null]")
    assert_eq(Json.Array([Json.Null, Json.Null]).to_string(), "[null, null]")
    assert_eq(Json.Array([Json.Null, Json.String("abc")]).to_string(), "[null, \"abc\"]")
    assert_eq(Json.Array([Json.Null, Json.String("abc"), Json.Bool(false), Json.Number(Number.Int(5)), Json.Number(Number.Float(3.4)), Json.Array([Json.Null])]).to_string(), "[null, \"abc\", false, 5, 3.4, [null]]")
    //object tests
    assert_eq(Json.Object(#[]).to_string(), "{}")
    assert_eq(Json.Object(#["abc": Json.Null]).to_string(), "{\n\t\"abc\": null\n}")
    assert_eq(Json.Object(#[
        "abc": Json.Null,
        "def": Json.Object(#[
            "ghi": Json.Array([Json.Null, Json.String("abc"), Json.Bool(false), Json.Number(Number.Int(5)), Json.Number(Number.Float(3.4)), Json.Array([Json.Null])]),
            "jkl": Json.Object(#[
                "mno": Json.Bool(true),
                "pqr": Json.String("value"),
            ])]),
    ]).to_string(), "{\n\t\"abc\": null,\n\t\"def\": {\n\t\t\"ghi\": [null, \"abc\", false, 5, 3.4, [null]],\n\t\t\"jkl\": {\n\t\t\t\"mno\": true,\n\t\t\t\"pqr\": \"value\"\n\t\t}\n\t}\n}")
}


//bind string.to_json = (self): Json Json.String(self.copy())

// NOTE (Audit Comment):
// 1. In `string.to_json` below, the return type is `Result[Json, string]`, so:
//    - Line 118-119 (`when 't'` / `when 'f'`) and line 123 need `Result.ok(...)`
//      (e.g. `func <- Result.ok(Json.Bool(true))` and `Result.ok(out)`).
// 2. In `to_json_helper`, `str` is an `own string` parameter (immutable by default unless
//    copied to a `let` binding), and on line 137 `str[1..str.len()-2]` drops the last 2
//    characters instead of 1 (`str[1..str.len()-1]` strips only the closing quote).
bind string.to_json = (self): Result[Json, string] -> {
    let in_str = self.copy()

    in_str = in_str.trim()

    if(in_str.len() == 0) func <- Result.fail("string is empty and not valid json")

    let cur = 0

    let out = match(char(in_str[0]))
    when '{': Json.Object(#[])
    when '[': Json.Array([])
    when '"': Json.String("")
    when 't': Json.Bool(true)
    when 'f': Json.Bool(false)
    when '0'..='9': Json.Number(Number.Int(3))
    else: Json.Null

    Result.ok( own out)
}

bind _to_json_helper = (str: own string): Json -> {
    str = str.trim()

    if(str == "true") <- Json.Bool(true)
    if(str == "false") <- Json.Bool(false)
    if(str.is_int()) <- Json.Number(Number.Int(str.to_int() ?? 0))
    if(str.is_float()) <- Json.Number(Number.Float(str.to_float() ?? 0.0))

    match(char(str[0]))
    when '{': Json.Object(#[])
    when '[': Json.Array([])
    when '"': _to_json_helper(str[1..str.len()-2])
    when 't': func <- Json.Bool(true)
    when 'f': func <- Json.Bool(false)
    when '0'..='9': Json.Number(Number.Int(3))
    else: Json.Null
}

bind bool.to_json = (self): Json -> Json.Bool(self)

bind int.to_json = (self): Json -> Json.Number(Number.Int(self))

bind char.to_json = (self): Json -> Json.String(self.to_string())

bind float.to_json = (self): Json -> Json.String(self.to_string())

bind u8.to_json = (self): Json -> Json.String(self.to_string())

bind u16.to_json = (self): Json -> Json.String(self.to_string())

bind u32.to_json = (self): Json -> Json.String(self.to_string())

bind u64.to_json = (self): Json -> Json.String(self.to_string())

bind i8.to_json = (self): Json -> Json.String(self.to_string())

bind i16.to_json = (self): Json -> Json.String(self.to_string())

bind i32.to_json = (self): Json -> Json.String(self.to_string())

// bind list.to_json = (self): Json

// bind map.to_json = (self): Json
