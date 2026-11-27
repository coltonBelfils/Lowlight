package lsp

import (
	"fmt"

	"lowlight/internal/types"
)

// BuiltinFuncDoc describes a universe built-in function or raw memory intrinsic.
type BuiltinFuncDoc struct {
	Name        string
	Signature   string
	Params      []string
	ReturnType  string
	Snippet     string
	IsIntrinsic bool
	NeedsUnsafe bool
	Summary     string
}

// BuiltinMethodDoc describes a synthesized built-in method on a concrete receiver type.
type BuiltinMethodDoc struct {
	Name        string
	RecvMode    string // "self", "&self"
	Signature   string
	Params      []string
	ReturnType  string
	Snippet     string
	NeedsUnsafe bool
	Summary     string
}

// BuiltinTypeDoc describes a built-in type or interface.
type BuiltinTypeDoc struct {
	Name    string
	Kind    CompletionItemKind
	Detail  string
	Snippet string
	Summary string
}

// KeywordDoc describes a reserved Lowlight keyword and optional snippet.
type KeywordDoc struct {
	Word    string
	Detail  string
	Summary string
}

var BuiltinKeywords = []KeywordDoc{
	{Word: "let", Detail: "mutable owned local variable", Summary: "Declares a mutable owned local variable (`let x = 10` or `let v: [int] = []`)."},
	{Word: "bind", Detail: "immutable binding, function, or method", Summary: "Declares a deeply immutable value, constant, function, or method (`bind add = (a: int, b: int) int: a + b`)."},
	{Word: "const", Detail: "compile-time constant, function, or `[N: const Type]` parameter", Summary: "Declares a binding or pure function/method whose value is known at compile time (`const PI = 3.14159`, `const fn_name = fn(...) ...`, `[N: const int]`)."},
	{Word: "comptime", Detail: "compile-time evaluation, conditional, match, or loop unrolling", Summary: "Forces compile-time evaluation (`comptime expr` / `comptime { ... }`), compile-time branching (`comptime if`, `comptime match`), or loop unrolling (`comptime for`)."},
	{Word: "define", Detail: "nominal type declaration", Summary: "Defines a `struct`, `enum`, `result`, `interface`, or distinct derived type (`define UserId int`)."},
	{Word: "struct", Detail: "product value type `[field: T, ...]`", Summary: "Declares a value-type struct (`define Point struct [x: float, y: float]`). Prefix `packed` for unpadded C ABI layout."},
	{Word: "enum", Detail: "tag or tagged-union enum `[a, b, ...]`", Summary: "Declares a strongly-typed integer tag enum (`define Status enum u8 [ok = 0, err = 1]`) or tagged union."},
	{Word: "interface", Detail: "dynamic/generic method contract", Summary: "Declares an interface (`define Shape interface [area: fn(self) float]`)."},
	{Word: "if", Detail: "conditional expression / optional unwrap", Summary: "Conditional expression (`if(cond): a else: b`) or optional narrowing (`if(user?): ...` / `if(expr as val): ...`)."},
	{Word: "else", Detail: "fallback branch in `if`, `for`, or `while`", Summary: "Fallback branch for `if`, `for ... else` (runs when a loop finishes without `for <-`), or `while ... else`."},
	{Word: "for", Detail: "collection, range, or condition loop (`for(x in items)`, `for(i in 0..n)`)", Summary: "Loop construct supporting ranges (`for(i in 0..n)`), collection iteration (`for(x in items)`), condition loops, and `else` fallback branches."},
	{Word: "while", Detail: "conditional loop (`while(cond): ...`)", Summary: "Conditional loop construct supporting boolean loops (`while(cond): ...`), value-returning infinite loops (`while(true) T: ...`), and `else` branches."},
	{Word: "in", Detail: "range, collection, or containment keyword", Summary: "Separates loop bindings from ranges/collections (`for(x in items)`) or introduces containment patterns (`when in container:`) in `match`."},
	{Word: "match", Detail: "exhaustive pattern match expression", Summary: "Matches values against `when` arms (values, variants, `in container`) and a final `default` arm (`match(x):`)."},
	{Word: "when", Detail: "pattern arm in `match` or a result chain", Summary: "Introduces a pattern arm (`when 1, 2: ...` or `when in 'a'..='z': ...`) inside `match`, or a failure arm (`when Missing(key): ...`) in a result chain."},
	{Word: "next", Detail: "continue loop (`next <-`)", Summary: "Advances to the next loop iteration when paired with unwind (`next <-` or `next outer <-`)."},
	{Word: "func", Detail: "early function return (`func <- val`)", Summary: "Unwinds out of the enclosing function or lambda (`func <-` or `func <- result`)."},
	{Word: "none", Detail: "empty optional value (`?T`)", Summary: "Represents the empty state of any optional type `?T`."},
	{Word: "true", Detail: "boolean literal `true`", Summary: "Boolean truth literal (`bool`)."},
	{Word: "false", Detail: "boolean literal `false`", Summary: "Boolean falsehood literal (`bool`)."},
	{Word: "self", Detail: "method receiver value", Summary: "Refers to the receiver instance inside a method (`(self)`, `(&self)`, or `(own self)`)."},
	{Word: "Self", Detail: "implementing type inside `interface`", Summary: "Refers to the concrete implementing type inside an `interface` signature (e.g. `Copyable`)."},
	{Word: "own", Detail: "explicit ownership transfer", Summary: "Marks ownership transfer on consuming parameters (`x: own T`) and call-site moves (`consume(own buf)`)."},
	{Word: "and", Detail: "short-circuit logical AND", Summary: "Short-circuit boolean conjunction (`a and b`)."},
	{Word: "or", Detail: "short-circuit logical OR", Summary: "Short-circuit boolean disjunction (`a or b`)."},
	{Word: "not", Detail: "logical negation", Summary: "Boolean negation (`not flag`). Lowlight uses `not` instead of `!`."},
	{Word: "as", Detail: "alias or optional/result binding (`if(e as x)`)", Summary: "Binds an unwrapped optional (`if(find(id) as user)`) or a result's success value (`if(cfg! as c)`), or aliases an `import` / `extern`."},
	{Word: "unsafe", Detail: "unsafe low-level memory block", Summary: "Enables raw pointer (`ptr[T]`) dereferences, pointer arithmetic, raw intrinsics, and `extern` C calls."},
	{Word: "extern", Detail: "C11 header or ABI function import", Summary: "Includes a C header (`extern \"stdio.h\"`) or binds a C ABI function (`extern puts: fn(s: ptr[u8]) i32`)."},
	{Word: "fn", Detail: "function that captures nothing", Summary: "Starts a function literal or type that captures no variables: `bind twice = fn(x: int) int: x * 2`, `f: fn(int) int`. An `fn` value is a plain code pointer and converts to `cl` where a closure is expected."},
	{Word: "cl", Detail: "closure", Summary: "Starts a closure literal or type, which may capture variables and owns them: `cl(x: int) int: x + offset`, `f: cl(int) int`. `cl(f)` converts a function to a closure."},
	{Word: "unpack", Detail: "unpack tuple elements (`let x, y = unpack ...`)", Summary: "Unpacks a tuple or distinct tuple into comma-separated variables or assignment targets (`let x, y = unpack +[5, 7]`)."},
	{Word: "import", Detail: "import Lowlight package", Summary: "Imports exported declarations from a package directory under a required qualifier (`import \"std/math\"` -> `math.sin`)."},
}

