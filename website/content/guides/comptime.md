---
title: Compile-time evaluation
summary: const functions and lookup tables, comptime control flow, const generics, reflection, @derivable, comp.TARGET, and asset embedding.
order: 14
group: Core ideas
---

# Compile-time evaluation

Lowlight evaluates pure computations at compile time inside the same language you already use at runtime—without a separate preprocessor, macro sub-language, or external build scripts. Every `const`, `comptime`, reflection query, and custom `@derive` implementation is type-checked once and executed deterministically within resource limits.

## `const`, `bind`, and `@comptime_only`

Lowlight distinguishes compile-time code from runtime bindings:

| Keyword / attribute | Evaluated | May call at runtime? | Typical use |
|---|---|---|---|
| `const x = expr` | compile time | yes | lookup tables, constants, derived configuration |
| `const f = fn(...)` | both | yes | math, string/bytes formatting, pure algorithms |
| `@comptime_only const` | compile time only | **no** | table builders, reflection, `@derivable` helpers |
| `bind x = expr` | before `main` / locally | yes | runtime immutables, FFI, I/O, tests |

```lowlight fragment
// Pure functions marked `const` work in both `const` initializers and runtime calls.
const fib = fn(n: int) int:
	if(n <= 1): n else: fib(n - 1) + fib(n - 2)

// Evaluated while compiling: no work happens when the binary starts.
const FIB_12 = fib(12)

// Default compile-time limits are 1_000_000 steps and 64 MB of simulated heap.
// Override per declaration when building large generated data:
@comptime_limit(steps: 5_000_000, memory: 128_000_000)
@comptime_only
const _heavy_builder = fn() [int]: { ... }
```

Calling host I/O (`println`, `std/fs`), `args()`, `exit()`, `extern` C functions, or user `unsafe` blocks inside a `const` function is rejected at compile time.

## Static `.rodata` tables and copy-on-write

Top-level `const` strings, lists, and fixed arrays (as well as `comptime expr` results) cross into the executable as read-only static slices (`.rodata`, `cap == 0`) with zero startup allocation. Calling mutating `List` or `string` methods on one promotes it lazily via copy-on-write, leaving the shared read-only table untouched.

For example, generating an IEEE CRC32 lookup table at compile time puts the 256-entry table in `.rodata` while keeping the `crc32` function usable both at compile time and at runtime:

```lowlight
import "std/strconv"

@comptime_only
const _build_crc32_table = fn() [256: u32]: {
	let table = [256: u32(0)]
	for(i in 0..256): {
		let c = u32(i)
		for(_ in 0..8): {
			if((c & u32(1)) != u32(0)): c = u32(0xedb88320) ^ (c >> 1)
			else: c = c >> 1
		}
		table[i] = c
	}
	table
}

const CRC32_TABLE = _build_crc32_table()

const crc32 = fn(data: string) u32: {
	let crc = u32(0xffffffff)
	for(i in 0..data.len()): {
		let idx = int((crc ^ u32(data.byte_at(i))) & u32(0xff))
		crc = CRC32_TABLE[idx] ^ (crc >> 8)
	}
	crc ^ u32(0xffffffff)
}

const CHECK_VECTOR = crc32("123456789")

bind main = fn(): {
	let runtime = crc32("123456789")
	assert(CHECK_VECTOR == runtime)
	println(strconv.int_to_hex(int(CHECK_VECTOR), false))
}
```

```output
cbf43926
```

## Inline `comptime` and compile-time control flow

- **`comptime expr` / `comptime { ... }`:** evaluates inside runtime code and splices the result directly into the binary.
- **`comptime if(cond):`** evaluates `cond` during checking and keeps only the selected branch. In expression position it must have an `else` arm.
- **`comptime match(scrut):`** checks every arm for exhaustiveness statically, then lowers only the single matching arm with bound pattern variables available as constants.
- **`comptime for(v in seq):`** unrolls over compile-time ranges, arrays, lists, strings, sets, and maps—including collecting `comptime for(...) T:` expressions to construct arrays, tuples, collections, or structs.

### What can cross to runtime

Plain values (`int`, `float`, `bool`, `char`, `Range`, tag enums, arrays, structs/tuples/enums without `Drop`), `string`, `[T]`, `none` optionals, and non-closure `const` `fn` references cross cleanly from `comptime` to runtime. `Map`, `Set`, `Arena`, `ref[T]`, `ptr[T]`, closures, and `@comptime_only` types cannot cross directly: use them freely inside `@comptime_only` builders and project the result into a `const` list, array, or struct.

