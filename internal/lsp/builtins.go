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
	{Word: "define", Detail: "nominal type declaration", Summary: "Defines a `struct`, `enum`, `result`, `interface`, or distinct derived type (`define UserID int`)."},
	{Word: "struct", Detail: "product value type `[field: T, ...]`", Summary: "Declares a value-type struct (`define Point struct [x: float, y: float]`). Prefix `packed` for unpadded C ABI layout."},
	{Word: "enum", Detail: "tag or tagged-union enum `[A, B, ...]`", Summary: "Declares a strongly-typed integer tag enum (`define Status enum u8 [Ok = 0, Err = 1]`) or tagged union."},
	{Word: "interface", Detail: "dynamic/generic method contract", Summary: "Declares an interface (`define Shape interface [area: (self) float]`)."},
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
	{Word: "extern", Detail: "C11 header or ABI function import", Summary: "Includes a C header (`extern \"stdio.h\"`) or binds a C ABI function (`extern puts: (s: ptr[u8]) i32`)."},
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
		Snippet: "bind main = (): {\n\t$0\n}",
		Summary: "Defines the Lowlight entry point function `main`.",
	},
	{
		Label:   "bind func",
		Detail:  "Define a top-level function",
		Snippet: "bind ${1:name} = (${2:x: int}) ${3:int}: {\n\t$0\n}",
		Summary: "Defines a top-level function with a block body.",
	},
	{
		Label:   "bind func expr",
		Detail:  "Define a single-expression function",
		Snippet: "bind ${1:name} = (${2:x: int}) ${3:int}: ${0:x}",
		Summary: "Defines a top-level function whose body is a single expression.",
	},
	{
		Label:   "bind method",
		Detail:  "Define a method on a type",
		Snippet: "bind ${1:Type}.${2:name} = (${3:self}) ${4:int}: {\n\t$0\n}",
		Summary: "Defines a method on a type. Use `&self` to mutate the receiver or `own self` to consume it.",
	},
	{
		Label:   "define struct",
		Detail:  "Define a value struct",
		Snippet: "define ${1:Name} struct [\n\t${2:x}: ${3:int},\n\t${4:y}: ${5:int},\n]",
		Summary: "Defines a nominal value struct with automatic deterministic cleanup.",
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
		Snippet: "define ${1:Name} interface [\n\t${2:method}: (self) ${3:string}\n]",
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
		Label:   "for in",
		Detail:  "Iterate range or collection",
		Snippet: "for(${1:item} in ${2:items}): {\n\t$0\n}",
		Summary: "Iterates over a range (`0..n`) or collection (`[T]`, `[N: T]`, `#[T]`, `#[K: V]`, `string`).",
	},
	{
		Label:   "while",
		Detail:  "Conditional while loop",
		Snippet: "while(${1:cond}): {\n\t$0\n}",
		Summary: "Executes a loop while a condition evaluates to true.",
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
		Snippet: "match(${1:val}):\n\twhen ${2:Type}.${3:Variant}: ${4:expr}\n\tdefault: ${0:panic(\"todo\")}",
		Summary: "Exhaustively matches over enum variants.",
	},
	{
		Label:   "result chain",
		Detail:  "Handle a result's failures, propagating `Err`",
		Snippet: "${1:load()}:\n\tdefault Err(${2:e}): func <- Err($2.wrap(\"${3:context}\"))",
		Summary: "Evaluates to the result's `value`; on failure, returns it from the enclosing function with added context.",
	},
	{
		Label:   "result chain when",
		Detail:  "Handle specific failures of a result",
		Snippet: "${1:find(key)}:\n\twhen ${2:Missing}(${3:_}): ${4:fallback}\n\tdefault panic",
		Summary: "Handles listed failures (each arm unwinds or evaluates to the value type); `default panic` panics on the rest.",
	},
	{
		Label:   "default panic",
		Detail:  "Unwrap a result, panicking on failure",
		Snippet: "${1:load()}: default panic",
		Summary: "Evaluates to the result's `value` and panics with `unhandled failure Name.Variant` on any failure.",
	},
	{
		Label:   "test",
		Detail:  "Define a unit test",
		Snippet: "@test(\"${1:description}\")\nbind test_${2:name} = (): {\n\t$0\n}",
		Summary: "Defines a test function runnable via `lowlight test`.",
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
	{Name: "Arena", Kind: KindStruct, Detail: "Bump-allocated memory region", Summary: "Region bump allocator (`Arena.new()`). Frees all `arena.alloc(val)` objects in O(1) at scope exit; statically prevents `ref[T]` escape."},
	{Name: "ptr", Kind: KindStruct, Detail: "ptr[T] — raw pointer", Snippet: "ptr[${1:u8}]", Summary: "Raw pointer `ptr[T]` for manual memory and C FFI. Copyable Plain address; dereferencing (`*p`, `p[i]`) requires `unsafe`."},
	{Name: "ref", Kind: KindStruct, Detail: "ref[T] — safe arena reference", Snippet: "ref[${1:Node}]", Summary: "Non-null, plain-copyable reference `ref[T]` allocated inside an `Arena`. Statically verified never to outlive its `Arena`."},
	{Name: "Printable", Kind: KindInterface, Detail: "built-in interface `[to_string: (self) string]`", Summary: "Built-in interface satisfied by all primitives, enums, strings, collections of `Printable`, and user types defining `to_string`."},
	{Name: "Copyable", Kind: KindInterface, Detail: "built-in generic bound `[copy: (self) Self]`", Summary: "Generic constraint (`[T: Copyable]`) satisfied by all Plain types, `string`, collections of `Copyable`, and structs without custom `Drop` (or with explicit `.copy()`)."},
	{Name: "Drop", Kind: KindInterface, Detail: "custom destructor `[drop: (&self)]`", Summary: "Implemented via `bind MyType.drop = (&self): ...` to run custom cleanup deterministically before fields are freed."},
	{Name: "Equatable", Kind: KindInterface, Detail: "built-in generic bound `[eq: (self, other: Self) bool]`", Summary: "Generic equality constraint (`[T: Equatable]`) satisfied by primitives, strings, enums, tuples/collections of `Equatable`, and types defining `eq`."},
	{Name: "Hashable", Kind: KindInterface, Detail: "built-in generic bound `[hash: (self) u64]`", Summary: "Generic hash constraint (`[T: Hashable]`) required for `Set[T]` and `Map[K, V]` keys."},
	{Name: "Comparable", Kind: KindInterface, Detail: "built-in ordering bound `[cmp: (self, other: Self) Ordering]`", Summary: "Generic total-ordering constraint (`[T: Comparable]`) satisfied by numeric primitives, `char`, `string`, and types implementing `cmp`."},
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

	// Low-level memory intrinsics
	{
		Name:        "size_of",
		Signature:   "size_of[T]() int",
		Params:      nil,
		ReturnType:  "int",
		Snippet:     "size_of[${1:int}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time byte size (`sizeof(T)`) of type `T`. Safe to call anywhere.",
	},
	{
		Name:        "align_of",
		Signature:   "align_of[T]() int",
		Params:      nil,
		ReturnType:  "int",
		Snippet:     "align_of[${1:int}]()",
		IsIntrinsic: true,
		NeedsUnsafe: false,
		Summary:     "Compile-time byte alignment (`_Alignof(T)`) of type `T`. Safe to call anywhere.",
	},
	{
		Name:        "is_plain",
		Signature:   "is_plain[T]() bool",
		Params:      nil,
		ReturnType:  "bool",
		Snippet:     "is_plain[${1:int}]()",
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
		if BuiltinFunctions[i].Name == name {
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

	// `char` methods
	if recvTy.Equals(types.TypeChar) {
		add(BuiltinMethodDoc{Name: "is_alpha", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_alpha()", Summary: "Returns `true` if `self` is an ASCII alphabetic letter (`'a'..='z'` or `'A'..='Z'`)."})
		add(BuiltinMethodDoc{Name: "is_digit", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_digit()", Summary: "Returns `true` if `self` is an ASCII decimal digit (`'0'..='9'`)."})
		add(BuiltinMethodDoc{Name: "is_alnum", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_alnum()", Summary: "Returns `true` if `self` is ASCII alphanumeric."})
		add(BuiltinMethodDoc{Name: "is_whitespace", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_whitespace()", Summary: "Returns `true` if `self` is ASCII whitespace (`' '`, `'\\t'`, `'\\n'`, `'\\r'`)."})
		add(BuiltinMethodDoc{Name: "to_int", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "to_int()", Summary: "Returns the Unicode code point of `self` as `int`."})
	}

	// `string` methods
	if recvTy.Equals(types.TypeString) {
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: "Returns the UTF-8 byte length of the string in O(1)."})
		add(BuiltinMethodDoc{Name: "is_empty", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_empty()", Summary: "Returns `true` if `.len() == 0`."})
		add(BuiltinMethodDoc{Name: "contains", RecvMode: "self", Signature: "(self, sub: string) bool", Params: []string{"sub: string"}, ReturnType: "bool", Snippet: "contains($1)", Summary: "Returns `true` if `sub` occurs anywhere inside `self`."})
		add(BuiltinMethodDoc{Name: "starts_with", RecvMode: "self", Signature: "(self, sub: string) bool", Params: []string{"sub: string"}, ReturnType: "bool", Snippet: "starts_with($1)", Summary: "Returns `true` if `self` begins with `sub`."})
		add(BuiltinMethodDoc{Name: "ends_with", RecvMode: "self", Signature: "(self, sub: string) bool", Params: []string{"sub: string"}, ReturnType: "bool", Snippet: "ends_with($1)", Summary: "Returns `true` if `self` ends with `sub`."})
		add(BuiltinMethodDoc{Name: "index_of", RecvMode: "self", Signature: "(self, sub: string) ?int", Params: []string{"sub: string"}, ReturnType: "?int", Snippet: "index_of($1)", Summary: "Returns the byte index of the first occurrence of `sub`, or `none`."})
		add(BuiltinMethodDoc{Name: "trim", RecvMode: "self", Signature: "(self) string", ReturnType: "string", Snippet: "trim()", Summary: "Returns a new owned `string` with leading and trailing whitespace removed."})
		add(BuiltinMethodDoc{Name: "split", RecvMode: "self", Signature: "(self, sep: string) [string]", Params: []string{"sep: string"}, ReturnType: "[string]", Snippet: "split($1)", Summary: "Splits `self` on `sep` into a new `[string]` vector."})
		add(BuiltinMethodDoc{Name: "replace", RecvMode: "self", Signature: "(self, old: string, new_str: string) string", Params: []string{"old: string", "new_str: string"}, ReturnType: "string", Snippet: "replace($1, $2)", Summary: "Returns a new `string` with all occurrences of `old` replaced by `new_str`."})
		add(BuiltinMethodDoc{Name: "to_upper", RecvMode: "self", Signature: "(self) string", ReturnType: "string", Snippet: "to_upper()", Summary: "Returns an ASCII-uppercased copy of `self`."})
		add(BuiltinMethodDoc{Name: "to_lower", RecvMode: "self", Signature: "(self) string", ReturnType: "string", Snippet: "to_lower()", Summary: "Returns an ASCII-lowercased copy of `self`."})
		add(BuiltinMethodDoc{Name: "byte_at", RecvMode: "self", Signature: "(self, idx: int) u8", Params: []string{"idx: int"}, ReturnType: "u8", Snippet: "byte_at($1)", Summary: "Returns byte at `idx` (panics if out of bounds)."})
		add(BuiltinMethodDoc{Name: "get_byte", RecvMode: "self", Signature: "(self, idx: int) ?u8", Params: []string{"idx: int"}, ReturnType: "?u8", Snippet: "get_byte($1)", Summary: "Bounds-checked byte lookup returning `?u8` (`none` if out of range)."})
		add(BuiltinMethodDoc{Name: "bytes", RecvMode: "self", Signature: "(self) [u8]", ReturnType: "[u8]", Snippet: "bytes()", Summary: "Copies the string's UTF-8 bytes into an owned `[u8]` vector."})
		add(BuiltinMethodDoc{Name: "chars", RecvMode: "self", Signature: "(self) [char]", ReturnType: "[char]", Snippet: "chars()", Summary: "Decodes UTF-8 code points into an owned `[char]` vector."})
		add(BuiltinMethodDoc{Name: "push", RecvMode: "&self", Signature: "(&self, suffix: string)", Params: []string{"suffix: string"}, ReturnType: "void", Snippet: "push($1)", Summary: "Appends `suffix` to mutable `let` string in place."})
		add(BuiltinMethodDoc{Name: "push_char", RecvMode: "&self", Signature: "(&self, ch: char)", Params: []string{"ch: char"}, ReturnType: "void", Snippet: "push_char($1)", Summary: "UTF-8 encodes and appends `ch` in place."})
		add(BuiltinMethodDoc{Name: "push_byte", RecvMode: "&self", Signature: "(&self, b: u8)", Params: []string{"b: u8"}, ReturnType: "void", Snippet: "push_byte($1)", Summary: "Appends raw byte `b` in place."})
		add(BuiltinMethodDoc{Name: "push_bytes", RecvMode: "&self", Signature: "(&self, bytes: ptr[u8], count: int)", Params: []string{"bytes: ptr[u8]", "count: int"}, ReturnType: "void", Snippet: "push_bytes($1, $2)", Summary: "Appends `count` raw bytes from `bytes` pointer in place."})
		add(BuiltinMethodDoc{Name: "clear", RecvMode: "&self", Signature: "(&self)", ReturnType: "void", Snippet: "clear()", Summary: "Resets string length to 0 while retaining allocated capacity."})
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
		add(BuiltinMethodDoc{Name: "remove", RecvMode: "&self", Signature: fmt.Sprintf("(&self, idx: int) %s", el), Params: []string{"idx: int"}, ReturnType: el, Snippet: "remove($1)", Summary: "Removes and returns element at `idx`, shifting subsequent elements left."})
		add(BuiltinMethodDoc{Name: "swap_remove", RecvMode: "&self", Signature: fmt.Sprintf("(&self, idx: int) %s", el), Params: []string{"idx: int"}, ReturnType: el, Snippet: "swap_remove($1)", Summary: "O(1) removal at `idx` by swapping with the last element."})
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

	// `#[T]` Set methods
	if setTy, ok := recvTy.(*types.Set); ok {
		el := setTy.Elem.String()
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: "Returns the number of elements in the set."})
		add(BuiltinMethodDoc{Name: "is_empty", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_empty()", Summary: "Returns `true` if the set is empty."})
		add(BuiltinMethodDoc{Name: "has", RecvMode: "self", Signature: fmt.Sprintf("(self, val: %s) bool", el), Params: []string{fmt.Sprintf("val: %s", el)}, ReturnType: "bool", Snippet: "has($1)", Summary: "Returns `true` if `val` is present in the set."})
		add(BuiltinMethodDoc{Name: "contains", RecvMode: "self", Signature: fmt.Sprintf("(self, val: %s) bool", el), Params: []string{fmt.Sprintf("val: %s", el)}, ReturnType: "bool", Snippet: "contains($1)", Summary: "Alias for `.has(val)`."})
		add(BuiltinMethodDoc{Name: "insert", RecvMode: "&self", Signature: fmt.Sprintf("(&self, val: own %s) bool", el), Params: []string{fmt.Sprintf("val: own %s", el)}, ReturnType: "bool", Snippet: "insert($1)", Summary: "Inserts `val` into the set; returns `true` if newly added."})
		add(BuiltinMethodDoc{Name: "remove", RecvMode: "&self", Signature: fmt.Sprintf("(&self, val: %s) bool", el), Params: []string{fmt.Sprintf("val: %s", el)}, ReturnType: "bool", Snippet: "remove($1)", Summary: "Removes `val` from the set; returns `true` if it was present."})
		add(BuiltinMethodDoc{Name: "clear", RecvMode: "&self", Signature: "(&self)", ReturnType: "void", Snippet: "clear()", Summary: "Removes and drops all elements in the set."})
		add(BuiltinMethodDoc{Name: "to_vec", RecvMode: "self", Signature: fmt.Sprintf("(self) [%s]", el), ReturnType: "[" + el + "]", Snippet: "to_vec()", Summary: "Copies all set elements into a new `[T]` vector."})
		add(BuiltinMethodDoc{Name: "to_array", RecvMode: "self", Signature: fmt.Sprintf("(self) [%s]", el), ReturnType: "[" + el + "]", Snippet: "to_array()", Summary: "Alias for `.to_vec()`. Copies all set elements into a new `[T]` vector."})
	}

	// `#[K: V]` Map methods
	if mapTy, ok := recvTy.(*types.Map); ok {
		k := mapTy.Key.String()
		v := mapTy.Value.String()
		add(BuiltinMethodDoc{Name: "len", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "len()", Summary: "Returns the number of key-value entries in the map."})
		add(BuiltinMethodDoc{Name: "is_empty", RecvMode: "self", Signature: "(self) bool", ReturnType: "bool", Snippet: "is_empty()", Summary: "Returns `true` if the map has 0 entries."})
		add(BuiltinMethodDoc{Name: "has", RecvMode: "self", Signature: fmt.Sprintf("(self, key: %s) bool", k), Params: []string{fmt.Sprintf("key: %s", k)}, ReturnType: "bool", Snippet: "has($1)", Summary: "Returns `true` if `key` exists in the map."})
		add(BuiltinMethodDoc{Name: "contains", RecvMode: "self", Signature: fmt.Sprintf("(self, key: %s) bool", k), Params: []string{fmt.Sprintf("key: %s", k)}, ReturnType: "bool", Snippet: "contains($1)", Summary: "Alias for `.has(key)`."})
		add(BuiltinMethodDoc{Name: "insert", RecvMode: "&self", Signature: fmt.Sprintf("(&self, key: own %s, val: own %s)", k, v), Params: []string{fmt.Sprintf("key: own %s", k), fmt.Sprintf("val: own %s", v)}, ReturnType: "void", Snippet: "insert($1, $2)", Summary: "Inserts or overwrites `key` with `val`."})
		add(BuiltinMethodDoc{Name: "push", RecvMode: "&self", Signature: fmt.Sprintf("(&self, entry: own +[%s, %s])", k, v), Params: []string{fmt.Sprintf("entry: own +[%s, %s]", k, v)}, ReturnType: "void", Snippet: "push($1)", Summary: "Inserts or overwrites a `+[key, val]` entry in the map."})
		add(BuiltinMethodDoc{Name: "remove", RecvMode: "&self", Signature: fmt.Sprintf("(&self, key: %s) ?%s", k, v), Params: []string{fmt.Sprintf("key: %s", k)}, ReturnType: "?" + v, Snippet: "remove($1)", Summary: "Removes `key` and returns its owned value `?V` (`none` if absent)."})
		add(BuiltinMethodDoc{Name: "keys", RecvMode: "self", Signature: fmt.Sprintf("(self) [%s]", k), ReturnType: "[" + k + "]", Snippet: "keys()", Summary: "Returns all keys in insertion order as an owned `[K]` vector."})
		add(BuiltinMethodDoc{Name: "values", RecvMode: "self", Signature: fmt.Sprintf("(self) [%s]", v), ReturnType: "[" + v + "]", Snippet: "values()", Summary: "Returns copies of all values in insertion order as `[V]`."})
		add(BuiltinMethodDoc{Name: "clear", RecvMode: "&self", Signature: "(&self)", ReturnType: "void", Snippet: "clear()", Summary: "Drops all keys and values and resets length to 0."})
	}

	// `Arena` instance methods
	if recvTy.Equals(types.TypeArena) {
		add(BuiltinMethodDoc{Name: "alloc", RecvMode: "&self", Signature: "(&self, val: own T) ref[T]", Params: []string{"val: own T"}, ReturnType: "ref[T]", Snippet: "alloc($1)", Summary: "Bump-allocates `val` inside the `Arena` and returns a region-checked `ref[T]`."})
		add(BuiltinMethodDoc{Name: "reset", RecvMode: "&self", Signature: "(&self)", ReturnType: "void", Snippet: "reset()", Summary: "Rewinds the arena bump cursor to 0 without freeing backing chunks."})
		add(BuiltinMethodDoc{Name: "bytes_used", RecvMode: "self", Signature: "(self) int", ReturnType: "int", Snippet: "bytes_used()", Summary: "Returns the total bytes currently bump-allocated across arena chunks."})
	}

	// Universal `.copy()` and `.to_string()` when supported
	if !recvTy.Equals(types.TypeArena) {
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