type CodeSnippet struct {
	Label   string
	Detail  string
	Snippet string
	Summary string
}

// BuiltinSnippets are offered as completions. Each one expands to code exactly as `lowlight fmt`
// lays it out (tabs, `:` headers, indented `when` arms); TestSnippetsAreFormatted enforces this.
var BuiltinSnippets = []CodeSnippet{
	{
		Label:   "bind main",
		Detail:  "Program entry point",
		Snippet: "bind main = fn(): {\n\t$0\n}",
		Summary: "Defines the Lowlight entry point function `main`.",
	},
	{
		Label:   "bind func",
		Detail:  "Define a top-level function",
		Snippet: "bind ${1:name} = fn(${2:x: int}) ${3:int}: {\n\t$0\n}",
		Summary: "Defines a top-level function with a block body.",
	},
	{
		Label:   "bind func expr",
		Detail:  "Define a single-expression function",
		Snippet: "bind ${1:name} = fn(${2:x: int}) ${3:int}: ${0:x}",
		Summary: "Defines a top-level function whose body is a single expression.",
	},
	{
		Label:   "bind method",
		Detail:  "Define a method on a type",
		Snippet: "bind ${1:Type}.${2:name} = fn(${3:self}) ${4:int}: {\n\t$0\n}",
		Summary: "Defines a method on a type. Use `&self` to mutate the receiver or `own self` to consume it.",
	},
	{
		Label:   "bind const",
		Detail:  "Define a typed constant",
		Snippet: "bind ${1:NAME}: ${2:int} = ${0:0}",
		Summary: "Defines a constant value (`SCREAMING_SNAKE_CASE` at top level).",
	},
	{
		Label:   "bind drop",
		Detail:  "Define a destructor (`drop`) on a type",
		Snippet: "bind ${1:Type}.drop = fn(&self): {\n\t$0\n}",
		Summary: "Defines custom cleanup logic called automatically when a value leaves scope.",
	},
	{
		Label:   "extern fn",
		Detail:  "Declare a C11 extern function",
		Snippet: "extern ${1:c_func}: fn(${2:x: i32}) ${3:i32}",
		Summary: "Declares a C11 ABI function callable inside `unsafe`.",
	},
	{
		Label:   "fn lambda",
		Detail:  "Function value that captures nothing",
		Snippet: "fn(${1:x: int}) ${2:int}: ${0:x}",
		Summary: "A function literal with no captures: a plain code pointer that copies freely and converts to a closure where one is expected.",
	},
	{
		Label:   "cl closure",
		Detail:  "Closure that captures variables",
		Snippet: "cl(${1:x: int}) ${2:int}: ${0:x}",
		Summary: "A closure literal. It owns the variables it captures (plain ones are copied, resources moved in).",
	},
	{
		Label:   "derive struct",
		Detail:  "Struct with derived methods",
		Snippet: "@derive(${1:Equatable, Printable})\ndefine ${2:Name} struct [\n\t${3:x}: ${4:int},\n\t${5:y}: ${6:int},\n]",
		Summary: "Defines a struct and asks `@derive` to write `eq`, `cmp`, `hash`, `copy`, or `to_string` for it.",
	},
	{
		Label:   "define struct",
		Detail:  "Define a value struct",
		Snippet: "define ${1:Name} struct [\n\t${2:x}: ${3:int},\n\t${4:y}: ${5:int},\n]",
		Summary: "Defines a nominal value struct with automatic deterministic cleanup. Add `@derive(...)` for `==`, printing, hashing, ordering, or `.copy()`.",
	},
	{
		Label:   "define packed struct",
		Detail:  "Define a C-ABI packed struct",
		Snippet: "@packed\ndefine ${1:Header} struct [\n\t${2:tag}: ${3:u8},\n\t${4:len}: ${5:u32},\n]",
		Summary: "Defines a struct laid out without padding, matching the C ABI.",
	},
	{
		Label:   "define distinct",
		Detail:  "Define a distinct newtype wrapper",
		Snippet: "define ${1:UserId} ${2:int}",
		Summary: "Defines a distinct nominal type wrapping an existing type with zero runtime overhead.",
	},
	{
		Label:   "define enum",
		Detail:  "Define a tag enum",
		Snippet: "define ${1:Status} enum [\n\t${2:Active},\n\t${3:Stopped},\n]",
		Summary: "Defines a strongly-typed integer tag enum.",
	},
	{
		Label:   "define union enum",
		Detail:  "Define a tagged union enum with payloads",
		Snippet: "define ${1:Value} enum [\n\t${2:Empty},\n\t${3:Flag}: bool,\n\t${4:Number}: float,\n\t${5:Text}: string,\n]",
		Summary: "Defines a sum type / tagged union enum carrying typed data payloads.",
	},
	{
		Label:   "define result",
		Detail:  "Define a result type",
		Snippet: "define ${1:Lookup} result [\n\tvalue: ${2:string},\n\t${3:Missing}: ${4:string},\n\t${5:Forbidden},\n]",
		Summary: "Defines a result type: one `value` member plus failure variants. Names holding one end in `!`.",
	},
	{
		Label:   "define interface",
		Detail:  "Define an interface",
		Snippet: "define ${1:Name} interface [\n\t${2:method}: fn(self) ${3:string}\n]",
		Summary: "Defines a structural interface for zero-allocation borrows or owned heap boxes.",
	},
	{
		Label:   "if else",
		Detail:  "Conditional if/else expression",
		Snippet: "if(${1:cond}): ${2:a} else: ${0:b}",
		Summary: "Canonical single-expression conditional branch.",
	},
	{
		Label:   "if?",
		Detail:  "Unwrap optional in if-block",
		Snippet: "if(${1:opt}?): {\n\t$0\n}",
		Summary: "Narrows an optional variable `opt?` (`?T`) to non-optional `opt` (`T`) inside the `then` branch.",
	},
	{
		Label:   "if as",
		Detail:  "Unwrap an optional or result into a name",
		Snippet: "if(${1:expr} as ${2:v}): ${3:v} else: ${0:fallback}",
		Summary: "Binds the value of an optional (`?T`) or the success value of a result (`r!`) when present.",
	},
	{
		Label:   "if as block",
		Detail:  "Unwrap an optional or result into a block",
		Snippet: "if(${1:expr} as ${2:v}): {\n\t$0\n}",
		Summary: "Binds the value of an optional (`?T`) or result (`r!`) inside an `if` block.",
	},
	{
		Label:   "for in",
		Detail:  "Iterate range or collection",
		Snippet: "for(${1:item} in ${2:items}): {\n\t$0\n}",
		Summary: "Iterates over a range (`0..n`) or collection (`[T]`, `[N: T]`, `#[T]`, `#[K: V]`, `string`).",
	},
	{
		Label:   "for index",
		Detail:  "Iterate with index and item",
		Snippet: "for(${1:i}, ${2:item} in ${3:items}): {\n\t$0\n}",
		Summary: "Iterates a collection with each element's index (`for(i, x in list)`), or a map's keys and values (`for(k, v in map)`).",
	},
	{
		Label:   "for collect",
		Detail:  "Collect elements from a for comprehension",
		Snippet: "bind ${1:items} = for(${2:x} in ${3:xs}): ${0:x}",
		Summary: "Builds a `[T]` list (or `#[T]` / `#[K: V]` when annotated) from a `for` expression.",
	},
	{
		Label:   "while",
		Detail:  "Conditional while loop",
		Snippet: "while(${1:cond}): {\n\t$0\n}",
		Summary: "Executes a loop while a condition evaluates to true.",
	},
	{
		Label:   "while search",
		Detail:  "Search loop yielding ?T",
		Snippet: "bind ${1:found?} = while(${2:x} in ${3:xs}): if(${4:cond}): while <- $2",
		Summary: "Searches a range or collection, unwinding with `while <- val` on match (produces `?T`).",
	},
	{
		Label:   "match when",
		Detail:  "Exhaustive pattern match",
		Snippet: "match(${1:val}):\n\twhen ${2:pattern}: ${3:expr}\n\tdefault: ${0:expr}",
		Summary: "Exhaustively matches a scrutinee across `when` arms and a `default` arm.",
	},
	{
		Label:   "match enum",
		Detail:  "Exhaustively match enum variants",
		Snippet: "match(${1:val}):\n\twhen ${2:variant}: ${3:expr}\n\tdefault: ${0:panic(\"todo\")}",
		Summary: "Exhaustively matches over enum variants.",
	},
	{
		Label:   "result chain",
		Detail:  "Handle a result's failures, propagating `err`",
		Snippet: "${1:load()}:\n\tdefault err(${2:e}): func <- err($2.wrap(\"${3:context}\"))",
		Summary: "Evaluates to the result's `value`; on failure, returns it from the enclosing function with added context.",
	},
	{
		Label:   "result chain when",
		Detail:  "Handle specific failures of a result",
		Snippet: "${1:find(key)}:\n\twhen ${2:missing}(${3:_}): ${4:fallback}\n\tdefault panic",
		Summary: "Handles listed failures (each arm unwinds or evaluates to the value type); `default panic` panics on the rest.",
	},
	{
		Label:   "default panic",
		Detail:  "Unwrap a result, panicking on failure",
		Snippet: "${1:load()}: default panic",
		Summary: "Evaluates to the result's `value` and panics with `unhandled failure Name.variant` on any failure.",
	},
	{
		Label:   "test",
		Detail:  "Define a unit test",
		Snippet: "@test(\"${1:description}\")\nbind test_${2:name} = fn(): {\n\t$0\n}",
		Summary: "Defines a test function runnable via `lowlight test`.",
	},
	{
		Label:   "unsafe",
		Detail:  "Unsafe block for raw pointers and extern C calls",
		Snippet: "unsafe {\n\t$0\n}",
		Summary: "Permits raw pointer dereferencing, pointer arithmetic, and `extern` C calls.",
	},
}