## Const generic parameters

Structs, enums, results, interfaces, functions, and methods can mix type parameters and const generic value parameters (`[N: const int]`, where allowed constant types are integer primitives, `bool`, `char`, tag enums, and `string`). Canonical constant equality applies: `Buffer[4 + 4]` and `Buffer[8]` are the exact same type, and `N` can size fixed arrays (`[N * 2: u8]`) and repeat literals (`[N: 0]`). See [Interfaces and generics](/guides/generics.html#const-generic-parameters) for an example.

## Reflection and custom `@derivable` traits

Without emitting runtime metadata (RTTI), `@comptime_only` reflection lets generic code inspect types and manipulate fields or variants safely:

| Query / construct | Meaning |
|---|---|
| `comp.type_name[T]()` · `comp.kind_of[T]()` | Human-readable type name and `comp.TypeKind` classification |
| `comp.fields_of[T]()` | `[comp.FieldInfo]` for structs (`name`, `type_name`, `is_opt`, `is_public`, `has_default`, `attrs`) |
| `comp.variants_of[T]()` | `[comp.VariantInfo]` for enums and `result` types |
| `comp.elems_of[T]()` | `[comp.ElemInfo]` for tuples and `[N: T]` fixed arrays |
| `comp.attrs_of[T]()` · `comp.implements[T, I]()` | Declaration attributes (`AttrInfo`) and structural interface satisfaction |
| `v.field(f)` · `t.elem(e)` | Access, borrow (`&`), assign, or move a reflected struct field or tuple/array element |
| `variant_index(v)` · `v.is_variant(vr)` · `v.payload(vr)` · `v.as_variant(vr)` · `make_variant[T](vr [, own p])` | Inspect, narrow, extract, or construct enum/result variants generically |

Descriptors report `_`-prefixed private members (`is_public == false`), and reading or writing a private member via reflection outside its package is a compile error—except inside a generic helper instantiated by `@derive(...)` on the type's own declaration.

Any interface (in `std/core` or your own package) can declare `@derivable(method: generic_impl, ...)` so structs, enums, and results derive it automatically. Derived methods become `const` whenever every field/variant operation they call is `const`, falling back to `bind` otherwise:

```lowlight
@derivable(tag_summary: _derive_summary)
define TagSummary interface [
	tag_summary: fn(self) string,
]

const _derive_summary[T] = fn(v: T) string: {
	let parts: [string] = []
	comptime for(f in comp.fields_of[T]()):
		parts.push("${f.name}=${v.field(f)}")
	"${comptime comp.type_name[T]()}(${parts.join(", ")})"
}

@derive(TagSummary)
define ServerCfg struct [
	host: string,
	port: int,
]

bind main = fn(): println(ServerCfg[host: "localhost", port: 8080].tag_summary())
```

```output
ServerCfg(host=localhost, port=8080)
```

## Target selection and cross-compilation

`comp.TARGET` is an `@comptime_only` `comp.Target` descriptor populated from `--target`, `[build].target`, or the host (`os: comp.TargetOs`, `arch: comp.TargetArch`, `endian: comp.Endian`, `is_unix: bool`, `ptr_size: int`, `ptr_bits: int`, `triple: string`).

- **Single-file `comptime if(comp.TARGET...)` is preferred** for conditional OS/architecture code—both at top level (wrapping declarations, `extern`s, and `const`s) and inside functions. `import` is disallowed inside top-level `comptime if` branches so package graphs remain target-independent.
- **Target filename suffixes** (`foo_linux.ll`, `foo_darwin.ll`, `foo_unix.ll`, `foo_arm64.ll`) remain supported when splitting large platform modules.
- Run **`lowlight check --all-targets`** to type-check untaken `comptime if(comp.TARGET...)` branches across all canonical OS/arch targets without waiting for CI, and **`lowlight expand`** to inspect expanded post-comptime Lowlight source.

## Static file embedding

Use `comp.embed_str(path)` (`string`, validated UTF-8) and `comp.embed_bytes(path)` (`[u8]`) inside `const` or `comptime` contexts to bake static assets into `.rodata` (`cap == 0`). Paths resolve relative to the `.ll` file containing the call and are confined strictly inside the declaring package/project sandbox (rejecting absolute paths, `..` traversal escapes, and escaping symlinks). Edits to embedded assets automatically invalidate cached package objects and IDE analysis.
