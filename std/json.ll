//This file is completely written by hand by me. It is very much a work in progress and no decision about the language or code should at all be made based on this file. Please leave it as is. Feel free to comment on it though.

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

bind List[Json].eq = (self, other: List[Json]) bool: {
    if(self.len() != other.len()): <- false

    while(i, v in self): {
        if(self[i] != other[i]): while <- false
    } else: true
}

// bind Map[string, Json].eq = (self, other: Map[string, Json]) bool: {
//     if(self.len() != other.len()): <- false

//     while(i in self): {
//         if((not other.contains(i.key)) or i.value != other[i.key]): while <- false
//     } else: true
// }

bind Json.eq = (self, other: Json) bool: {
    match (self, other):
        when Json.Empty, Json.Empty: true
        when Json.Null, Json.Null: true
        when Json.String(s), Json.String(os): s == os
        when Json.Bool(b), Json.Bool(ob): b == ob
        when Json.Number(n), Json.Number(on): n == on
        when Json.Array(a), Json.Array(oa): a == oa
        when Json.Object(o), Json.Object(oo): {
                if(o.len() != oo.len()): <- false

                while(i in o): {
                    if((not oo.contains(i.key)) or i.value != oo[i.key]): while <- false
                } else: true
            }
        default: false


    // match (self):
    //     when Json.Empty: match (other):
    //         when Json.Empty: true
    //         default: false
    //     when Json.Null: match (other):
    //         when Json.Null: true
    //         default: false
    //     when Json.String(s): match(other):
    //         when Json.String(os): s == os
    //         default: false
    //     when Json.Bool(b): match(other):
    //         when Json.Bool(ob): b == ob
    //         default: false
    //     when Json.Number(n): match(other):
    //         when Json.Number(on): n == on
    //         default: false
    //     when Json.Array(a): match(other):
    //         when Json.Array(ao): {
    //             if(a.len() != ao.len()): <- false

    //             while(i, v in a): {
    //                 if(a[i] != ao[i]): while <- false
    //             } else: true
    //         }
    //         default: false
    //     default Json.Object(o): match(other):
    //         when Json.Object(oo): {
    //             if(o.len() != oo.len()): <- false

    //             while(i in o): {
    //                 if((not oo.contains(i.key)) or i.value != oo[i.key]): while <- false
    //             } else: true
    //         }
    //         default: false
}

define Number enum [
    Int: int,
    Float: float,
]

define Jsonable interface [
    to_json: (self) Json,
]

bind Json.to_string = (json: Json) string: {
    match (json):
        when Json.Empty: ""
        when Json.Null: "null"
        when Json.String(s): "\"${s.to_string()}\""
        when Json.Bool(b): b.to_string()
        when Json.Number(n): match (n):
            when Number.Int(i): i.to_string()
            default Number.Float(f): f.to_string()
        when Json.Array(a): {
            let out = "["

            for(i in a): out += {i.to_string() + ", "}

            if (out == "["): match <- "[]"

            assert(out.len() > 1)
            out = out[0..out.len()-2]

            out += "]"

            out
        }
        default Json.Object(o): {
            let out = "{"

            for(i in o): out += {"\n\"" + i.key.to_string() + "\": " + i.value.to_string() + ","}

            if (out == "{"): <- "{}"

            assert(out.len() > 0)
            out = out[0..out.len()-1]

            out = out.replace_all("\n", "\n\t")

            out += "\n}"

            out
        }
}

@test("test_json_to_string")
bind test_json_to_string = (): {
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
bind string.to_json = (self) Result[Json]: {
    let in_str = self.copy()

    in_str = in_str.trim()

    if(in_str.len() == 0): func <- Result.ok(Json.Empty)

    // let cur = 0

    let out = match(char(in_str[0])):
        when '{': Json.Object(#[])
        when '[': Json.Array([])
        when '"': Json.String("")
        when 't': Json.Bool(true)
        when 'f': Json.Bool(false)
        when in '0'..='9': Json.Number(Number.Int(3))
        default: Json.Null

    Result.ok( own out)
}

bind _parse_json_string = (s: &string) Result[Json]: {
    if(s.len() < 2): <- Result.fail(Error.new("Invalid Json string"))
    if(char(s[0]) != '\"'): <- Result.fail(Error.new("Json string expected to start with \""))

    let str = ""

    for(c in s.copy()): {
        println("current str: $str\ncurrent char: $c\n\n")
        s = s[1..]
        if(char(s[0]) == '\"'): {
            s = s[1..]
            println(s)
            println(str)
            func <- Result.ok(Json.String(str))
        }
        str += string(char(s[0]))
    }

    Result.fail(Error.new("Json endquote never reached"))
}

bind Number.to_json = (self) Json: Json.Number(self)

@test("tests for string.to_json")
bind string_to_json_test = (): {
    // assert_eq({`null`.to_json().value?.to_string() ?? "bad json"}.to_string(), Json.Null.to_string())

    let str = `"abc" 123`
    println("before: $str")
    println("parsed: ${_parse_json_string(&str).into_ok() ?? Json.Empty}")
    println("after:  $str")

    let a = "".to_json().into_ok()?.to_string() ?? func <-

    assert_eq({``.to_json().into_ok() ?? Json.Null}, Json.Empty)
    assert_eq({`null`.to_json().into_ok() ?? Json.Empty}, Json.Null)
    assert_eq({`"abc"`.to_json().into_ok() ?? Json.Empty}, Json.String("abc"))
    assert_eq({`5`.to_json().into_ok() ?? Json.Empty}, Json.Number(Number.Int(5)))
    assert_eq({`5.5`.to_json().into_ok() ?? Json.Empty}, Json.Number(Number.Float(5.5)))
    assert_eq({`true`.to_json().into_ok() ?? Json.Empty}, Json.Bool(true))
    assert_eq({`[]`.to_json().into_ok() ?? Json.Empty}, Json.Array([]))
    assert_eq({`["abc", 123, false]`.to_json().into_ok() ?? Json.Empty}, Json.Array([Json.String("abc"), Json.Number(Number.Int(123)), Json.Bool(false)]))
    assert_eq({`{}`.to_json().into_ok() ?? Json.Empty}, Json.Object(#[]))
    assert_eq({`{"abc": 123, "xyz": 5.3, "def": {"abc": [1, 2, 3, "abc", true]}}`.to_json().into_ok() ?? Json.Empty}, Json.Object(#[
        "abc": Json.Number(Number.Int(123)),
        "xyz": Json.Number(Number.Float(5.3)),
        "def": Json.Object(#[
            "abc": Json.Array([
                Json.Number(Number.Int(1)),
                Json.Number(Number.Int(2)),
                Json.Number(Number.Int(3)),
                Json.String("abc"),
                Json.Bool(true),
            ]),
        ]),
    ]))
}

bind _to_json_helper = (str: own string) Json: {
    str = str.trim()

    if(str == "true"): <- Json.Bool(true)
    if(str == "false"): <- Json.Bool(false)
    if(str.is_int()): <- Json.Number(Number.Int(str.to_int() ?? 0))
    if(str.is_float()): <- Json.Number(Number.Float(str.to_float() ?? 0.0))

    match(char(str[0])):
        when '{': Json.Object(#[])
        when '[': Json.Array([])
        when '"': _to_json_helper(str[1..str.len()-2])
        when 't': func <- Json.Bool(true)
        when 'f': func <- Json.Bool(false)
        when in '0'..='9': Json.Number(Number.Int(3))
        when in ['1', '2', '3']: Json.Null
        default: Json.Null
}

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