var BuiltinTypes = []BuiltinTypeDoc{
	{Name: "int", Kind: KindStruct, Detail: "64-bit signed integer (i64)", Summary: "Default 64-bit signed two's-complement integer (`int64_t` in C). Plain value type."},
	{Name: "i8", Kind: KindStruct, Detail: "8-bit signed integer", Summary: "8-bit signed integer (`int8_t`, -128..127). Plain value type."},
	{Name: "i16", Kind: KindStruct, Detail: "16-bit signed integer", Summary: "16-bit signed integer (`int16_t`). Plain value type."},
	{Name: "i32", Kind: KindStruct, Detail: "32-bit signed integer", Summary: "32-bit signed integer (`int32_t`). Plain value type."},
	{Name: "i64", Kind: KindStruct, Detail: "64-bit signed integer (alias of int)", Summary: "Explicit width name for `int` (`int64_t`). Plain value type."},
	{Name: "u8", Kind: KindStruct, Detail: "8-bit unsigned integer (byte)", Summary: "8-bit unsigned byte (`uint8_t`, 0..255). Plain value type."},
	{Name: "u16", Kind: KindStruct, Detail: "16-bit unsigned integer", Summary: "16-bit unsigned integer (`uint16_t`). Plain value type."},
	{Name: "u32", Kind: KindStruct, Detail: "32-bit unsigned integer", Summary: "32-bit unsigned integer (`uint32_t`). Plain value type."},
	{Name: "u64", Kind: KindStruct, Detail: "64-bit unsigned integer", Summary: "64-bit unsigned integer (`uint64_t`). Plain value type."},
	{Name: "float", Kind: KindStruct, Detail: "64-bit IEEE-754 float (f64)", Summary: "Default 64-bit IEEE-754 floating-point number (`double` in C). Plain value type."},
	{Name: "f32", Kind: KindStruct, Detail: "32-bit IEEE-754 float", Summary: "32-bit single-precision IEEE-754 float (`float` in C). Plain value type."},
	{Name: "f64", Kind: KindStruct, Detail: "64-bit IEEE-754 float (alias of float)", Summary: "Explicit width name for `float` (`double` in C). Plain value type."},
	{Name: "bool", Kind: KindStruct, Detail: "boolean (`true` or `false`)", Summary: "Boolean value (`true` or `false`). Plain value type."},
	{Name: "char", Kind: KindStruct, Detail: "Unicode scalar value (32-bit rune)", Summary: "32-bit Unicode scalar value (`'a'`, `'\\n'`, `'\\u{1F680}'`). Plain value type."},
	{Name: "string", Kind: KindStruct, Detail: "UTF-8 string (Resource / zero-copy view)", Summary: "Owned heap UTF-8 string (or zero-allocation borrowed view when passed read-only). Automatically freed at scope exit."},
	{Name: "ptr", Kind: KindStruct, Detail: "ptr[T] — raw pointer", Snippet: "ptr[${1:u8}]", Summary: "Raw pointer `ptr[T]` for manual memory and C FFI. Copyable Plain address; dereferencing (`*p`, `p[i]`) requires `unsafe`."},
	{Name: "ref", Kind: KindStruct, Detail: "ref[T] — safe arena reference", Snippet: "ref[${1:Node}]", Summary: "Non-null, plain-copyable reference `ref[T]` allocated inside an `Arena`. Statically verified never to outlive its `Arena`."},
	{Name: "Printable", Kind: KindInterface, Detail: "interface `[to_string: fn(self) string]`", Summary: "Interpolation and `println`. Primitives, strings, tuples, and collections of `Printable` implement it; structs and enums do with a `to_string` method or `@derive(Printable)`."},
	{Name: "Copyable", Kind: KindInterface, Detail: "generic bound `[copy: fn(self) Self]`", Summary: "`.copy()`. Plain types, strings, and tuples and collections of `Copyable` implement it; a non-plain struct or enum does with a `copy` method or `@derive(Copyable)`."},
	{Name: "Drop", Kind: KindInterface, Detail: "custom destructor `[drop: fn(&self)]`", Summary: "Implemented via `bind MyType.drop = fn(&self): ...` to run custom cleanup deterministically before fields are freed."},
	{Name: "Equatable", Kind: KindInterface, Detail: "generic bound `[eq: fn(self, other: Self) bool]`", Summary: "`==` and `!=`. Numbers, `bool`, `char`, strings, optionals, and tuples of `Equatable` implement it; structs and enums do with an `eq` (or `cmp`) method or `@derive(Equatable)`. Lists, sets, and maps have no `==`."},
	{Name: "Hashable", Kind: KindInterface, Detail: "generic bound `[hash: fn(self) u64]`", Summary: "Set elements and map keys (which must also be `Equatable`). Integers, `bool`, `char`, strings, pointers, and tuples of `Hashable` implement it; structs and enums do with a `hash` method or `@derive(Hashable)`."},
	{Name: "Comparable", Kind: KindInterface, Detail: "ordering bound `[cmp: fn(self, other: Self) int]`", Summary: "`<`, `<=`, `>`, `>=`, and sorting; `cmp` returns a negative number, zero, or a positive number. Numbers, `char`, strings, and tuples of `Comparable` implement it; structs and enums do with a `cmp` method or `@derive(Comparable)`."},
	{Name: "Addable", Kind: KindInterface, Detail: "operator bound `[add: fn(self, other: Self) Self]`", Summary: "`a + b` on anything but a number calls `a.add(b)`. `string` implements it (concatenation), as do the numeric types, so generic code can require `T: Addable`."},
	{Name: "AddAssignable", Kind: KindInterface, Detail: "operator bound `[add_assign: fn(&self, other: Self)]`", Summary: "`a += b` calls `a.add_assign(b)`; a type with only `add` gets `a = a.add(b)`. `string` implements it (appending in place)."},
	{Name: "comp.TARGET", Kind: KindVariable, Detail: "comp.Target (@comptime_only)", Summary: "Compile-time target descriptor (`os`, `arch`, `endian`, `is_unix`, `ptr_size`, `ptr_bits`, `triple`) for the active `--target`."},
	{Name: "comp.Target", Kind: KindStruct, Detail: "compile-time target info struct (@comptime_only)", Summary: "Target descriptor struct holding `os: TargetOs`, `arch: TargetArch`, `endian: Endian`, `is_unix: bool`, `ptr_size: int`, `ptr_bits: int`, and `triple: string`."},
	{Name: "comp.TargetOs", Kind: KindEnum, Detail: "compile-time OS enum (@comptime_only)", Summary: "Target operating system variants (`darwin`, `linux`, `windows`, `freebsd`, `openbsd`, `netbsd`, `wasm`, `unknown`)."},
	{Name: "comp.TargetArch", Kind: KindEnum, Detail: "compile-time CPU architecture enum (@comptime_only)", Summary: "Target CPU architecture variants (`amd64`, `arm64`, `x86`, `riscv64`, `wasm32`, `wasm64`, `unknown`)."},
	{Name: "comp.Endian", Kind: KindEnum, Detail: "compile-time endianness enum (@comptime_only)", Summary: "Target byte order (`little`, `big`)."},
	{Name: "comp.TypeKind", Kind: KindEnum, Detail: "compile-time type classification enum (@comptime_only)", Summary: "Compile-time type classification variants (`primitive`, `struct_kind`, `enum_kind`, `result_kind`, `distinct_kind`, `interface_kind`, `tuple`, `array`, `list`, `map`, `set`, `optional`, `ptr`, `ref`, `func`, `range`)."},
	{Name: "comp.AttrInfo", Kind: KindStruct, Detail: "compile-time attribute descriptor (@comptime_only)", Summary: "Attribute descriptor returned by `comp.attrs_of` (`name: string`, `args: [string]`)."},
	{Name: "comp.FieldInfo", Kind: KindStruct, Detail: "compile-time struct field descriptor (@comptime_only)", Summary: "Struct field descriptor returned by `comp.fields_of` (`name: string`, `type_name: string`, `index: int`, `is_optional: bool`, `attrs: [AttrInfo]`, `.field(v)`, `.get(v)`, `.set(&v, val)`)."},
	{Name: "comp.VariantInfo", Kind: KindStruct, Detail: "compile-time enum variant descriptor (@comptime_only)", Summary: "Enum variant descriptor returned by `comp.variants_of` (`name: string`, `index: int`, `has_payload: bool`, `payload_type_name: string`, `attrs: [AttrInfo]`, `.is_variant(v)`, `.payload(v)`, `.as_variant(v)`)."},
	{Name: "comp.ElemInfo", Kind: KindStruct, Detail: "compile-time sequence element descriptor (@comptime_only)", Summary: "Tuple/array element descriptor returned by `comp.elems_of` (`index: int`, `type_name: string`, `.elem(v)`)."},
	{Name: "comp.DistinctInfo", Kind: KindStruct, Detail: "compile-time distinct type descriptor (@comptime_only)", Summary: "Distinct type descriptor returned by `comp.underlying_of` (`type_name: string`, `.implements(I)`). Call `.underlying()` directly on distinct values."},
}

var BuiltinFunctions = []BuiltinFuncDoc{
	{
		Name:       "println",
		Signature:  "println(val: Printable)",
		Params:     []string{"val: Printable"},
		ReturnType: "void",
		Snippet:    "println($1)",
		Summary:    "Prints `val` (or an empty line if called with 0 arguments) to stdout followed by `\\n`.",
	},
	{
		Name:       "print",
		Signature:  "print(val: Printable)",
		Params:     []string{"val: Printable"},
		ReturnType: "void",
		Snippet:    "print($1)",
		Summary:    "Prints `val` to stdout without a trailing newline.",
	},
	{
		Name:       "eprintln",
		Signature:  "eprintln(val: Printable)",
		Params:     []string{"val: Printable"},
		ReturnType: "void",
		Snippet:    "eprintln($1)",
		Summary:    "Prints `val` (or an empty line if called with 0 arguments) to stderr followed by `\\n`.",
	},
	{
		Name:       "eprint",
		Signature:  "eprint(val: Printable)",
		Params:     []string{"val: Printable"},
		ReturnType: "void",
		Snippet:    "eprint($1)",
		Summary:    "Prints `val` to stderr without a trailing newline.",
	},
	{
		Name:       "assert",
		Signature:  "assert(cond: bool, msg: string = ...)",
		Params:     []string{"cond: bool", "msg: string"},
		ReturnType: "void",
		Snippet:    "assert($1)",
		Summary:    "Asserts `cond` at runtime.\n\n- `cond`: Condition expected to evaluate to `true`.\n- `msg`: Optional custom failure diagnostic message.\n\n# Panics\nAborts with file/line diagnostic when `cond` is `false`.",
	},
	{
		Name:       "panic",
		Signature:  "panic(msg: string | Error) never",
		Params:     []string{"msg: string | Error"},
		ReturnType: "never",
		Snippet:    "panic($1)",
		Summary:    "Aborts the process with a fatal error (`never` return type).\n\n- `msg`: A `string` message, or an `Error` (printed with its full chain and trace).\n\nStandard output is flushed first. Panics cannot be caught and no `Drop` runs; return `Result[T]` for failures a caller can handle.",
	},
	{
		Name:       "caller_location",
		Signature:  "caller_location() string",
		Params:     []string{},
		ReturnType: "string",
		Snippet:    "caller_location()",
		Summary:    "Returns the caller's source location as `file:line:col` (tracks caller across `@track_caller` boundaries).",
	},
	{
		Name:       "caller_file",
		Signature:  "caller_file() string",
		Params:     []string{},
		ReturnType: "string",
		Snippet:    "caller_file()",
		Summary:    "Returns the caller's source file name.",
	},
	{
		Name:       "caller_line",
		Signature:  "caller_line() int",
		Params:     []string{},
		ReturnType: "int",
		Snippet:    "caller_line()",
		Summary:    "Returns the caller's 1-based source line number.",
	},
	{
		Name:       "caller_col",
		Signature:  "caller_col() int",
		Params:     []string{},
		ReturnType: "int",
		Snippet:    "caller_col()",
		Summary:    "Returns the caller's 1-based source column number.",
	},
	{
		Name:       "exit",
		Signature:  "exit(code: int) never",
		Params:     []string{"code: int"},
		ReturnType: "never",
		Snippet:    "exit(${1:0})",
		Summary:    "Terminates the process with status `code` (`never` type).",
	},
	{
		Name:       "parse_int",
		Signature:  "parse_int(s: string) ?int",
		Params:     []string{"s: string"},
		ReturnType: "?int",
		Snippet:    "parse_int($1)",
		Summary:    "Parses a signed 64-bit integer from `s`, returning `none` on invalid syntax or overflow.",
	},
	{
		Name:       "parse_float",
		Signature:  "parse_float(s: string) ?float",
		Params:     []string{"s: string"},
		ReturnType: "?float",
		Snippet:    "parse_float($1)",
		Summary:    "Parses a 64-bit floating-point number from `s`, returning `none` if invalid.",
	},
	{
		Name:       "abs",
		Signature:  "abs(x: Numeric) Numeric",
		Params:     []string{"x: Numeric"},
		ReturnType: "Numeric",
		Snippet:    "abs($1)",
		Summary:    "Returns the absolute value of any signed integer or floating-point number.",
	},
	{
		Name:       "min",
		Signature:  "min(a: Numeric, b: Numeric) Numeric",
		Params:     []string{"a: Numeric", "b: Numeric"},
		ReturnType: "Numeric",
		Snippet:    "min($1, $2)",
		Summary:    "Returns the smaller of two matching numeric values.",
	},
	{
		Name:       "max",
		Signature:  "max(a: Numeric, b: Numeric) Numeric",
		Params:     []string{"a: Numeric", "b: Numeric"},
		ReturnType: "Numeric",
		Snippet:    "max($1, $2)",
		Summary:    "Returns the larger of two matching numeric values.",
	},
	{
		Name:       "args",
		Signature:  "args() [string]",
		Params:     nil,
		ReturnType: "[string]",
		Snippet:    "args()",
		Summary:    "Returns command-line arguments (`argv`) as an owned `[string]` vector.",
	},

	// Compile-time & Type-layout namespaced intrinsics
	{
		Name:        "comp.compile_error",
		Signature:   "comp.compile_error(msg: string) never",
		Params:      []string{"msg: string"},
		ReturnType:  "never",
		Snippet:     "comp.compile_error(\"${1:error message}\")",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Emits a compile-time error diagnostic with `msg` and aborts compile-time evaluation. `@comptime_only`.",
	},
	{
		Name:        "comp.log",
		Signature:   "comp.log(val: Printable)",
		Params:      []string{"val: Printable"},
		ReturnType:  "void",
		Snippet:     "comp.log(${1:val})",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Emits an informational compile-time note diagnostic showing `val` during evaluation. `@comptime_only`.",
	},
	{
		Name:        "comp.embed_str",
		Signature:   "comp.embed_str(path: string) string",
		Params:      []string{"path: string"},
		ReturnType:  "string",
		Snippet:     "comp.embed_str(\"${1:path}\")",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Reads a UTF-8 file relative to the declaring source file at compile time and embeds it into static `.rodata` (`cap == 0`). `@comptime_only`.",
	},
	{
		Name:        "comp.embed_bytes",
		Signature:   "comp.embed_bytes(path: string) [u8]",
		Params:      []string{"path: string"},
		ReturnType:  "[u8]",
		Snippet:     "comp.embed_bytes(\"${1:path}\")",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Reads a binary file relative to the declaring source file at compile time and embeds it into static `.rodata` (`cap == 0`). `@comptime_only`.",
	},
	{
		Name:        "comp.type_name",
		Signature:   "comp.type_name[T]() string",
		Params:      nil,
		ReturnType:  "string",
		Snippet:     "comp.type_name[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns the canonical display name of type `T` at compile time. `@comptime_only`.",
	},
	{
		Name:        "comp.kind_of",
		Signature:   "comp.kind_of[T]() comp.TypeKind",
		Params:      nil,
		ReturnType:  "comp.TypeKind",
		Snippet:     "comp.kind_of[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns the `comp.TypeKind` variant describing type `T` at compile time. `@comptime_only`.",
	},
	{
		Name:        "comp.fields_of",
		Signature:   "comp.fields_of[T]() [comp.FieldInfo]",
		Params:      nil,
		ReturnType:  "[comp.FieldInfo]",
		Snippet:     "comp.fields_of[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns static field descriptors (`FieldInfo`) for struct type `T`. `@comptime_only`.",
	},
	{
		Name:        "comp.variants_of",
		Signature:   "comp.variants_of[T]() [comp.VariantInfo]",
		Params:      nil,
		ReturnType:  "[comp.VariantInfo]",
		Snippet:     "comp.variants_of[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns static variant descriptors (`VariantInfo`) for enum/result type `T`. `@comptime_only`.",
	},
	{
		Name:        "comp.elems_of",
		Signature:   "comp.elems_of[T]() [comp.ElemInfo]",
		Params:      nil,
		ReturnType:  "[comp.ElemInfo]",
		Snippet:     "comp.elems_of[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns static element descriptors (`ElemInfo`) for tuple or fixed array type `T`. `@comptime_only`.",
	},
	{
		Name:        "comp.underlying_of",
		Signature:   "comp.underlying_of[D]() comp.DistinctInfo",
		Params:      nil,
		ReturnType:  "comp.DistinctInfo",
		Snippet:     "comp.underlying_of[${1:D}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns a `DistinctInfo` descriptor for distinct type `D` (`type_name`, `.implements(I)`). `@comptime_only`.",
	},
	{
		Name:        "comp.attrs_of",
		Signature:   "comp.attrs_of[T]() [comp.AttrInfo]",
		Params:      nil,
		ReturnType:  "[comp.AttrInfo]",
		Snippet:     "comp.attrs_of[${1:T}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Returns the custom attribute descriptors (`AttrInfo`) attached to type `T`. `@comptime_only`.",
	},
	{
		Name:        "comp.implements",
		Signature:   "comp.implements[T, I]() bool",
		Params:      nil,
		ReturnType:  "bool",
		Snippet:     "comp.implements[${1:T}, ${2:Equatable}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time predicate reporting whether type `T` implements interface `I`.",
	},
	{
		Name:        "type.size_of",
		Signature:   "type.size_of[T]() int",
		Params:      nil,
		ReturnType:  "int",
		Snippet:     "type.size_of[${1:int}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time byte size (`sizeof(T)`) of type `T`. Safe to call anywhere.",
	},
	{
		Name:        "type.align_of",
		Signature:   "type.align_of[T]() int",
		Params:      nil,
		ReturnType:  "int",
		Snippet:     "type.align_of[${1:int}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time byte alignment (`_Alignof(T)`) of type `T`. Safe to call anywhere.",
	},
	{
		Name:        "type.is_plain",
		Signature:   "type.is_plain[T]() bool",
		Params:      nil,
		ReturnType:  "bool",
		Snippet:     "type.is_plain[${1:int}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time predicate reporting whether type `T` is Plain (implicitly bitwise-copied with no `Drop`). Safe to call anywhere.",
	},
	{
		Name:        "alloc_raw",
		Signature:   "alloc_raw[T](count: int) ptr[T]",
		Params:      []string{"count: int"},
		ReturnType:  "ptr[T]",
		Snippet:     "alloc_raw[${1:u8}](${2:count})",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Allocates zero-initialized raw heap memory for `count` elements of `T`. Requires `unsafe`.",
	},
	{
		Name:        "alloc_uninit",
		Signature:   "alloc_uninit[T](count: int) ptr[T]",
		Params:      []string{"count: int"},
		ReturnType:  "ptr[T]",
		Snippet:     "alloc_uninit[${1:u8}](${2:count})",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Allocates uninitialized (`malloc`) raw heap memory for `count` elements of `T`. Requires `unsafe`.",
	},
	{
		Name:        "realloc_raw",
		Signature:   "realloc_raw(p: ptr[T], count: int) ptr[T]",
		Params:      []string{"p: ptr[T]", "count: int"},
		ReturnType:  "ptr[T]",
		Snippet:     "realloc_raw($1, $2)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Resizes a raw allocation `p` to hold `count` elements of `T`. Requires `unsafe`.",
	},
	{
		Name:        "free_raw",
		Signature:   "free_raw(p: ptr[T])",
		Params:      []string{"p: ptr[T]"},
		ReturnType:  "void",
		Snippet:     "free_raw($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Frees raw heap memory previously allocated with `alloc_raw`. Requires `unsafe`.",
	},
	{
		Name:        "null_ptr",
		Signature:   "null_ptr[T]() ptr[T]",
		Params:      nil,
		ReturnType:  "ptr[T]",
		Snippet:     "null_ptr[${1:u8}]()",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Returns a null raw pointer (`NULL`) of type `ptr[T]`. Requires `unsafe`.",
	},
	{
		Name:        "is_null",
		Signature:   "is_null(p: ptr[T]) bool",
		Params:      []string{"p: ptr[T]"},
		ReturnType:  "bool",
		Snippet:     "is_null($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Returns `true` if raw pointer `p` is null (`NULL`). Requires `unsafe`.",
	},
	{
		Name:        "addr_of",
		Signature:   "addr_of(place: T) ptr[T]",
		Params:      []string{"place: T"},
		ReturnType:  "ptr[T]",
		Snippet:     "addr_of($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Takes the raw address (`&place`) of an lvalue variable, struct field, or element. Requires `unsafe`.",
	},
	{
		Name:        "ptr_add",
		Signature:   "ptr_add(p: ptr[T], offset: int) ptr[T]",
		Params:      []string{"p: ptr[T]", "offset: int"},
		ReturnType:  "ptr[T]",
		Snippet:     "ptr_add($1, $2)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Advances raw pointer `p` by `offset` elements (`p + offset`). Requires `unsafe`.",
	},
	{
		Name:        "ptr_read",
		Signature:   "ptr_read(p: ptr[T]) T",
		Params:      []string{"p: ptr[T]"},
		ReturnType:  "T",
		Snippet:     "ptr_read($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Reads the value of type `T` at raw pointer `p`. Requires `unsafe`.",
	},
	{
		Name:        "ptr_write",
		Signature:   "ptr_write(p: ptr[T], val: own T)",
		Params:      []string{"p: ptr[T]", "val: own T"},
		ReturnType:  "void",
		Snippet:     "ptr_write($1, $2)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Writes `val` to raw memory at `p` without dropping any prior uninitialized bytes. Requires `unsafe`.",
	},
	{
		Name:        "ptr_cast",
		Signature:   "ptr_cast[To](src: ptr[From] | int) ptr[To]",
		Params:      []string{"src: ptr[From]"},
		ReturnType:  "ptr[To]",
		Snippet:     "ptr_cast[${1:u8}]($2)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Reinterprets a raw pointer or integer address as `ptr[To]`. Requires `unsafe`.",
	},
	{
		Name:        "syscall",
		Signature:   "syscall(number: int, a1: int, ..., a6: int) int",
		Params:      []string{"number: int", "a1: int", "a2: int", "a3: int", "a4: int", "a5: int", "a6: int"},
		ReturnType:  "int",
		Snippet:     "syscall($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Makes system call `number` with up to six integer arguments, using the target's system call instruction. Returns the result, or `-errno` on failure on every platform. Call numbers differ by OS and architecture, and macOS doesn't promise them stable. Requires `unsafe`.",
	},
	{
		Name:        "ref_from_ptr",
		Signature:   "ref_from_ptr(p: ptr[T]) ref[T]",
		Params:      []string{"p: ptr[T]"},
		ReturnType:  "ref[T]",
		Snippet:     "ref_from_ptr($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "The `ref[T]` for the value at `p`. std/core's `Arena` uses it to hand out references to memory it owns; the caller guarantees the memory outlives the reference. Requires `unsafe`.",
	},
	{
		Name:        "ptr_addr",
		Signature:   "ptr_addr(p: ptr[T]) u64",
		Params:      []string{"p: ptr[T]"},
		ReturnType:  "u64",
		Snippet:     "ptr_addr($1)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Converts raw pointer `p` into its numeric `u64` (`uintptr_t`) address. Requires `unsafe`.",
	},
	{
		Name:        "copy_bytes",
		Signature:   "copy_bytes(dst: ptr[u8], src: ptr[u8], count: int)",
		Params:      []string{"dst: ptr[u8]", "src: ptr[u8]", "count: int"},
		ReturnType:  "void",
		Snippet:     "copy_bytes($1, $2, $3)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Copies `count` raw bytes from `src` to `dst` (`memmove`-safe). Requires `unsafe`.",
	},
	{
		Name:        "zero_bytes",
		Signature:   "zero_bytes(dst: ptr[u8], count: int)",
		Params:      []string{"dst: ptr[u8]", "count: int"},
		ReturnType:  "void",
		Snippet:     "zero_bytes($1, $2)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Zero-fills `count` raw bytes starting at `dst` (`memset(dst, 0, count)`). Requires `unsafe`.",
	},
	{
		Name:        "fill_bytes",
		Signature:   "fill_bytes(dst: ptr[u8], val: u8, count: int)",
		Params:      []string{"dst: ptr[u8]", "val: u8", "count: int"},
		ReturnType:  "void",
		Snippet:     "fill_bytes($1, $2, $3)",
		IsIntrinsic: true,
		NeedsUnsafe: true,
		Summary:     "Fills `count` raw bytes starting at `dst` with byte `val` (`memset(dst, val, count)`). Requires `unsafe`.",
	},
}

func LookupBuiltinFunc(name string) *BuiltinFuncDoc {
	for i := range BuiltinFunctions {
		n := BuiltinFunctions[i].Name
		if n == name || n == "type."+name || n == "comp."+name {
			return &BuiltinFunctions[i]
		}
	}
	return nil
}

func LookupBuiltinTypeDoc(name string) *BuiltinTypeDoc {
	for i := range BuiltinTypes {
		if BuiltinTypes[i].Name == name {
			return &BuiltinTypes[i]
		}
	}
	return nil
}

func LookupKeywordDoc(word string) *KeywordDoc {
	for i := range BuiltinKeywords {
		if BuiltinKeywords[i].Word == word {
			return &BuiltinKeywords[i]
		}
	}
	return nil
}

// BuiltinMethodsForType returns all compiler-provided methods available on `recvTy`
// (with concrete element/key/value types substituted into signatures).
func BuiltinMethodsForType(recvTy types.Type, isOptChain bool) []BuiltinMethodDoc {
	if recvTy == nil || recvTy == types.TypeInvalid || recvTy == types.TypeVoid {
		return nil
	}
	wrapRet := func(ret string) string {
		if !isOptChain || ret == "void" {
			return ret
		}
		if len(ret) > 0 && ret[0] == '?' {
			return ret
		}
		return "?" + ret
	}

	var out []BuiltinMethodDoc
	add := func(m BuiltinMethodDoc) {
		if isOptChain && m.RecvMode == "&self" {
			// Mutating methods cannot be called on `?.` optional chains
			return
		}
		m.ReturnType = wrapRet(m.ReturnType)
		out = append(out, m)
	}

	// `char` methods are ordinary Lowlight in std/core and std/chars; their docs come from there.

	// `string` methods the compiler provides; the rest are Lowlight in std/core and std/strings.
	if recvTy.Equals(types.TypeString) {
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: "Returns the UTF-8 byte length of the string in O(1)."})
		add(BuiltinMethodDoc{Name: "byte_at", RecvMode: "self", Signature: "(self, idx: int) u8", Params: []string{"idx: int"}, ReturnType: "u8", Snippet: "byte_at($1)", Summary: "Returns byte at `idx` (panics if out of bounds)."})
		add(BuiltinMethodDoc{Name: "c_str", RecvMode: "self", Signature: "(self) ptr[u8]", ReturnType: "ptr[u8]", Snippet: "c_str()", NeedsUnsafe: true, Summary: "Returns NUL-terminated `ptr[u8]` pointer for C FFI. Requires `unsafe`."})
	}

	// `[T]` Vector methods
	if vec, ok := recvTy.(*types.Vector); ok {
		el := vec.Elem.String()
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: "Returns the number of elements in the vector."})
		add(BuiltinMethodDoc{Name: "cap", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "cap()", Summary: "Returns the allocated capacity of the vector."})
		add(BuiltinMethodDoc{Name: "is_empty", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_empty()", Summary: "Returns `true` if `.len() == 0`."})
		add(BuiltinMethodDoc{Name: "push", RecvMode: "&self", Signature: fmt.Sprintf("(&self, val: own %s)", el), Params: []string{fmt.Sprintf("val: own %s", el)}, ReturnType: "void", Snippet: "push($1)", Summary: "Appends `val` to the end of the vector, taking ownership."})
		add(BuiltinMethodDoc{Name: "pop", RecvMode: "&self", Signature: fmt.Sprintf("(&self) ?%s", el), ReturnType: "?" + el, Snippet: "pop()", Summary: "Removes and returns the last element, or `none` if empty."})
		add(BuiltinMethodDoc{Name: "get", RecvMode: "self", Signature: fmt.Sprintf("(self, idx: int) ?%s", el), Params: []string{"idx: int"}, ReturnType: "?" + el, Snippet: "get($1)", Summary: "Bounds-checked copy lookup returning `?T` (`none` if out of bounds)."})
		add(BuiltinMethodDoc{Name: "contains", RecvMode: "self", Signature: fmt.Sprintf("(self, val: %s) bool", el), Params: []string{fmt.Sprintf("val: %s", el)}, ReturnType: "bool", Snippet: "contains($1)", Summary: "Returns `true` if any element equals `val`."})
		add(BuiltinMethodDoc{Name: "reserve", RecvMode: "&self", Signature: "(&self, cap: int)", Params: []string{"cap: int"}, ReturnType: "void", Snippet: "reserve($1)", Summary: "Ensures capacity for at least `cap` elements without reallocating."})
		add(BuiltinMethodDoc{Name: "clear", RecvMode: "&self", Signature: "(&self)", ReturnType: "void", Snippet: "clear()", Summary: "Drops all elements and resets length to 0 while keeping capacity."})
		if vec.Elem.Equals(types.TypeString) {
			add(BuiltinMethodDoc{Name: "join", RecvMode: "self", Signature: "(self, sep: string) string", Params: []string{"sep: string"}, ReturnType: "string", Snippet: "join($1)", Summary: "Concatenates all strings in `[string]` separated by `sep`."})
		}
	}

	// `[N: T]` StackArray methods
	if arr, ok := recvTy.(*types.StackArray); ok {
		el := arr.Elem.String()
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: fmt.Sprintf("Returns the fixed compile-time length (%d).", arr.Size)})
		add(BuiltinMethodDoc{Name: "is_empty", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_empty()", Summary: "Returns `true` if array length is 0."})
		add(BuiltinMethodDoc{Name: "get", RecvMode: "self", Signature: fmt.Sprintf("(self, idx: int) ?%s", el), Params: []string{"idx: int"}, ReturnType: "?" + el, Snippet: "get($1)", Summary: "Bounds-checked element lookup returning `?T`."})
		add(BuiltinMethodDoc{Name: "contains", RecvMode: "self", Signature: fmt.Sprintf("(self, val: %s) bool", el), Params: []string{fmt.Sprintf("val: %s", el)}, ReturnType: "bool", Snippet: "contains($1)", Summary: "Returns `true` if any element equals `val`."})
	}

	// Universal `.copy()` and `.to_string()` when supported
	if !types.IsArena(recvTy) {
		if st, isSt := recvTy.(*types.Struct); !isSt || !st.HasCustomDrop {
			add(BuiltinMethodDoc{
				Name:       "copy",
				RecvMode:   "self",
				Signature:  fmt.Sprintf("(self) %s", recvTy.String()),
				ReturnType: recvTy.String(),
				Snippet:    "copy()",
				Summary:    "Creates a deep, independent owned copy of `self`.",
			})
		}
		if _, isSt := recvTy.(*types.Struct); !isSt {
			if _, isFn := recvTy.(*types.Func); !isFn {
				if _, isPtr := recvTy.(*types.Ptr); !isPtr {
					if _, isRef := recvTy.(*types.Ref); !isRef {
						add(BuiltinMethodDoc{
							Name:       "to_string",
							RecvMode:   "self",
							Signature:  "(self) string",
							ReturnType: "string",
							Snippet:    "to_string()",
							Summary:    "Formats `self` as a new owned `string` (`Printable`).",
						})
					}
				}
			}
		}
	}

	return out
}
