# The Lowlight Language Specification

**Language Edition:** 2026  
**Document Status:** Normative Language Reference  

---

## Abstract

Lowlight is a statically typed, expression-oriented systems programming language engineered for uncompromising memory safety, predictable zero-cost execution, and human ergonomics. Lowlight eliminates entire classes of software defects—including null pointer dereferences, data races, use-after-free, double-free, dangling pointers, unhandled error states, and unconstrained memory leaks—without employing a garbage collector, a reference-counting runtime, or manual lifetime annotations.

Lowlight enforces memory safety through an affine ownership model with deterministic LIFO lexical destruction, strict borrow exclusivity, compile-time region escape analysis, and an explicit distinction between trivially copyable *Plain* values and destructured *Resource* values.

This document serves as the complete, authoritative, and implementation-independent specification of the Lowlight programming language. A conforming Lowlight compiler or toolchain may target native machine code via LLVM, ISO C (C11), WebAssembly, the Java Virtual Machine, or JavaScript. Any compiler that satisfies the syntax, type system, ownership rules, semantic constraints, and standard prelude behavior described herein is a conforming implementation of Lowlight.

---

## Table of Contents

1. [Introduction & Design Principles](#1-introduction--design-principles)
2. [Lexical Structure](#2-lexical-structure)
   - [2.1 Source Representation & Unicode](#21-source-representation--unicode)
   - [2.2 Whitespace, Comments, & Semicolons](#22-whitespace-comments--semicolons)
   - [2.3 Automatic Semicolon Insertion (ASI)](#23-automatic-semicolon-insertion-asi)
   - [2.4 Multiline Trailing Commas](#24-multiline-trailing-commas)
   - [2.5 Identifiers & Optional Identifiers](#25-identifiers--optional-identifiers)
   - [2.6 Keywords & Contextual Keywords](#26-keywords--contextual-keywords)
   - [2.7 Literals](#27-literals)
   - [2.8 String Literals & String Interpolation](#28-string-literals--string-interpolation)
   - [2.9 Operators & Delimiters](#29-operators--delimiters)
3. [Type System & Value Categories](#3-type-system--value-categories)
   - [3.1 Value Categories: Plain vs. Resource](#31-value-categories-plain-vs-resource)
   - [3.2 Primitive Numerical Types](#32-primitive-numerical-types)
   - [3.3 Boolean & Character Types](#33-boolean--character-types)
   - [3.4 The String Type](#34-the-string-type)
   - [3.5 Optional Types (`?T`) & Flattening Invariant](#35-optional-types-t--flattening-invariant)
   - [3.6 Collection Types (`[T]`, `[N: T]`, `#[T]`, `#[K: V]`)](#36-collection-types-t-n-t-t-k-v)
   - [3.7 Structs & Field Punning](#37-structs--field-punning)
   - [3.8 Enums: Pure Tag Enums & Tagged Unions](#38-enums-pure-tag-enums--tagged-unions)
   - [3.9 Distinct Types](#39-distinct-types)
   - [3.10 Structural Interfaces & `Self`](#310-structural-interfaces--self)
   - [3.11 First-Class Functions & Closures](#311-first-class-functions--closures)
   - [3.12 Arenas (`Arena`) & Scoped References (`ref[T]`)](#312-arenas-arena--scoped-references-reft)
   - [3.13 Raw Pointers (`ptr[T]`)](#313-raw-pointers-ptrt)
   - [3.14 Internal & Control Types (`void`, `never`)](#314-internal--control-types-void-never)
4. [Program Structure, Declarations, & Modules](#4-program-structure-declarations--modules)
   - [4.1 Compilation Units & File Resolution](#41-compilation-units--file-resolution)
   - [4.2 Order-Independent Top-Level Declarations](#42-order-independent-top-level-declarations)
   - [4.3 Modules & Imports](#43-modules--imports)
   - [4.4 Program Entry Point (`main`)](#44-program-entry-point-main)
   - [4.5 Compiler Attributes](#45-compiler-attributes)
   - [4.6 Implicit Standard Prelude](#46-implicit-standard-prelude)
5. [Ownership, Borrowing, & Lifetime Mechanics](#5-ownership-borrowing--lifetime-mechanics)
   - [5.1 Affine Value Ownership](#51-affine-value-ownership)
   - [5.2 Value Moves & The Explicit `own` Qualifier](#52-value-moves--the-explicit-own-qualifier)
   - [5.3 Parameter Passing Modes (`T`, `&T`, `own T`)](#53-parameter-passing-modes-t-t-own-t)
   - [5.4 Borrow Exclusivity & Alias Invariants](#54-borrow-exclusivity--alias-invariants)
   - [5.5 Deterministic Destruction & Custom `Drop`](#55-deterministic-destruction--custom-drop)
   - [5.6 Scoped Arena Pinning & Escape Analysis](#56-scoped-arena-pinning--escape-analysis)
6. [Expressions & Evaluation Semantics](#6-expressions--evaluation-semantics)
   - [6.1 Operator Precedence & Associativity](#61-operator-precedence--associativity)
   - [6.2 Arithmetic Semantics & Boundary Traps](#62-arithmetic-semantics--boundary-traps)
   - [6.3 Equality & Comparison](#63-equality--comparison)
   - [6.4 Indexing & Slicing](#64-indexing--slicing)
   - [6.5 Member Access, UFCS, & Receiver Auto-Borrowing](#65-member-access-ufcs--receiver-auto-borrowing)
   - [6.6 Optional Chaining & Coalescing (`?.`, `??`)](#66-optional-chaining--coalescing--)
   - [6.7 Function Calls & Closures](#67-function-calls--closures)
7. [Statements & Control Flow](#7-statements--control-flow)
   - [7.1 Lexical Blocks & Block Value Rules](#71-lexical-blocks--block-value-rules)
   - [7.2 Variable Declarations (`let` vs. `bind`)](#72-variable-declarations-let-vs-bind)
   - [7.3 Assignment & Compound Mutation](#73-assignment--compound-mutation)
   - [7.4 Conditional Branches (`if` / `else`) & Optional Unwrapping](#74-conditional-branches-if--else--optional-unwrapping)
   - [7.5 Loops (`for`) & Iteration Value Semantics](#75-loops-for--iteration-value-semantics)
   - [7.6 Pattern Matching (`match` / `when`)](#76-pattern-matching-match--when)
   - [7.7 Targeted Lexical Unwinding (`<-`)](#77-targeted-lexical-unwinding--)
8. [Generics & Monomorphization](#8-generics--monomorphization)
   - [8.1 Generic Declarations](#81-generic-declarations)
   - [8.2 Interface Constraints & Bounds](#82-interface-constraints--bounds)
   - [8.3 The Strictly Non-Optional Generic Rule](#83-the-strictly-non-optional-generic-rule)
   - [8.4 Type Inference & Monomorphization Limits](#84-type-inference--monomorphization-limits)
9. [Standard Library Contract & Built-in Operations](#9-standard-library-contract--built-in-operations)
   - [9.1 Built-in Primitive Methods](#91-built-in-primitive-methods)
   - [9.2 String Built-ins](#92-string-built-ins)
   - [9.3 Collection Operations](#93-collection-operations)
   - [9.4 The `Result[T, E]` Standard Type](#94-the-resultt-e-standard-type)
   - [9.5 Universe Built-in Functions](#95-universe-built-in-functions)
10. [Unsafe Operations & Foreign ABI Interoperability](#10-unsafe-operations--foreign-abi-interoperability)
    - [10.1 The `unsafe` Block and Prefix Expression](#101-the-unsafe-block-and-prefix-expression)
    - [10.2 Raw Pointer Mechanics (`ptr[T]`)](#102-raw-pointer-mechanics-ptrt)
    - [10.3 Safe Sizing vs. Unsafe Memory Intrinsics](#103-safe-sizing-vs-unsafe-memory-intrinsics)
    - [10.4 Foreign Function Interface (`extern`)](#104-foreign-function-interface-extern)
11. [Formal Grammar Specification (EBNF)](#11-formal-grammar-specification-ebnf)

---

## 1. Introduction & Design Principles

Lowlight is designed to provide the absolute control and raw efficiency of unmanaged systems languages (such as C) alongside the structural elegance and safety guarantees of modern functional and affine languages.

The language architecture adheres to seven foundational axioms:

1. **Deterministic Lifecycle Over Tracing Garbage Collection:** Memory and system resources (file handles, network sockets, OS allocations) must have predictable lifetimes bound directly to lexical scopes. No background runtime pauses, reference-counting overhead, or unpredictable finalizers exist in safe Lowlight.
2. **Values vs. Resources:** Every type in Lowlight is statically partitioned into either a *Plain* type or a *Resource* type. Plain types are freely copyable bitwise values without destructors. Resource types represent unique capabilities that cannot be copied implicitly; they obey affine ownership rules and are dropped exactly once at the end of their owning scope.
3. **No Null References:** The concept of `null` or `nil` does not exist. The absence of a value is expressed strictly via optional types (`?T`). Optionals do not nest, cannot wrap other optionals, and require explicit syntactic acknowledgment (`?`) at declaration, assignment, unwrap, and access.
4. **No Lifetime Annotations:** Safe Lowlight enforces reference validity through static borrow exclusivity, lexical tree nesting, and scoped arena pinning. Programmers never write lifetime variables (such as `'a`), yet references can never outlive the data they borrow.
5. **Expression Orientation with Syntactic Clarity:** Every block, conditional branch, match arm, and loop is an expression capable of yielding a value. Braces (`{}`) delimit compound blocks and scopes, parentheses (`()`) delimit headers and tuples, brackets (`[]`) delimit type parameters, indexing, struct fields, and vectors, and hash-brackets (`#[ ]`) delimit sets and maps.
6. **Explicit Mutation and Consumption:** Functions cannot mutate caller arguments unless the caller explicitly prefixes the argument with `&` at the call site. Similarly, an existing owned resource cannot be moved into another owner without explicit qualification via the `own` keyword.
7. **Target Neutrality:** The semantics of Lowlight are entirely decoupled from any specific machine representation, intermediate representation, or transpiler backend. A conforming implementation may lower to machine instructions, bytecode, or high-level source code, provided the observable behaviors, ownership invariants, and runtime trap guarantees defined in this specification are preserved.

---

## 2. Lexical Structure

### 2.1 Source Representation & Unicode

A Lowlight source file is a sequence of Unicode characters encoded in UTF-8. Non-ASCII characters are permitted inside comments, character literals, and string literals. Identifiers, keywords, operators, and delimiters must consist strictly of ASCII characters.

### 2.2 Whitespace, Comments, & Semicolons

Whitespace is defined as ASCII space (`U+0020`), horizontal tab (`U+0009`), carriage return (`U+000D`), and line feed (`U+000A`). Whitespace separates tokens and formats source text but is otherwise insignificant except when resolving Automatic Semicolon Insertion (ASI) or parsing the whitespace-delimited unwind operator (`<-`).

Lowlight supports two forms of comments:
- **Single-line comments:** Begin with `//` and extend to the end of the line.
- **Multi-line comments:** Begin with `/*` and terminate with `*/`. Multi-line comments do not nest; the first occurrence of `*/` closes the comment.

Semicolons (`;`) are statement terminators. An explicit semicolon written at the end of a block expression suppresses the tail value and causes the block to yield `void`.

### 2.3 Automatic Semicolon Insertion (ASI)

To eliminate unnecessary punctuation while preserving unambiguous parsing, Lowlight employs a deterministic delimiter-stack Automatic Semicolon Insertion algorithm.

#### 2.3.1 Delimiter Stack
The lexer tracks an internal stack of open delimiters:
- `{` pushes a brace context.
- `(` pushes a paren context.
- `[` and `#[` push a bracket context.
- `${` pushes an interpolation context.

A newline is eligible for semicolon insertion only when `blockLevelAfter` is true: that is, when the delimiter stack is empty (top-level scope) or when the outermost open delimiter on the stack is a curly brace (`{`). When inside parentheses `(...)`, brackets `[...]`, hash-brackets `#[...]`, or string interpolation `${...}`, ASI is entirely disabled.

#### 2.3.2 Insertion Trigger
When `blockLevelAfter` is true and a newline or end-of-file (`EOF`) is encountered, the lexer inserts an artificial semicolon token (`AutoSemi`) if and only if the immediately preceding token belongs to the set of **value-terminating tokens**:
- Identifiers and Optional Identifiers (`Ident`, `OptIdent`)
- Numeric, Boolean, None, Character, and String literals (`IntLit`, `FloatLit`, `CharLit`, `StringLit`, `InterpEnd`, `true`, `false`, `none`)
- Closing delimiters: `)`, `]`, `}`
- Special keywords: `self`, `Self`
- The unwind operator: `<-`

#### 2.3.3 Lookahead Exceptions
An `AutoSemi` is **not** inserted if the next non-whitespace token is one of the following continuation tokens:
- `else`
- `when`
- `.` (member access)
- `?.` (optional member access)

#### 2.3.4 Block Tail Value Preservation
The parser distinguishes an explicit semicolon (`;`) from an `AutoSemi`:
- An explicit `;` after the final expression of a block discards its result, yielding `void`.
- An `AutoSemi` (or an unpunctuated expression immediately followed by `}`) preserves the expression as the block's tail return value.

### 2.4 Multiline Trailing Commas

Lowlight enforces clean diffs in multiline collection literals, argument lists, and parameter definitions via a strict trailing comma rule.

For any comma-delimited sequence enclosed by delimiters `( ... )`, `[ ... ]`, or `#[ ... ]`:
- If the closing delimiter is placed on a line strictly greater than the opening delimiter's line, AND strictly greater than the last element's line, a trailing comma **must** follow the final element.
- Omitting the trailing comma in such a multiline declaration is a static syntax error.
- Trailing commas are optional if the closing delimiter shares the line of the last item or opening delimiter.

This rule applies uniformly across all 13 comma-separated lists in Lowlight:
1. Struct field declarations
2. Enum variant declarations
3. Interface method declarations
4. Function parameter declarations
5. Function call argument lists
6. Struct literal field initializations
7. List literals (`[ ... ]`)
8. Stack array literals (`[ ... ]`)
9. Set literals (`#[ ... ]`)
10. Map literals (`#[ ... ]`)
11. Generic type parameter declarations (`[T, U]`)
12. Generic type argument lists (`[int, string]`)
13. `match` pattern lists (`when A, B, C:`)

### 2.5 Identifiers & Optional Identifiers

Lowlight distinguishes between standard identifiers and optional identifiers:

```ebnf
Ident    = [a-zA-Z_] [a-zA-Z0-9_]* ;
OptIdent = Ident "?" ;
```

- **Standard Identifiers (`Ident`):** Name types, functions, constants, non-optional variables, enum variants, and struct fields.
- **Optional Identifiers (`OptIdent`):** Identifiers ending with a literal question mark `?` (e.g., `user?`, `count?`, `next?`). 
- **Optional Invariant:** Any local variable, function parameter, struct field, or enum variant carrying an optional type `?T` **must** be named using an `OptIdent`. Conversely, a variable holding a non-optional type must never end with `?`. Violating this invariant produces a static compile error.

#### Lexical Disambiguation of `?`
The lexer scans a trailing `?` into an `OptIdent` only if the character following the identifier is `?` and that `?` is not part of a double question mark operator (`??`), unless a third `?` follows (e.g., in `x???` parsed as `x? ??`). If `?` is immediately followed by a dot `.`, it is scanned as part of the `OptIdent` (e.g., `user?.name` produces `OptIdent("user?")` followed by `.` followed by `Ident("name")`).

### 2.6 Keywords & Contextual Keywords

The following 26 tokens are permanently reserved keywords and cannot be used as standard identifiers:

```
as          bind        define      else        enum        extern      
false       for         func        if          import      in          
interface   let         match       none        not         or          
own         self        Self        struct      true        unsafe      
when        and         
```

#### Contextual Keywords
The tokens `next` and `packed` are contextual keywords. They retain identifier status in general expression and declaration contexts, but trigger special parsing under specific grammatical rules:
- `next`: Treated as a loop continue keyword strictly when followed by `<-` (i.e., `next <-` or `next <label> <-`). In all other positions (e.g., `let next = 10`, `item.next()`, `let next? = find()`), `next` is a regular identifier.
- `packed`: Treated as a struct layout modifier strictly when appearing between `define` and the struct name (e.g., `define packed Header struct [ ... ]`).

The type names `void` and `never` are internal semantic types and are not surface keywords.

### 2.7 Literals

#### 2.7.1 Integer Literals
Integer literals represent exact whole numbers. Underscores (`_`) may be placed between digits for readability.
- **Decimal:** Digits `0` through `9` (e.g., `42`, `1_000_000`).
- **Hexadecimal:** Prefixed with `0x` or `0X`, followed by hexadecimal digits `0`–`9`, `a`–`f`, `A`–`F` (e.g., `0xFF`, `0x1A_3F`).
- **Binary:** Prefixed with `0b` or `0B`, followed by binary digits `0` and `1` (e.g., `0b1010`, `0b1111_0000`).

Integer literals adapt to expected integer types (`i8`–`i64`, `u8`–`u64`). If unconstrained, an integer literal defaults to `int` (`i64`).

#### 2.7.2 Floating-Point Literals
Floating-point literals represent IEEE-754 numbers.
- Must contain an integer part, a decimal point `.`, and at least one fractional digit, optionally followed by an exponent `e` or `E` with an optional sign (e.g., `3.14`, `0.5`, `1.0e-4`, `2.5E+3`).
- A digit must immediately follow the decimal point: `1.` is illegal to avoid lexical ambiguity with range syntax (`1..10`) and member access.
- Floating-point literals adapt to `f32` or `f64`. If unconstrained, a float literal defaults to `float` (`f64`).

#### 2.7.3 Boolean Literals
The literals `true` and `false` represent the truth values of type `bool`.

#### 2.7.4 The `none` Literal
The literal `none` denotes the absence of a value in an optional type `?T`. It represents the unit state of optionality and is assignable to any `?T`.

#### 2.7.5 Character Literals
A character literal is enclosed in single quotes `'` and represents a single 32-bit Unicode scalar value (`char`):
- Examples: `'a'`, `'Z'`, `'9'`, `'\n'`, `'\''`, `'🦀'`.
- Valid escape sequences: `\n`, `\r`, `\t`, `\\`, `\'`, `\"`, `\0`, `\xHH` (8-bit hex), `\u{HEX}` (up to 6-digit hex Unicode scalar).

### 2.8 String Literals & String Interpolation

String literals are enclosed in double quotes `"..."` and represent immutable sequences of UTF-8 encoded bytes (`string`).

#### Escape Sequences
Within a string literal, the following escape sequences are recognized:
- `\n` (newline, `0x0A`)
- `\r` (carriage return, `0x0D`)
- `\t` (tab, `0x09`)
- `\\` (literal backslash)
- `\'` (literal single quote)
- `\"` (literal double quote)
- `\0` (null byte, `0x00`)
- `\$` (literal dollar sign, suppresses interpolation)
- `\xHH` (hexadecimal byte value)
- `\u{HEX}` (Unicode scalar value in UTF-8)

#### String Interpolation
Lowlight strings natively support embedded expression interpolation:
- **Identifier Interpolation:** `$ident` interpolates the string representation of an in-scope variable. If the variable is an optional, `$ident` stops before a trailing `?` unless wrapped in braces.
- **Self Interpolation:** `$self` interpolates the receiver within instance methods.
- **Arbitrary Expression Interpolation:** `${expr}` evaluates any valid Lowlight expression and embeds its string representation.

Any type interpolated into a string must implement the standard `Printable` interface (defining `.to_string(): string`). Interpolation is expanded at compile time into formatted string concatenation without runtime reflection.

### 2.9 Operators & Delimiters

Lowlight reserves the following punctuation symbols:

```
+       -       *       /       %       
&       |       ^       ~       <<      >>      
==      !=      <       <=      >       >=      
=       +=      -=      *=      /=      %=      
&=      |=      ^=      <<=     >>=     
.       ?.      ..      ..=     ::      
?       ??      <-      
(       )       [       ]       {       }       #[
:       ,       ;       @       $
```

#### The Whitespace-Delimited Unwind Operator (`<-`)
The unwind operator `<-` indicates an explicit non-local exit, return, break, or continue. To eliminate ambiguity with relational comparisons such as `a < -b`, the operator `<-` is lexed as an unwind token if and only if:
1. It is preceded by the start of a file, whitespace, a comment, or an interpolation open delimiter `${`; AND
2. It is followed by end-of-file, whitespace, a semicolon, a closing delimiter (`)`, `]`, `}`), a comma, or a comment.

---

## 3. Type System & Value Categories

Lowlight is a strictly typed, statically verified language. Types are fully resolved at compile time.

```
Type = Primitive
     | string
     | "?" Type
     | "[" Type "]"
     | "[" IntLit ":" Type "]"
     | "#[" Type "]"
     | "#[" Type ":" Type "]"
     | TypeIdent [ "[" TypeArgs "]" ]
     | "(" [ ParamTypes ] ")" [ ":" Type ]
     | "ref" "[" Type "]"
     | "ptr" "[" Type "]"
     ;
```

### 3.1 Value Categories: Plain vs. Resource

Every type $T$ in Lowlight belongs to one of two mutually exclusive value categories:

```
                      +-------------------+
                      |   Lowlight Types  |
                      +-------------------+
                                |
             +------------------+------------------+
             |                                     |
    +-----------------+                   +-----------------+
    |   Plain Types   |                   |  Resource Types |
    +-----------------+                   +-----------------+
    | Trivially       |                   | Unique owner,   |
    | copyable,       |                   | affine moves,   |
    | no destructor   |                   | deterministic   |
    |                 |                   | drop on exit    |
    +-----------------+                   +-----------------+
    | Primitives,     |                   | string, [T],    |
    | pure enums,     |                   | #[T], #[K: V],  |
    | ptr[T],         |                   | Arena, closures,|
    | Plain structs   |                   | Resource structs|
    +-----------------+                   +-----------------+
```

#### Plain Types
- A type is Plain if it occupies a fixed-size memory footprint, contains no heap allocations, holds no active OS resources, and requires no custom destructor.
- Plain values are **trivially copyable**. Assigning, passing, or returning a Plain value duplicates its bits.
- Plain types automatically satisfy the standard `Copyable` interface.
- Primitives (`int`, `float`, `bool`, `char`), pure tag enums, distinct types wrapping Plain bases, raw pointers `ptr[T]`, and fixed arrays/structs whose elements are all Plain are Plain types.

#### Resource Types
- A type is a Resource if it owns dynamically allocated heap memory, manages an OS resource, contains a custom `Drop` implementation, or wraps another Resource type.
- Resource values obey **affine ownership**. They cannot be implicitly copied. Assigning or passing an owned Resource moves the value, invalidating the source variable.
- When a Resource variable reaches the end of its enclosing lexical scope without being moved, its destructor is executed automatically and deterministically.
- `string`, vectors `[T]`, sets `#[T]`, maps `#[K: V]`, `Arena`, first-class closures, interfaces, and any struct or tagged union carrying a Resource field are Resource types.

### 3.2 Primitive Numerical Types

Lowlight provides exact-width signed and unsigned integers, as well as IEEE-754 floating-point numbers:

| Type | Bit Width | Category | Description |
| :--- | :--- | :--- | :--- |
| `i8` | 8 | Plain | Signed 8-bit two's complement integer |
| `i16` | 16 | Plain | Signed 16-bit two's complement integer |
| `i32` | 32 | Plain | Signed 32-bit two's complement integer |
| `i64` | 64 | Plain | Signed 64-bit two's complement integer |
| `int` | 64 | Plain | Canonical signed integer (alias for `i64`) |
| `u8` | 8 | Plain | Unsigned 8-bit integer (byte) |
| `u16` | 16 | Plain | Unsigned 16-bit integer |
| `u32` | 32 | Plain | Unsigned 32-bit integer |
| `u64` | 64 | Plain | Unsigned 64-bit integer |
| `f32` | 32 | Plain | Single-precision IEEE-754 floating point |
| `f64` | 64 | Plain | Double-precision IEEE-754 floating point |
| `float` | 64 | Plain | Canonical float (alias for `f64`) |

Lowlight strictly prohibits implicit widening or narrowing between numerical types. All cross-type numerical conversions must be stated explicitly using constructor syntax (e.g., `i32(my_int)`, `int(my_u8)`, `float(my_i32)`).

### 3.3 Boolean & Character Types

- **`bool`:** A 1-byte logical type representing either `true` or `false`. Condition expressions in `if` and `for` must evaluate strictly to `bool`.
- **`char`:** A 32-bit unsigned type representing a Unicode scalar value (`0x0000` to `0xD7FF` or `0xE000` to `0x10FFFF`). Surrogates are invalid characters.

### 3.4 The String Type

The `string` type represents an immutable, UTF-8 encoded text sequence. A `string` value carries:
1. A pointer to contiguous UTF-8 byte data;
2. A 64-bit length (count of valid UTF-8 bytes);
3. A 64-bit capacity.

`string` is a Resource type. When created via concatenation, formatting, or slicing promotion, the string owns its heap buffer and frees it upon scope exit. Static string literals borrow read-only static memory (`capacity == 0`) and perform zero heap allocations.

- `s.len()` returns the count of UTF-8 characters (Unicode scalar values).
- `s.byte_len()` returns the exact byte count.
- `s[i]` and `s.byte_at(i)` yield the raw `u8` byte at index `i` (with bounds checking).
- Iterating a string via `for (ch in s)` yields `char` scalars; iterating via `for (i, ch in s)` yields `(byte_offset: int, ch: char)`.

### 3.5 Optional Types (`?T`) & Flattening Invariant

Any type $T$ may be modified with a leading question mark to form an optional type `?T`. An optional represents a value that is either present (containing a value of type $T$) or absent (`none`).

```lowlight
let name?: ?string = "Alice"
let empty?: ?string = none
```

#### 3.5.1 The Non-Nesting Invariant
Optional types in Lowlight **do not nest**. Syntactically writing `??T` is a compile error, and generic substitution of `?T` into a type parameter yielding an optional automatically flattens to `?T`. There is no distinction between "an absent optional" and "a present optional containing absence."

#### 3.5.2 The Optional Naming Rule
Every variable, parameter, or field holding an optional type must end with `?`. Conversely, non-optional bindings must not end with `?`:

```lowlight
let count?: ?int = 42       // Valid
let total: int = 100        // Valid
let bad: ?int = 10          // Compile Error: must be named `bad?`
let invalid?: int = 20      // Compile Error: non-optional cannot end with `?`
```

#### 3.5.3 Implicit Wrapping
A non-optional value of type $T$ implicitly coerces to `?T` when assigned or passed to a function parameter:

```lowlight
bind find_id = (): ?int 100   // 100 implicitly wraps into ?int
```

#### 3.5.4 Recursive Struct Boxing
A struct cannot directly embed an unboxed instance of itself. However, a struct field may reference its own type if wrapped in an optional (`?Node`). In this case, the compiler automatically represents the optional field as a heap-allocated pointer (box), allowing recursive data structures without explicit manual pointer management:

```lowlight
define Node struct [
    value: int,
    next?: ?Node,           // Automatically boxed nullable pointer
]
```

### 3.6 Collection Types (`[T]`, `[N: T]`, `#[T]`, `#[K: V]`)

Lowlight collection nomenclature distinguishes between:
1. **Array**: The fixed-size array type (`[N: T]`), stack-allocated with zero heap overhead.
2. **List**: The dynamically resizable list type (`[T]` / `List[T]`), heap-allocated.
*(Note: `Vector` is not an alias to either and is not part of Lowlight.)*

#### 3.6.1 Dynamic Lists (`[T]` / `List[T]`)
A list `[T]` (defined in `std/core.ll` as `@lang("list") List[T]`) is a dynamically resizable, heap-allocated contiguous sequence of elements of type $T$.
- Lists are Resource types. Dropping a list drops every element in the list and frees its backing buffer.
- Empty list literal: `[]`.
- Populated list literal: `[1, 2, 3]`.
- Indexing `lst[i]` is bounds-checked at runtime and panics on out-of-bounds access.

#### 3.6.2 Fixed Arrays (`[N: T]` / `Array`)
A fixed array `[N: T]` represents an inline, fixed-size contiguous sequence of exactly $N$ elements of type $T$, where $N$ is a compile-time constant integer $\ge 0$.
- An array carries **zero heap allocation overhead**.
- Category: An array is Plain if $T$ is Plain; it is a Resource if $T$ is a Resource.
- **Repeat constructor:** `[N: value]` initializes $N$ elements with `value` (e.g., `[4: 0]` creates `[0, 0, 0, 0]`). If $N > 1$, $T$ must implement `Copyable`.
- **Explicit element constructor:** `[N: e0, e1, ..., eN-1]` creates an array of size $N$ with explicit comma-separated elements (e.g., `[4: 1, 2, 3, 4]`). A single element `[1: 0]` is valid as either a 1-element array or repeat. Any element count mismatch where $M > 1$ and $M \ne N$ (e.g., `[4: 1, 2]`) is a compile-time error.
- **List constructor:** `[1, 2, 3, 4]` when typed against an expected `[4: int]`.
- **Automatic Sequence View Coercion:** An array `[N: T]` or `&[N: T]` can be passed directly to any function expecting a read-only sequence view `[T]`, enabling zero-cost generic algorithms across fixed and dynamic sequences.

#### 3.6.3 Sets (`#[T]` / `Set[T: Hashable]`)
A set `#[T]` (defined in `std/core.ll` as `@lang("set") Set[T: Hashable]`) is an unordered collection of unique elements of type $T$.
- **Constraint:** Requires $T$ to satisfy the built-in `Hashable` interface (`T: Hashable`, providing `hash(): u64` and equality `==`).
- Sets are Resource types.
- Literal syntax: `#[1, 2, 3]`. Empty set: `#[]` (resolved from expected type context).

#### 3.6.4 Maps (`#[K: V]` / `Map[K: Hashable, V]`)
A map `#[K: V]` (defined in `std/core.ll` as `@lang("map") Map[K: Hashable, V]`) is a hash table associating unique keys of type $K$ with values of type $V$.
- **Constraint:** Requires key type $K$ to satisfy `Hashable` (`K: Hashable`).
- Maps preserve insertion order during iteration.
- Maps are Resource types.
- Literal syntax: `#["a": 1, "b": 2]`. Empty map: `#[]` (resolved from expected type context).
- **Access & Lookup:** Calling `m.get(key)` returns an optional `?V`. Alternatively, map index lookup syntax `m[key]` is syntactic sugar for `m.get(key)` (yielding `?V`, containing the value if present, or `none` if absent).
- **Mutation & Insertion:** Calling `m.insert(key, val)` updates or inserts an entry. Alternatively, assignment syntax `m[key] = val` is syntactic sugar for `m.insert(key, val)`.

### 3.7 Structs & Field Punning

A struct is a user-defined nominal record grouping named fields:

```lowlight
define User struct [
    id: int,
    name: string,
    email?: ?string,
    role: string = "viewer",    // Default field value
]
```

#### Construction & Field Punning
Struct instances are constructed with bracket notation `Type[ ... ]`. When an in-scope variable matches a field name, explicit value assignment may be omitted (field punning):

```lowlight
let id = 1
let name = "Bob"
let email?: ?string = "bob@example.com"

// Full syntax:
let u1 = User[id: id, name: name, email?: email?, role: "admin"]

// Punning syntax:
let u2 = User[id, name, email?]  // role defaults to "viewer"
```

- **Field Defaults & Optionals:** Fields with default initializers or optional types (which default to `none`) may be omitted during construction.
- **Case Sensitivity:** Like all identifiers in Lowlight (variable names, field names, and enum variants), struct type names are strictly case-sensitive. By convention, type names use PascalCase (e.g., `User`, `Node`, `EmptyHeader`).
- **Empty Structs:** Structs declared with zero fields (e.g., `define Empty struct []`) are constructed with empty brackets: `Empty[]`.

### 3.8 Enums: Pure Tag Enums & Tagged Unions

Lowlight supports two distinct enum models:

#### 3.8.1 Flavor A: Pure Tag Enums
A pure tag enum represents a closed set of symbolic scalar constants backed by an integer primitive:

```lowlight
define Color enum [
    Red,
    Green,
    Blue,
]

define Status enum i32 [
    Pending = 0,
    Active = 1,
    Suspended = 10,
    Closed = 11,
]
```

- **Base Type:** Defaults to `int` if omitted; can be explicitly specified as any integer primitive (`i8`–`i64`, `u8`–`u64`).
- **Discriminants:** Auto-increment from `0` or the previous explicit discriminant plus one.
- **Category:** Pure tag enums are always Plain and satisfy `Copyable` and `Hashable`.
- **Conversion:** Pure enums explicitly convert to their underlying integer type via cast syntax: `int(Color.Green)`. Integers do **not** implicitly or explicitly convert to enums to ensure variant validity.

#### 3.8.2 Flavor B: Tagged Unions & Generic Enums
A tagged union represents a value that can take on one of several distinct variant shapes, each optionally carrying data payloads:

```lowlight
define JsonValue enum [
    Null,
    Bool: bool,
    Number: float,
    Str: string,
    Arr: [JsonValue],
    Obj: [
        keys: [string],
        values: [JsonValue],
    ],
]
```

Variants support three shapes:
1. **Unit Variant:** Carries no payload (e.g., `Null`). Constructed as `JsonValue.Null`.
2. **Tuple Variant:** Carries an unnamed payload (e.g., `Number: float`). Constructed as `JsonValue.Number(3.14)`.
3. **Struct Variant:** Carries named fields (e.g., `Obj: [keys: [string], values: [JsonValue]]`). Constructed as `JsonValue.Obj[keys: k, values: v]`.

Tagged unions are Resource types if any variant carries a Resource. They do not have an integer base type, cannot have manual discriminants assigned, cannot be converted to `int`, and cannot be used as map keys. Tagged union variants are inspected and destructured using `match`.

### 3.9 Distinct Types

A distinct type defines a new nominal type wrapping an existing base type without runtime overhead:

```lowlight
define UserId = int
define OrderId = int
```

`UserId` and `OrderId` share the same memory representation as `int`, but cannot be implicitly mixed or compared. Explicit conversion is required: `UserId(42)` or `int(my_user_id)`. Distinct types inherit the value category (Plain vs. Resource) of their underlying base type.

### 3.10 Structural Interfaces & `Self`

An interface specifies a set of method signatures that a type must implement:

```lowlight
define Writer interface [
    write: (&self, data: string): int,
    flush: (&self),
]
```

#### Structural Conformance
Lowlight interfaces are **structural**: a type does not explicitly declare that it implements an interface. If a type provides matching inherent or UFCS methods satisfying all required signatures and receiver modes, it automatically satisfies the interface.

#### The `Self` Type
Inside an interface definition, `Self` refers to the concrete implementing type.

#### Receiver Modes
Methods on an interface may specify three receiver modes:
- `(self)`: Read-only borrow.
- `(&self)`: Exclusive mutable borrow.
- `(own self)`: Ownership consuming.

#### Interface Values
An interface can be used as a dynamically dispatched trait object:
- `Writer`: An owned interface fat pointer (carrying a pointer to the value and a pointer to the compiler-generated virtual method table).
- `&Writer`: A borrowed interface view. Calling `.copy()` on an owned interface duplicates the underlying value via the vtable's copy function.

#### Core Built-in Interfaces (`std/core.ll`)
The standard prelude provides universal interfaces wired to compiler semantics and language operators via `@lang(...)`:
- **`@lang("printable") Printable`**: Universal stringification (`to_string: (self): string`).
- **`@lang("copyable") Copyable`**: Explicit value replication (`copy: (self): Self`).
- **`@lang("drop") Drop`**: Deterministic RAII destruction (`drop: (&self)`).
- **`@lang("hashable") Hashable`**: 64-bit hashing (`hash: (self): u64`). Required by `Set[T: Hashable]` elements and `Map[K: Hashable, V]` keys.
- **`@lang("equatable") Equatable`**: Universal equivalence relation checking (`eq: (self, other: Self): bool`). Backs `==` and `!=`.
- **`@lang("comparable") Comparable`**: Universal total ordering (`cmp: (self, other: Self): int`). Returns `< 0` for Less, `0` for Equal, and `> 0` for Greater. Backs `<`, `<=`, `>`, `>=`. Any type implementing `Comparable` automatically satisfies `Equatable`.
- **`@lang("collectable") Collectable[T]`**: Target for collection accumulators and comprehensions (`push: (&self, item: T)`).
- **`@lang("iterable") Iterable[T]`**: Sequences producing iterators (`iter: (self): Iterator[T]`).
- **`@lang("iterator") Iterator[T]`**: Stateful iteration cursor (`next: (&self): ?T`).

### 3.11 First-Class Functions & Closures

Functions in Lowlight are first-class citizens. Function types are denoted `(ParamTypes): RetType` (or `(ParamTypes)` for functions yielding no value):

```lowlight
bind filter_nums = (nums: [int], predicate: (int): bool): [int] { ... }
```

A function literal (lambda) is written `(params)[: Ret] bodyExpr`, where `bodyExpr` is an executable block `{ ... }` or a single expression:

```lowlight
let is_positive = (x: int): bool x > 0
let add = (a: int, b: int): int { a + b }
```

#### Closures as Resource Types
A function literal that captures variables from its surrounding lexical scope is a closure.
- **Category:** Closures are **Resource types**. A closure value carries an execution entry point, an environment payload, a destructor pointer, and a copy pointer.
- **Capture Rules:**
  - Variables with Plain types are copied into the closure environment.
  - Variables with Resource types are **moved** into the closure environment (invalidating the outer variable).
  - Mutable borrows (`&T`) cannot be captured by closures to prevent dangling references.
  - Captured variables are deeply immutable within the closure body.
- Top-level `bind` functions that capture no environment automatically coerce to function types with zero heap allocation.

### 3.12 Arenas (`Arena`) & Scoped References (`ref[T]`)

An `Arena` is a fast, chunked bump-allocator that manages the lifetime of a collection of objects in a single memory region.

```lowlight
let arena = Arena.new()
let r = arena.alloc(User[id: 1, name: "Alice"])
```

- **`Arena`:** A Resource type. When the `Arena` goes out of scope or is explicitly reset via `arena.reset()`, all memory allocated within it is freed at once. If any allocated objects implement `Drop`, their destructors are executed in LIFO order during arena cleanup.
- **`ref[T]`:** A safe, borrowed reference to a value of type $T$ allocated inside an `Arena`.
- **Scoped Pinning:** Allocating from a local `Arena` pins the arena to that scope. A pinned arena cannot be reset, reassigned, or moved while references derived from it remain active.
- **Escape Prevention:** The type checker strictly prevents `ref[T]` from escaping the lexical scope of its creating `Arena`. A `ref[T]` cannot be returned from a function (unless allocated from a caller-provided `&Arena` parameter), cannot be stored in an outer container, and cannot be captured across scope boundaries.

### 3.13 Raw Pointers (`ptr[T]`)

`ptr[T]` represents an unmanaged, raw memory address. It is a Plain type used exclusively for low-level systems programming and C ABI interoperability.
- Safe Lowlight can create raw pointers (`ptr_null()`, `ptr_from_ref(&x)`), query their addresses, and pass them to foreign functions.
- Dereferencing a raw pointer (`*p`, `*p = val`, `p[i]`) is an unsafe operation and is permitted only inside an `unsafe` block or prefix expression.

### 3.14 Internal & Control Types (`void`, `never`)

- **`void`:** The unit type, indicating the absence of a meaningful value. Blocks ending with a semicolon, functions without an explicit return type, and statements evaluate to `void`.
- **`never`:** The bottom type. Represents computations that never terminate or yield control to their subsequent statement (e.g., `panic()`, infinite loops without breaks, unconditional `<-` unwinds). A subexpression of type `never` is compatible with any expected type.

---

## 4. Program Structure, Declarations, & Modules

### 4.1 Compilation Units & File Resolution

A Lowlight program consists of one or more source files with the `.ll` extension. File paths in `import` statements are resolved relative to the directory of the importing file or against the standard library root.

### 4.2 Order-Independent Top-Level Declarations

Source files contain top-level declarations:
- Constant and function bindings: `bind NAME = expr`
- Method and associated function bindings: `bind TypeName.method = (self, ...) [: Ret] { ... }`
- Distinct types: `define Name = BaseType`
- Struct declarations: `define Name struct [ ... ]`
- Enum declarations: `define Name enum [ ... ]`
- Interface declarations: `define Name interface [ ... ]`
- Foreign FFI declarations: `extern ...`

Top-level declarations are **order-independent**. A constant, type, or function may reference declarations appearing later in the source text or in imported modules.

#### 4.2.1 Method & Associated Function Declarations
Methods are declared at the top level by binding a function to a type name using dot notation (`bind TypeName.method = ...`):

```lowlight
// Inherent method with read-only borrow receiver (self):
bind User.display = (self): string {
    "$self.name <$self.email?>"
}

// Inherent method with mutable borrow receiver (&self):
bind User.set_email = (&self, new_email: string) {
    self.email? = new_email
}

// Inherent method with consuming ownership receiver (own self):
bind User.into_name = (own self): string {
    own self.name
}

// Associated (static) constructor function (omits self receiver):
bind User.new = (id: int, name: string): User {
    User[id, name, email?: none, role: "viewer"]
}
```

- **Receiver Modes:**
  - `(self)`: Read-only borrow. The method borrows the instance immutably.
  - `(&self)`: Exclusive mutable borrow. The method can modify the receiver's fields. The caller's receiver is automatically borrowed by reference (`&`).
  - `(own self)`: Ownership consuming. Consumes the receiver instance, invalidating the caller's variable upon call.
- **Associated Functions:** Functions declared on a type without a `self` receiver (such as `bind Point.origin = (): Point`) act as namespaced functions called as `Point.origin()`.
- **Target Types:** Methods may be attached to structs, enums, distinct types, and primitive types (e.g., `int`, `float`, `string`).
- **Visibility:** Inherent and attached methods are part of the type's inherent API and are automatically visible wherever the type itself is in scope, regardless of whether the defining module was imported openly or with an alias.

#### Constant Initialization & Cycle Detection
Top-level `bind` constants are evaluated at compile time. The compiler performs a topological sort on global constants. If a circular dependency is detected (e.g., constant $A$ depends on $B$, which depends on $A$), compilation fails with a cyclic dependency error. Mutable variables (`let`) are strictly forbidden at the top level.

### 4.3 Modules & Imports

Code is organized across modules using the `import` statement:

```lowlight
import "std/fs.ll"
import "std/math.ll" as math
import "./models/user.ll"
```

- **Open Import (`import "path.ll"`):** Exposes all top-level types, constants, and functions of the imported module directly into the current scope.
- **Aliased Import (`import "path.ll" as name`):** Namespaces the imported symbols under `name` (e.g., `math.sin(math.PI)`).
- **Method Visibility:** Methods attached to structs, enums, and interfaces are automatically visible wherever the type itself is in scope, regardless of whether the module was imported openly or with an alias.

### 4.4 Program Entry Point (`main`)

The execution entry point of a Lowlight program is the `main` function defined at the top level of the primary module:

```lowlight
bind main = () {
    println("Hello, Lowlight!")
}
```

- **Parameters:** `main` must accept exactly zero parameters. Command-line arguments are accessed via the universe function `args(): [string]`.
- **Return Value:** `main` must either omit the return type (yielding no value, exiting with process status code `0`) or return an `int` (used as the process exit code). Note that `void` is strictly an internal compiler type and cannot be written in user code.

### 4.5 Compiler Attributes

Attributes apply compiler metadata and layout directives to declarations using the `@name` or `@name(...)` syntax:

| Attribute | Target | Description |
| :--- | :--- | :--- |
| `@inline` | Function, Method | Instructs the compiler to inline the function body directly at call sites |
| `@noinline` | Function, Method | Inhibits inlining, ensuring the function is emitted as a distinct subroutine |
| `@must_use` / `@nodiscard` | Function, Struct | Emits a compile error if the returned value is discarded |
| `@deprecated` | Declaration | Emits a compile warning upon use |
| `@deprecated("msg")` | Declaration | Emits a compile warning with a custom deprecation message |
| `@packed` | Struct | Suppresses field alignment padding (byte-packed layout) |
| `@doc("...")` | Declaration | Attaches documentation strings accessible to tooling and LSP |
| `@lang("...")` | Declaration | Binds a declaration to an internal compiler-known language hook |

#### Attribute Definitions
- **Function Inlining (`@inline` and `@noinline`):**
  - **Inlining** is a compiler optimization where the machine instructions or body of a function are substituted directly at each call site rather than generating a call instruction / jump. This eliminates function call overhead (such as stack frame allocation and register saves) at the cost of potential executable size expansion.
  - `@inline` directs the compiler to always inline the annotated function or method whenever possible.
  - `@noinline` instructs the compiler to never inline the function, preserving it as an independent subroutine (useful for cold error handlers or stack trace readability).
- **Packed Struct Layout (`@packed`):**
  - In standard structs, compilers automatically insert unused **alignment padding bytes** between fields to align each data member to natural machine word boundaries (e.g., aligning 64-bit integers to 8-byte boundaries) for hardware efficiency.
  - `@packed` disables all alignment padding. The compiler arranges fields consecutively in memory byte-for-byte. This layout is critical when modeling hardware registers, binary network packet headers, or serialized disk structures.
- **Compiler-Known Hooks (`@lang("...")`):**
  - A **compiler hook** is an internal mechanism that links a user-space declaration with core language semantics and runtime behaviors. Lowlight does not hard-code standard library names into the compiler; instead, declarations marked with `@lang` inform the compiler how to synthesize operations:
    - `@lang("printable")`: Designates the interface whose `.to_string(): string` method is invoked during string interpolation (`${expr}`) and universe printing (`print`/`println`).
    - `@lang("copyable")`: Designates the interface providing the `.copy(): Self` method for deep duplication.
    - `@lang("drop")`: Designates the interface whose `.drop(&self)` method is automatically invoked during deterministic LIFO destruction.
    - `@lang("panic")` and `@lang("assert")`: Bind the universe error and assertion functions.

### 4.6 Implicit Standard Prelude

Every Lowlight compilation unit automatically imports the core standard prelude (`std/core.ll`). The prelude defines essential language interfaces and universe functions:

```lowlight
@lang("printable")
define Printable interface [
    to_string: (self): string,
]

@lang("copyable")
define Copyable interface [
    copy: (self): Self,
]

@lang("drop")
define Drop interface [
    drop: (&self),
]
```

The prelude also supplies primitive `.to_string()` implementations for all basic types, the canonical `Result[T, E]` type, and universe math functions (`min`, `max`, `abs`, `clamp`).

---

## 5. Ownership, Borrowing, & Lifetime Mechanics

Lowlight enforces memory safety through an affine ownership model verified at compile time.

### 5.1 Affine Value Ownership

Every value in a Lowlight program has a single, unique owner at any point during execution.
- If a value has a Plain type, copying the value produces an independent duplicate. Both copies are valid and operate autonomously.
- If a value has a Resource type, assignment or transfer transfers ownership (a **move**). Once moved, the source binding becomes uninitialized and inert; any subsequent attempt to read, pass, or mutate the moved variable is a compile error.

```lowlight
let s1 = "Hello, world!"
let s2 = own s1          // s1 is moved to s2; s1 can no longer be accessed
// println(s1)           // Compile Error: use of moved value `s1`
```

### 5.2 Value Moves & The Explicit `own` Qualifier

To ensure that resource transfers are obvious during code review, Lowlight requires the `own` keyword when moving an existing owned Resource variable into:
1. A new `let` or `bind` variable;
2. An assignment target;
3. A struct field or enum variant payload;
4. A collection literal;
5. A function argument expecting an `own` parameter.

```lowlight
let u = User[id: 1, name: "Alice"]
let list = [own u]       // Explicit `own` required to move existing local
```

#### Implicit Move Exceptions
Rvalues (temporary expressions that have no persistent binding name), function return tail expressions, and `<-` unwinds move implicitly without requiring `own`:

```lowlight
bind make_user = (): User {
    let u = User[id: 2, name: "Bob"]
    u                    // Valid: tail return moves implicitly
}
```

### 5.3 Parameter Passing Modes (`T`, `&T`, `own T`)

Lowlight provides three parameter modes for function and method signatures:

```
                      +---------------------------------------+
                      |           Parameter Modes             |
                      +---------------------------------------+
                      |                                       |
       +--------------+--------------+         +--------------+--------------+
       |                             |         |                             |
+--------------+              +--------------+ |                      +--------------+
| Read-Only: T |              | Mutable: &T  | |                      | Transfer:    |
| (Default)    |              |              | |                      | own T        |
+--------------+              +--------------+ |                      +--------------+
| Borrows value|              | Borrows with | |                      | Takes unique |
| for reading; |              | exclusive    | |                      | ownership;   |
| caller keeps |              | mutation;    | |                      | caller drops |
| ownership.   |              | caller writes| |                      | rights.      |
| Plain copied;|              | explicit `&` | |                      | Caller writes|
| Resource     |              | at call site.| |                      | explicit     |
| borrowed.    |              +--------------+ |                      | `own` local. |
+--------------+                               +----------------------+--------------+
```

1. **Read-Only Borrow (`p: T`):** The default parameter mode. The function borrows the value for inspection. If $T$ is Plain, the value is passed by bitwise copy. If $T$ is a Resource, the compiler automatically passes an immutable reference. The caller retains ownership.
2. **Mutable Borrow (`p: &T`):** Grants the function exclusive, mutable access to the caller's variable for the duration of the call. **The caller must explicitly write `&` at the call site** (e.g., `update(&state)`).
3. **Ownership Transfer (`p: own T`):** The function consumes the value and assumes full responsibility for its lifecycle. The caller relinquishes ownership.

### 5.4 Borrow Exclusivity & Alias Invariants

To eliminate data races, iterator invalidation, and dangling pointers, Lowlight enforces two borrow exclusivity invariants at every call site:

#### Rule 1: No Multiple Mutable Aliases
A mutable place cannot be borrowed mutably more than once in the same expression:

```lowlight
// Suppose `modify` has signature: (a: &State, b: &State)
modify(&state, &state)   // Compile Error: cannot borrow `state` mutably more than once
```

#### Rule 2: No Read/Write Aliasing
A mutable place cannot be read or moved while simultaneously borrowed mutably in the same expression:

```lowlight
// Suppose `update_and_check` has signature: (s: &State, val: State)
update_and_check(&state, state) // Compile Error: cannot read `state` while mutably borrowed
```

*(Note: If `val` is a Plain copy evaluated prior to the call, read access is permitted; if `state` is a Resource or passed as a view, it is rejected.)*

### 5.5 Deterministic Destruction & Custom `Drop`

When a lexical scope exits (via natural block completion or an explicit `<-` targeted unwind), all Resource variables declared within that scope that have not been moved are destroyed in **reverse order of declaration (LIFO)**.

#### Struct Destruction
When a struct is dropped:
1. If the struct implements the `Drop` interface, its `.drop(&self)` method is executed first.
2. All member fields of the struct are dropped in reverse order of declaration.

#### Enum Destruction
When an enum instance is dropped, if the active variant carries a payload, that payload's destructor is executed. If a `match` arm performs a partial move on one variant field, all unmoved sibling fields are dropped immediately, and the enum is marked inert.

#### Global Destruction
Top-level constants holding Resource values are destroyed at program exit in reverse order of initialization.

### 5.6 Scoped Arena Pinning & Escape Analysis

Lowlight permits fast region allocation via `Arena` without risking dangling pointers:
1. When `arena.alloc(value)` is invoked, `arena` is **pinned** to the current lexical scope depth.
2. While pinned, `arena.reset()`, reassigning `arena`, or moving `own arena` (including capturing it in a closure) is a static compile error.
3. A `ref[T]` reference derived from an `Arena` cannot escape the scope of that `Arena`. Specifically:
   - A `ref[T]` cannot be returned from a function unless the function borrows an external `&Arena` parameter.
   - A `ref[T]` cannot be stored into a vector, map, or struct located in an outer scope.
   - A `ref[T]` cannot be assigned to an outer variable.
4. Once the inner scope exits and all references derived from the allocation expire, the arena unpins automatically.

---

## 6. Expressions & Evaluation Semantics

Lowlight is an expression-oriented language: almost every construct evaluates to a value.

### 6.1 Operator Precedence & Associativity

The following table defines operator precedence from highest (binding tightest) to lowest:

| Level | Operator | Description | Associativity |
| :---: | :--- | :--- | :---: |
| **12** | `.` `?.` `()` `[]` | Member access, optional chaining, call, index | Left-to-right |
| **11** | `-` `not` `~` `*` `&` `unsafe` | Unary negation, logical not, bitwise not, pointer deref, borrow, unsafe prefix | Right-to-left |
| **10** | `as` | Type casting / unwrapping | Left-to-right |
| **9** | `*` `/` `%` | Multiplicative arithmetic | Left-to-right |
| **8** | `+` `-` | Additive arithmetic | Left-to-right |
| **7** | `<<` `>>` | Bitwise shift left, shift right | Left-to-right |
| **6** | `<` `<=` `>` `>=` | Relational comparisons | Non-associative |
| **5** | `==` `!=` | Structural equality and inequality | Non-associative |
| **4** | `&` | Bitwise AND | Left-to-right |
| **3** | `^` | Bitwise XOR | Left-to-right |
| **2** | `\|` | Bitwise OR | Left-to-right |
| **1** | `and` | Short-circuit logical AND | Left-to-right |
| **0** | `or` | Short-circuit logical OR | Left-to-right |
| **-1** | `??` | Optional null-coalescing fallback | Left-to-right |
| **-2** | `<-` | Lexical unwind (return, break, continue) | Right-to-left |

Subexpressions are strictly evaluated from left to right.

### 6.2 Arithmetic Semantics & Boundary Traps

Lowlight enforces mathematical predictability across all compilation modes (including debug and `--release`):

#### Two's Complement Integer Arithmetic
Addition (`+`), subtraction (`-`), multiplication (`*`), and unary negation (`-`) operate with standard two's complement wrapping arithmetic across all integer widths (`i8`–`i64`, `u8`–`u64`). Integer overflow does not cause undefined behavior.

#### Checked Division & Modulo
- Dividing or taking the remainder by zero (`x / 0`, `x % 0`) **always deterministically traps and terminates the process with a safety panic**.
- Minimum integer division wrapping: For signed types, dividing the minimum representable value by `-1` (`MIN_INT / -1`) wraps to `MIN_INT`, and `MIN_INT % -1` evaluates to `0`.

#### Checked Bitwise Shifts
Bitwise shift operations (`<<`, `>>`) require the right-hand shift operand $S$ to satisfy $0 \le S < \text{bit\_width}$. If $S < 0$ or $S \ge \text{bit\_width}$, the operation **always deterministically traps**.

### 6.3 Equality & Comparison

- **`==` and `!=` (Equality):** Checked via the `Equatable` interface:
  - Primitives (`int`, `float`, `char`, `bool`, fixed-width integers) lower directly to native C equality operators (`((a) == (b))`) with zero runtime overhead.
  - Strings compare byte-by-byte via `ll_str_eq(a, b)` (or `.eq(other)`).
  - Custom structs and user types satisfy equality by implementing `.eq(self, other: Self): bool` or `.cmp(self, other: Self): int` (where `a == b` $\iff$ `a.cmp(b) == 0`).
  - Structural equality is automatically synthesized for Plain structs, tagged enums, optionals, and distinct types whose fields satisfy equality.
  - Generic parameters bounded by `[T: Equatable]` or `[T: Comparable]` permit `==` and `!=`.
- **`<`, `<=`, `>`, `>=` (Ordering):** Checked via the `Comparable` interface:
  - Primitives (`int`, `float`, `char`, fixed-width integers) lower directly to native C relational operators (`((a) < (b))`) with zero runtime overhead.
  - Strings compare lexicographically by byte values via `ll_str_cmp(a, b)` (or `.cmp(other)`).
  - Custom structs and user types satisfy ordering by implementing `.cmp(self, other: Self): int` (returning negative for `<`, zero for `==`, and positive for `>`).
  - Generic parameters bounded by `[T: Comparable]` permit relational operators (`<`, `<=`, `>`, `>=`).
  - Types that do not implement `Comparable` (such as `bool` or unorderable structs) cannot be used with relational operators and produce a compile-time diagnostic error.
- **Prelude Order Utilities (`min`, `max`, `clamp`):**
  - `min[T: Comparable + Copyable](a: T, b: T): T`: Returns the smaller value.
  - `max[T: Comparable + Copyable](a: T, b: T): T`: Returns the larger value.
  - `clamp[T: Comparable + Copyable](val: T, low: T, high: T): T`: Clamps `val` within `[low, high]`.


### 6.4 Indexing & Slicing

#### Indexing
Collection indexing `c[i]` applies to dynamic lists `[T]`, fixed arrays `[N: T]`, strings, maps `#[K: V]`, and raw pointers `ptr[T]`:
- **Lists (`[T]`) & Arrays (`[N: T]`):** `c[i]` yields the element at index $i$ (or allows mutable in-place assignment `c[i] = val` when `c` is a mutable place). The index $i$ must satisfy $0 \le i < \text{len}$. Out-of-bounds access **deterministically traps with a safety panic**.
- **Strings (`string`):** `s[i]` yields the raw `u8` byte at byte offset $i$ (bounds checked: $0 \le i < \text{byte\_len}$). Traps on out-of-bounds access.
- **Maps (`#[K: V]`):** `m[key]` looks up `key` in map `m` and yields an optional `?V` (syntactic sugar for `m.get(key)`). If `key` is present in `m`, `m[key]` evaluates to the corresponding value wrapped in `?V`; if absent, it evaluates to `none`. Assigning to a map index `m[key] = val` is syntactic sugar for `m.set(key, val)`.
- **Raw Pointers (`ptr[T]`):** In `unsafe` expressions, `p[i]` performs raw pointer arithmetic and dereferences the memory location at address $p + i \times \text{sizeof}(T)$.

#### Slicing
Slicing extracts a contiguous sub-range view from a vector, array, or string:
- `c[lo..hi]`: Half-open range $[lo, hi)$.
- `c[lo..=hi]`: Closed range $[lo, hi]$.
- Omitting `lo` defaults to `0`; omitting `hi` defaults to `c.len()`.
- Slicing validates that $0 \le lo \le hi \le \text{len}$ at runtime; invalid ranges trap deterministically.
- Slicing returns a borrowed sequence view without copying heap memory.

### 6.5 Member Access, UFCS, & Receiver Auto-Borrowing

Member access uses the dot operator `.` (`object.member`).

#### Uniform Function Call Syntax (UFCS)
If an expression `recv.method(args)` is called, the compiler resolves the call in the following priority order:
1. Inherent methods defined directly on the type of `recv`.
2. Interface methods implemented by the type of `recv`.
3. Standalone functions in scope whose first parameter matches the type of `recv`: `method(recv, args)`.

#### Receiver Auto-Borrowing
When calling a method with receiver `(&self)` on a mutable place, the compiler automatically borrows the receiver by reference (`&recv`). The caller is not required to write `(&recv).method()`.

### 6.6 Optional Chaining & Coalescing (`?.`, `??`)

#### Optional Chaining (`?.`)
Accessing a member or method on an optional base `?T` requires optional chaining syntax `?.` (or a dot following an `OptIdent`):

```lowlight
let user?: ?User = find_user(id)
let name?: ?string = user?.name
let length?: ?int = user?.name.len()  // Chain short-circuits to none if user? is none
```

- If the base evaluates to `none`, the entire chained expression immediately short-circuits and evaluates to `none`.
- Once an optional chain is initiated, subsequent standard dot accesses continue short-circuiting.
- Calling a method that requires `(&self)` or `(own self)` over an optional chain `?.` is a compile error.

#### Optional Null-Coalescing (`??`)
The fallback operator `??` unwraps an optional value or provides a default:

```lowlight
let display_name: string = user?.name ?? "Guest"
```

The right-hand operand is evaluated lazily only when the left-hand operand evaluates to `none`.

### 6.7 Function Calls & Closures

Functions and closures are called using parentheses `f(arg1, arg2)`. Arguments are evaluated strictly from left to right.
- Arguments matching `&T` parameters must be explicitly prefixed with `&`.
- Arguments moving existing Resource locals into `own T` parameters must be explicitly prefixed with `own`.

---

## 7. Statements & Control Flow

### 7.1 Lexical Blocks & Block Value Rules

A block `{ ... }` groups a sequence of declarations and expressions into a distinct lexical scope.

```lowlight
let result = {
    let a = 10
    let b = 20
    a + b                // Block evaluates to 30
}
```

- **Tail Value:** The value of a block is the value of its final expression, provided it is not followed by an explicit semicolon.
- **Void Blocks:** A block ending with an explicit semicolon (`;`) evaluates to `void`.

### 7.2 Variable Declarations (`let` vs. `bind`)

Lowlight provides two binding forms:
- **`let`:** Declares a mutable variable. The variable may be reassigned via `=`.
- **`bind`:** Declares an immutable constant. Once initialized, the binding cannot be reassigned or mutated. Top-level variables must use `bind`.

```lowlight
let x = 10
x = 20                   // Valid

bind y = 100
// y = 200               // Compile Error: cannot reassign immutable binding `y`
```

### 7.3 Assignment & Compound Mutation

Assignment (`=`) and compound operators (`+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`, `<<=`, `>>=`) update mutable places:
- The left-hand target must be a mutable place (a `let` variable, a field of a mutable struct, or an indexed element of a mutable vector or array).
- Compound assignment `a += b` is semantically equivalent to `a = a + b`, evaluating the place `a` once.

### 7.4 Conditional Branches (`if` / `else`) & Optional Unwrapping

An `if` statement evaluates a condition and selects a branch:

```lowlight
let message = if (score >= 50) "Pass" else "Fail"
```

#### Evaluation Rules
When an `if` construct is evaluated as a value expression:
- **With `else` Branch (Type `T`):** Both branches must evaluate to compatible types, and the `if` expression evaluates to their unified type `T`.
- **Without `else` Branch (Type `?T`):** If the `else` branch is omitted, the `if` expression evaluates to an optional `?T` of the `then` branch value. If the condition is true, it yields the evaluated value wrapped in `?T`; if false, it evaluates to `none`:
  ```lowlight
  let bonus?: ?int = if (completed_on_time) 500
  ```
When an `if` expression is used in statement position (not assigned to a variable or yielded from a block), the branches execute purely for side effects and evaluate to `void`.

#### 7.4.1 Optional Unwrapping in `if`
Lowlight provides concise, type-narrowing unwrapping directly inside `if` conditions:

##### Form A: Direct Optional Variable Narrowing
```lowlight
let opt?: ?string = get_name()
if (opt?) {
    // Inside this block, `opt` is narrowed to non-optional `string`
    println("Hello, " + opt)
}
```

##### Form B: Mutable Optional Borrow
```lowlight
if (&opt?) {
    // Inside this block, `opt` is narrowed to mutable `&string`
    opt.push_str(" (verified)")
}
```

##### Form C: Expression Binding Unwrapping (`as`)
```lowlight
if (get_user() as u) {
    // `u` is bound to the unwrapped, non-optional User value
    println(u.name)
}

if (&get_user() as u) {
    // `u` is bound as a mutable borrow `&User`
}
```

##### Value Yielding from Optional Unwraps
Like boolean `if` expressions, optional unwrapping `if` expressions can produce values (evaluating to `T` with `else`, or `?T` without `else`). The resulting value does not need to be captured if executed purely for side effects:

```lowlight
// Used as a value expression:
let display_name: string = if (opt?) "User: $opt" else "Anonymous"

// Used as a statement (value not captured):
if (opt?) {
    println("Welcome back, $opt")
}
```

### 7.5 Loops: Scalar Loops (`while`) & Collection Loops (`for`)

Lowlight distinguishes between two distinct loop forms based on their evaluated output:
- **`while`**: Evaluates to a **scalar** value (`T` with `else`, or `?T` without `else`).
- **`for`**: Evaluates to a **collection** satisfying `Collectable[T]` (defaulting to dynamic vector `[T]`).

Both forms share the same loop headers: boolean conditions (`(cond)`), integer ranges (`(i in lo..hi)`), and collection iterators (`(x in collection)`).

#### 7.5.1 Scalar Loops (`while`)
The `while` loop construct evaluates to a scalar:
```lowlight
while (<condition> | <identifier> in <iterable>) [: <evaluation_type>] <body> [else <else_body>]
```

1. **Scalar Value Semantics:**
   - With `else`: Evaluates to `T` (scalar guaranteed). If zero iterations run, or no break executes, evaluates to `else_body`.
   - Without `else`: Evaluates to `?T` (scalar optional). If zero iterations run, evaluates to `none`.
   - Infinite Loops: `while(true): T <expr>` evaluates to `T` directly without requiring an `else` clause.
2. **Early Exit (`while <- val`):**
   Executing `while <- val` immediately terminates the loop and evaluates the entire `while` expression to `val`.
3. **Examples:**
   ```lowlight
   let res1? = while (true) 1
   let res2: int = while (true): int 1
   let found: bool = while (x in 1..10): bool {
       if (x == 5) while <- true
   } else false
   ```
4. **Statement Position:**
   When placed in void or statement position, `while` executes purely for side effects without allocating.

#### 7.5.2 Collection Loops & Comprehensions (`for`)
The `for` loop construct evaluates to a collection:
```lowlight
for (<condition> | <identifier> in <iterable>) [: <evaluation_type>: Collectable] <body>
```

1. **Collection Value Semantics:**
   - Evaluates to `T: Collectable` where each iteration's body expression is pushed into the collection via `.push()`.
   - Default Collection: If no type annotation is provided, `for` evaluates to dynamic List `[Elem]` with element type inferred from the body.
   - List Shorthand: `: []` explicitly specifies a dynamic list whose element type is inferred from the body.
   - Specific Collections: Any type implementing `Collectable` (e.g. `#[int]` or `#[string: int]`) can be specified.
   - Non-Collectable Error: Annotating with a non-`Collectable` type (e.g., `for (true): int`) is a compile-time error (`type int does not implement Collectable`).
2. **Filtering (`next <-`):**
   Calling `next <-` inside the loop skips the rest of the current iteration without adding anything to the collection.
3. **Early Break (`for <-`):**
   Calling `for <-` terminates the loop early and yields the collection accumulated up to that point.
4. **Examples:**
   ```lowlight
   bind v = for (x in 1..=4) x              // [1, 2, 3, 4]
   let v2 = for (x in 0..10): [] x          // [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]

   let evens = for (x in 0..10): [] {
       if (x % 2 != 0) next <-              // Filters out odds
       x
   }                                        // [0, 2, 4, 6, 8]

   let s = for (x in 0..5): #[int] (x % 3)  // #[0, 1, 2]
   ```
5. **Statement Position:**
   When a `for` loop is placed in statement or void position, it executes purely for side effects without allocating any collection.

#### 7.5.3 Collection Iteration & Uniform Map Interface
Lowlight provides the universal `Iterable[T]`, `Iterator[T]`, and `Collectable[T]` interfaces in `std/core.ll`.

All built-in collections (`[T]`, `[N: T]`, `string`, `#[T]`, and `#[K: V]`) implement `Iterable[T]` and `Collectable[T]`:
1. **Single-Variable Map Iteration:**
   Maps yield key-value pairs wrapped in the standard `MapEntry[K, V]` struct (`[key: K, value: V]`):
   ```lowlight
   for (entry in my_map) {
       println(entry.key)
       println(entry.value)
   }
   ```
2. **Two-Variable Map Iteration:**
   For backward compatibility and convenience, two-variable iteration `for (k, v in my_map)` remains fully supported.
3. **Map Collection via `push`:**
   Maps implement `Collectable[MapEntry[K, V]]` via `my_map.push(entry)` and can be constructed in comprehensions:
   ```lowlight
   let doubled = for (entry in my_map): #[string: int] {
       MapEntry[string, int][key: entry.key.copy(), value: entry.value * 2]
   }
   ```

### 7.6 Pattern Matching (`match` / `when`)

Lowlight provides a clean, **container-free** pattern matching expression delimited by `match (scrutinee)` and terminated strictly by an `else:` or `else Pattern:` arm:

```lowlight
let label = match (status)
    when Status.Pending: "Waiting"
    when Status.Active: "Running"
    when Status.Suspended: "Paused"
    else: "Unknown"
```

Notice that `match` does **not** use enclosing curly braces `{}`. Arms begin with `when`, followed by a pattern and a colon `:`, followed by an action body (which can be a single expression or a `{ ... }` block).

#### 7.6.1 Variant Destructuring
`match` destructures tuple and struct variants of tagged unions:

```lowlight
match (expr)
    when Expr.Num(n): println("Number: $n")
    when Expr.Add(a, b): println("Addition")
    else Expr.Var[name, id: local_id]: println("Var: $name #$local_id")
```

#### 7.6.2 Partial Moves & Sibling Drops
When matching on an owned tagged union, an arm may move out specific variant fields using `own` (e.g., `when Msg.Data[own payload, id]: ...`). In this case:
1. The moved field `payload` is transferred to the arm scope.
2. All unmoved Resource fields (such as `id` if Resource) are **immediately and deterministically dropped**.
3. The parent enum instance is marked moved and inert.
4. Mixing `&` borrows and `own` moves in the same pattern is a compile error.

#### 7.6.3 Exhaustiveness & The Mandatory `else` Arm
Every `match` expression must be exhaustive and **must terminate with an `else` arm**, guaranteeing unambiguous end-of-match resolution even when nested:
- **Open Fallback (`else:`):** Matches any remaining values with a default action:
  ```lowlight
  let label = match (code)
      when 200: "OK"
      when 404: "Not Found"
      else: "Other"
  ```
- **Exhaustive Variant Arm (`else Pattern:`):** When all possible variants of an enum are handled explicitly, the final variant is placed on the `else` arm (`else Status.Closed: 300`). The compiler statically verifies that every variant is covered.

### 7.7 Targeted Lexical Unwinding (`<-`)

Lowlight eliminates traditional ambiguous keywords (`break`, `continue`, `return`) in favor of the unified, targeted **unwind operator** `<-`.

```
UnwindTarget = "<-" [ Expr ]
             | "if" "<-" [ Expr ]
             | "match" "<-" [ Expr ]
             | "for" "<-" [ Expr ]
             | "next" "<-" [ Expr ]
             | Ident "<-" [ Expr ]
             | "next" Ident "<-" [ Expr ]
             | "func" "<-" [ Expr ]
             ;
```

#### The 8 Unwind Forms

| Syntax | Target Construct | Action |
| :--- | :--- | :--- |
| `<- [expr]` | Enclosing lexical block `{}` | Breaks from the immediately enclosing block, yielding `expr` |
| `if <- [expr]` | Enclosing `if` expression | Breaks out of the surrounding `if` construct with `expr` |
| `match <- [expr]` | Enclosing `match` expression | Breaks out of the surrounding `match` construct with `expr` |
| `for <- [expr]` | Enclosing `for` loop | Breaks from the loop, yielding `expr` as the loop result |
| `next <- [expr]` | Enclosing `for` loop | Skips to the next loop iteration (continue), optionally yielding `expr` |
| `<label> <- [expr]` | Block or loop labeled `<label>` | Breaks out of the labeled construct across nested blocks |
| `next <label> <- [expr]` | Loop labeled `<label>` | Skips to the next iteration of the outer labeled loop |
| `func <- [expr]` | Enclosing function | Early return from the function, yielding `expr` |

#### Boundary & Safety Invariants
1. **No Closure Boundary Crossing:** An unwind statement **cannot cross a function or lambda boundary**. A `func <-` inside a closure returns from that closure, not the outer enclosing function.
2. **LIFO Drop Guarantee:** When an unwind statement transfers control outward across nested scopes, the compiler guarantees that all local Resource variables in all bypassed scopes are dropped in strict reverse declaration order before control resumes at the target.
3. **The `?? <unwind>` Guard Pattern:** Because unwinds are expressions of type `never`, an unwind may be used directly on the right-hand side of a `??` coalescing operator:

```lowlight
let user = find_user(id) ?? func <- Err("User not found")
// Beyond this line, `user` is guaranteed to be present and non-optional
```

---

## 8. Generics & Monomorphization

Lowlight supports parameterized polymorphism across structs, enums, functions, and methods.

### 8.1 Generic Declarations

Generic parameters are declared inside brackets `[...]`:

```lowlight
define Stack[T] struct [
    items: [T],
]

bind swap[T] = (a: &T, b: &T) {
    let tmp = own *a
    *a = own *b
    *b = own tmp
}
```

### 8.2 Interface Constraints & Bounds

Generic type parameters can be constrained by one or more interfaces using the `:` syntax:

```lowlight
bind print_all[T: Printable + Copyable] = (items: [T]) {
    for (item in items) {
        println(item.to_string())
    }
}
```

Inside the body of `print_all`, values of type `T` are guaranteed to provide all methods specified by `Printable` and `Copyable`.

### 8.3 The Strictly Non-Optional Generic Rule

To preserve the invariant that optional types do not nest (`??T` is impossible), Lowlight enforces that **all generic type parameters `T` strictly represent non-optional types**.
- Instantiating a generic type with an optional (e.g., `Stack[?int]`) is a static compile error.
- To store optional values inside a generic data structure, the generic declaration itself must explicitly write `?T`:

```lowlight
define Container[T] struct [
    item?: ?T,               // Valid: represents optional T
]
```

### 8.4 Type Inference & Monomorphization Limits

- **Inference:** When calling a generic function or constructing a generic variant, type arguments may be omitted if they can be uniquely inferred from call arguments or expected return types.
- **Full Monomorphization:** Lowlight compiles generic code via static monomorphization. Each distinct combination of concrete type arguments generates specialized machine representation.
- **Recursion Guard:** To prevent compiler infinite loops from recursively expanded generic types (e.g., `struct Box[T] { next: Box[Box[T]] }`), the compiler enforces a maximum monomorphization depth of **128**. Exceeding this limit halts compilation with an instantiation depth error.

---

## 9. Standard Library Contract & Built-in Operations

Conforming Lowlight implementations provide a built-in standard runtime contract along with native standard library modules implemented directly in Lowlight.

### 9.1 Built-in Primitive Methods & Character Module (`std/chars.ll`)

All primitive types provide canonical conversion and formatting methods:
- `int.to_string(): string`
- `float.to_string(): string`
- `bool.to_string(): string`
- `char.to_string(): string`

The `std/chars.ll` module provides comprehensive character classification, ASCII case conversion, radix conversions, and UTF-8 encoding measurements:

#### Character Inherent Methods
- `ch.is_digit(): bool`: Returns `true` if `ch` is an ASCII decimal digit (`'0'..'9'`).
- `ch.is_alpha(): bool`: Returns `true` if `ch` is an ASCII letter (`'a'..'z'` or `'A'..'Z'`).
- `ch.is_alnum(): bool`: Returns `true` if `ch` is alphanumeric.
- `ch.is_whitespace(): bool`: Returns `true` if `ch` is ASCII whitespace (`' '`, `'\t'`, `'\n'`, `'\r'`, `0x0B`, `0x0C`).
- `ch.is_ascii(): bool`: Returns `true` if `ch <= 0x7F`.
- `ch.is_ascii_upper(): bool`: Returns `true` if `ch` is an uppercase ASCII letter.
- `ch.is_ascii_lower(): bool`: Returns `true` if `ch` is a lowercase ASCII letter.
- `ch.is_control(): bool`: Returns `true` if `ch` is an ASCII control character (`< 32` or `127`).
- `ch.is_punctuation(): bool`: Returns `true` if `ch` is ASCII punctuation.
- `ch.to_ascii_upper(): char`: Converts ASCII lowercase to uppercase; non-ASCII/uppercase characters unchanged.
- `ch.to_ascii_lower(): char`: Converts ASCII uppercase to lowercase; non-ASCII/lowercase characters unchanged.
- `ch.to_digit(radix: int): ?int`: Parses `ch` as a digit in radix $2 \le \text{radix} \le 36$; returns `none` if invalid.
- `ch.len_utf8(): int`: Returns the UTF-8 byte length required to encode `ch` (1 to 4 bytes).

#### Character Static & Free Functions
- `char.from_digit(digit: int, radix: int): ?char`: Converts numeric value into character digit.
- Standalone free functions: `chars.is_digit(ch)`, `chars.is_alpha(ch)`, `chars.is_alnum(ch)`, `chars.is_whitespace(ch)`, `chars.to_ascii_upper(ch)`, `chars.to_ascii_lower(ch)`, `chars.to_digit(ch, radix)`, `chars.from_digit(digit, radix)`.

### 9.2 String Built-ins & Standard Strings Module (`std/strings.ll`)

The `string` type represents an immutable UTF-8 text sequence. The standard library module `std/strings.ll` provides a complete suite of string methods:

#### Inherent String Operations
- `s.len(): int`: Returns Unicode scalar count.
- `s.byte_len(): int`: Returns UTF-8 byte count.
- `s.byte_at(i: int): u8`: Returns the byte at index `i` (bounds-checked).
- `s.is_empty(): bool`: Returns `true` if `s.byte_len() == 0`.
- `s.is_whitespace(): bool`: Returns `true` if all characters are whitespace.
- `s.is_ascii(): bool`: Returns `true` if all characters are ASCII.

#### Search & Inspection
- `s.starts_with(prefix: string): bool`
- `s.ends_with(suffix: string): bool`
- `s.contains(sub: string): bool`
- `s.index_of(sub: string): ?int`: Returns start index of first occurrence of `sub`, or `none`.
- `s.last_index_of(sub: string): ?int`: Returns start index of last occurrence of `sub`, or `none`.
- `s.count(sub: string): int`: Returns non-overlapping occurrence count of `sub`.

#### Trimming
- `s.trim(): string`: Trims leading and trailing whitespace.
- `s.trim_start(): string`: Trims leading whitespace.
- `s.trim_end(): string`: Trims trailing whitespace.

#### Case & Transformation
- `s.to_lower(): string`: Returns lowercase string (also `to_lowercase()`).
- `s.to_upper(): string`: Returns uppercase string (also `to_uppercase()`).
- `s.capitalize(): string`: Capitalizes first character and lowercases remainder.
- `s.repeat(n: int): string`: Repeats string `n` times.
- `s.reverse(): string`: Reverses character order.
- `s.replace(old: string, new: string): string`: Replaces first occurrence of `old` with `new`.
- `s.replace_all(old: string, new: string): string`: Replaces all occurrences of `old` with `new`.

#### Padding & Formatting
- `s.pad_left(width: int, pad_ch: char = ' '): string`
- `s.pad_right(width: int, pad_ch: char = ' '): string`
- `s.center(width: int, pad_ch: char = ' '): string`

#### Splitting, Joining & Conversions
- `s.split(delim: string): [string]`: Splits `s` by `delim`.
- `s.split_whitespace(): [string]`: Splits `s` by contiguous whitespace runs.
- `s.lines(): [string]`: Splits `s` into lines across `\n` and `\r\n`.
- `s.chars_list(): [char]`: Returns an owned list of Unicode `char` scalars.
- `s.bytes_list(): [u8]`: Returns an owned list of raw `u8` bytes.
- `strings.join(parts: [string], sep: string): string`: Joins list of strings with `sep`.

### 9.3 Collection Operations & Sorting (`std/sort.ll`)

#### Dynamic Lists (`[T]` / `List[T]`)
- `lst.len(): int`: Element count.
- `lst.cap(): int`: Allocated capacity.
- `lst.push(val: own T)`: Appends `val` to the list.
- `lst.pop(): ?T`: Removes and returns the last element, or `none` if empty.
- `lst.get(i: int): ?T`: Retrieves element at index `i` as optional `?T`.
- `lst.clear()`: Drops all elements and resets length to zero.

#### Sets (`#[T]` / `Set[T: Hashable]`)
- `s.len(): int`
- `s.insert(val: own T): bool`: Inserts `val`; returns `true` if inserted, `false` if already present.
- `s.contains(val: T): bool`
- `s.remove(val: T): bool`
- `s.clear()`

#### Maps (`#[K: V]` / `Map[K: Hashable, V]`)
- `m.len(): int`
- `m.get(key: K): ?V`: Returns a read-only borrow of the value for `key`, or `none`.
- `m.has(key: K): bool`
- `m.set(key: own K, val: own V): ?V`: Inserts or updates the entry; returns prior value if replaced.
- `m.remove(key: K): ?V`: Removes key and returns prior value.
- `m.keys(): [K]`
- `m.values(): [V]`
- `m.clear()`

#### The `Sortable` Interface & Collection Sorting (`std/sort.ll`)
The `std/sort.ll` module provides high-performance in-place sorting based on Quicksort with median-of-three Lomuto partitioning and insertion sort optimization for sub-partitions $\le 16$ elements:

```lowlight
define Sortable interface [
    sort: (&self),
]
```

- `lst.sort()`: Sorts list in-place in ascending order (requires `T: Comparable`).
- `lst.sort_by(cmp_fn: (a: &T, b: &T): int)`: In-place sorts using custom comparator function returning negative, zero, or positive integer.
- `lst.is_sorted(): bool`: Checks whether list is sorted in non-decreasing order.
- `lst.binary_search(target: &T): ?int`: Binary search on sorted list; returns index of match or `none`.
- `lst.reverse()`: Reverses list in-place.
- `s.to_sorted_list(): [T]`: Converts set elements to a newly allocated sorted list.
- `m.to_sorted_keys(): [K]`: Extracts map keys into a newly allocated sorted list.
- Standalone functions: `sort.sort(&lst)`, `sort.sort_by(&lst, cmp_fn)`, `sort.is_sorted(lst)`, `sort.binary_search(lst, target)`.

### 9.4 File System Operations (`std/fs.ll`)

Lowlight provides robust native file system and file stream operations through `std/fs.ll`:

#### The `File` Resource Struct
```lowlight
define File struct [
    handle: ptr[void],
    is_open: bool,
]
```
- Implements `Drop` via `File.drop(&self)`: Automatically closes any open file descriptor upon scope exit, preventing file descriptor leaks under all control flow paths (including early returns and panics).

#### Constructors
- `File.open(path: string, mode: string): Result[File, string]`
- `File.open_read(path: string): Result[File, string]` (mode `"rb"`)
- `File.open_write(path: string): Result[File, string]` (mode `"wb"`)
- `File.open_append(path: string): Result[File, string]` (mode `"ab"`)
- `File.open_read_write(path: string): Result[File, string]` (mode `"w+b"`)

#### Stream Methods
- `file.close(): bool`: Closes stream explicitly.
- `file.flush(): bool`: Flushes internal buffers.
- `file.seek(offset: int, whence: i32): bool`: Seeks to position using `SEEK_SET = 0`, `SEEK_CUR = 1`, or `SEEK_END = 2`.
- `file.tell(): int`: Returns current stream position offset.
- `file.size(): int`: Returns total file size in bytes.
- `file.write_string(s: string): Result[int, string]`: Writes UTF-8 text to stream.
- `file.write_bytes(bytes: [u8]): Result[int, string]`: Writes byte slice to stream.
- `file.read_bytes(max_bytes: int): Result[[u8], string]`: Reads up to `max_bytes` into an owned list.
- `file.read_to_string(): Result[string, string]`: Reads remaining file stream contents to end as string.

#### Top-Level Convenience Functions
- `fs.read_file(path: string): Result[string, string]` (alias `fs.read_to_string`, `fs.read_text`)
- `fs.write_file(path: string, content: string): Result[bool, string]` (alias `fs.write_to_file`, `fs.write_text`)
- `fs.append_file(path: string, content: string): Result[bool, string]`
- `fs.read_bytes(path: string): Result[[u8], string]`
- `fs.write_bytes(path: string, bytes: [u8]): Result[bool, string]`
- `fs.exists(path: string): bool`
- `fs.file_size(path: string): int`
- `fs.remove_file(path: string): bool`
- `fs.rename_file(old_path: string, new_path: string): bool`
- `fs.copy_file(src_path: string, dst_path: string): Result[bool, string]`

### 9.5 Operating System & Universe Functions (`std/os.ll`)

The following functions are available globally in all scopes:
- `print(val: Printable)`: Writes string representation to standard output without newline.
- `println(val: Printable)`: Writes string representation to standard output with trailing newline.
- `eprint(val: Printable)`: Writes string representation to standard error.
- `eprintln(val: Printable)`: Writes string representation to standard error with trailing newline.
- `panic(msg: string): never`: Terminates process execution immediately with an error message.
- `assert(cond: bool [, msg: string])`: Evaluates `cond`; panics if `cond` is `false`.
- `args(): [string]`: Returns command-line invocation arguments as an owned `[string]` list. Implemented in pure native Lowlight in `std/os.ll` by iterating runtime argument counts and accessing argument vector elements.

### 9.6 The `Result[T, E]` Standard Type

The standard prelude defines the canonical error-handling type `Result[T, E]`:

```lowlight
@must_use
define Result[T, E] struct [
    value?: ?T,
    err?: ?E,
    ok_flag: bool,
]
```

#### Inherent Methods
- `Result[T, E].ok(val: own T): Result[T, E]`
- `Result[T, E].fail(err: own E): Result[T, E]`
- `res.is_ok(): bool`
- `res.is_err(): bool`
- `res.unwrap(): T`: Returns value if ok; panics with error if failed.
- `res.unwrap_or(default: own T): T`: Returns value if ok; otherwise returns `default`.

Because `Result` is marked `@must_use`, ignoring a returned `Result` without inspection is a static compile error.

### 9.7 Testing System & Standard Testing Library (`std/test.ll`)

Lowlight provides an integrated compile-time testing framework and standard assertion library:

#### Test Annotations (`@test`, `@test_only`)
- `@test` / `@test("description")`: Marks a top-level function as a test case. The function takes 0 parameters and returns `void`. The function identifier can have any name—the `@test` attribute marks it as a test.
- `@test_only`: Marks helper functions, mock structs, enums, interfaces, distinct types, or globals intended exclusively for test code.

#### Compile-Time Isolation & Build Stripping
- **Call-Graph Restriction**: Any function or declaration not marked `@test` or `@test_only` (production code) is strictly prohibited from calling or referencing any symbol marked `@test` or `@test_only`. Attempting to access test symbols from production code produces a static compile-time error.
- **Release & Non-Test Build Stripping**: In standard application builds (`lowlight build` or `--release`), all test functions and `@test_only` items are omitted during MIR lowering and completely excluded from generated C code and binaries.

#### Dedicated Test Entry Point & Runner (`lowlight test`)
Running `lowlight test <file.ll>` activates test compilation mode:
- If `bind test_main = ()` is explicitly defined, it serves as the test entry point.
- Otherwise, the compiler automatically synthesizes a C11 test runner that discovers and executes all `@test` functions across the entry module and imported modules.
- **Per-Test Leak Checking**: The runner evaluates `ll_rt_live_allocs` before and after each test function. Any test that leaks heap memory fails with: `--- FAIL: <test> (leaked N heap allocation(s))`.
- **Panic Recovery**: Test execution is guarded with C `setjmp`/`longjmp`. Failed assertions or panics report failure diagnostics and continue running remaining tests in the suite rather than terminating the test process prematurely.
- **High-Resolution Timing**: Reports execution duration per test in microseconds.

#### Standard Testing Assertions (`std/test.ll`)
Importing `std/test.ll` provides comprehensive test assertion utilities:
- `assert_eq[T: Equatable + Printable](actual: T, expected: T)`
- `assert_eq_msg[T: Equatable + Printable](actual: T, expected: T, msg: string)`
- `assert_ne[T: Equatable + Printable](actual: T, unexpected: T)`
- `assert_ne_msg[T: Equatable + Printable](actual: T, unexpected: T, msg: string)`
- `assert_true(cond: bool)` / `assert_true_msg(cond: bool, msg: string)`
- `assert_false(cond: bool)` / `assert_false_msg(cond: bool, msg: string)`
- `assert_gt[T: Comparable + Printable](actual: T, expected: T)`
- `assert_lt[T: Comparable + Printable](actual: T, expected: T)`
- `assert_ge[T: Comparable + Printable](actual: T, expected: T)`
- `assert_le[T: Comparable + Printable](actual: T, expected: T)`
- `assert_is_ok[T, E: Printable](res: &Result[T, E])` / `assert_is_ok_msg(...)`
- `assert_is_err[T, E](res: &Result[T, E])` / `assert_is_err_msg(...)`
- `assert_some[T](val?: ?T)`
- `assert_none[T](val?: ?T)`

---

## 10. Unsafe Operations & Foreign ABI Interoperability

Lowlight isolates all potentially memory-unsafe operations behind explicit syntactic boundaries.

### 10.1 The `unsafe` Prefix Operator

In Lowlight, an executable block `{ ... }` is an expression. The `unsafe` keyword is a **unary prefix operator** that can precede any expression:

```
unsafe <expr>
```

Operations that can violate memory safety—such as raw pointer dereferences (`*p`), writing through raw pointers (`*p = val`), pointer offsets (`p[i]`), calling foreign functions, or raw heap memory allocation—are permitted only within an `unsafe <expr>`:

```lowlight
// `unsafe` applied to a block expression:
unsafe {
    let p = ptr_from_ref(&x)
    *p = 100
}

// `unsafe` applied directly to a single operation:
let val = unsafe *raw_ptr
unsafe foreign_function_call(arg1, arg2)
```

Within an `unsafe` expression, the programmer assumes full responsibility for pointer validity, non-null guarantees, and memory alignment. Safe Lowlight invariants—including affine move semantics, borrow exclusivity, and deterministic destruction—continue to be strictly enforced by the compiler even inside `unsafe`.

### 10.2 Raw Pointer Mechanics (`ptr[T]`)

Raw pointers `ptr[T]` allow unmanaged address manipulation:
- `ptr_null[T](): ptr[T]`: Produces a null pointer.
- `ptr_from_ref[T](r: &T): ptr[T]`: Converts a safe reference to a raw pointer.
- `*p`: Dereferences a pointer to read its value (unsafe).
- `*p = val`: Overwrites the target memory location with `val` without running prior destructors (unsafe).
- `p[i]`: Accesses memory at offset $p + i \times \text{sizeof}(T)$ (unsafe).
- `ptr_add[T](p: ptr[T], offset: int): ptr[T]`: Performs pointer arithmetic.

### 10.3 Safe Sizing vs. Unsafe Memory Intrinsics

- **Safe Sizing Functions:**
  - `size_of[T](): int`: Returns the byte size of type $T$ at compile time.
  - `align_of[T](): int`: Returns the byte alignment of type $T$ at compile time.
- **Unsafe Allocator Intrinsics:**
  - `alloc_raw(bytes: int, align: int): ptr[u8]`: Allocates unmanaged heap memory (unsafe).
  - `free_raw(p: ptr[u8])`: Releases unmanaged heap memory (unsafe).
  - `copy_raw(src: ptr[u8], dst: ptr[u8], bytes: int)`: Copies non-overlapping memory (unsafe).

### 10.4 Foreign Function Interface (`extern`)

Lowlight provides a target-agnostic Foreign Function Interface (FFI) declaration mechanism for interacting with external libraries and runtimes:

```lowlight
extern "C" "stdio.h" [
    puts: (s: ptr[u8]): i32,
    fopen: (path: ptr[u8], mode: ptr[u8]): ptr[void],
    fclose: (stream: ptr[void]): i32,
]
```

- **Target-Agnostic ABI Specification:** The string identifying the foreign ABI (e.g., `"C"`) specifies the foreign calling convention. For compilers targeting ISO C or native machine code, `"C"` designates the standard platform C ABI. Compilers targeting other environments (such as the JVM or WebAssembly) map corresponding ABI identifiers (e.g., `"java"` or `"wasm"`) to their target platforms. C interoperability is one specific ABI mapping, not an inherent semantic assumption of Lowlight.
- **Header / Source Library Path:** An optional second string provides target-specific binding metadata (such as a C header file `"stdio.h"` or shared library).
- **Symbol Renaming (`as`):** Foreign symbols may be renamed to expose an ergonomic local identifier:
  ```lowlight
  extern "C" [
      c_rename as lowlight_name: (arg: int),
  ]
  ```
- **Safety Boundary:** All foreign functions are inherently outside the safety guarantees of Lowlight. Every invocation of an `extern` function is an unsafe operation and must be preceded by `unsafe`.

---

## 11. Formal Grammar Specification (EBNF)

```ebnf
(* Lowlight Grammar Specification in Extended Backus-Naur Form *)

Program             = { TopDeclaration } EOF ;

TopDeclaration      = [ AttributeList ] ( BindDecl
                                        | MethodDecl
                                        | StructDecl
                                        | EnumDecl
                                        | InterfaceDecl
                                        | DistinctDecl
                                        | ExternBlock
                                        | ImportDecl ) ;

AttributeList       = { "@" Ident [ "(" [ AttrArgList ] ")" ] } ;
AttrArgList         = AttrArg { "," AttrArg } [ "," ] ;
AttrArg             = StringLit | Ident ;

ImportDecl          = "import" StringLit [ "as" Ident ] ;

BindDecl            = "bind" Ident [ ":" Type ] "=" Expr ;
MethodDecl          = "bind" Ident [ TypeParams ] "." Ident [ TypeParams ] "=" ( FuncLit | Expr ) ;
LetDecl             = "let" ( Ident | OptIdent ) [ ":" Type ] "=" Expr ;

DistinctDecl        = "define" Ident "=" Type ;

StructDecl          = "define" [ "packed" ] Ident [ TypeParams ] "struct" "[" [ FieldDeclList ] "]" ;
FieldDeclList       = FieldDecl { "," FieldDecl } [ "," ] ;
FieldDecl           = [ AttributeList ] ( Ident | OptIdent ) ":" Type [ "=" Expr ] ;

EnumDecl            = "define" Ident [ TypeParams ] "enum" [ IntegerType ] "[" [ VariantList ] "]" ;
VariantList         = VariantDecl { "," VariantDecl } [ "," ] ;
VariantDecl         = UnitVariant | TupleVariant | StructVariant ;
UnitVariant         = Ident [ "=" ConstExpr ] ;
TupleVariant        = Ident ":" Type ;
StructVariant       = Ident ":" "[" [ FieldDeclList ] "]" ;

InterfaceDecl       = "define" Ident [ TypeParams ] "interface" "[" [ MethodSigList ] "]" ;
MethodSigList       = MethodSig { "," MethodSig } [ "," ] ;
MethodSig           = Ident ":" "(" [ ParamList ] ")" [ ":" Type ] ;

ExternBlock         = "extern" StringLit [ StringLit ] "[" [ ExternSymbolList ] "]" ;
ExternSymbolList    = ExternSymbol { "," ExternSymbol } [ "," ] ;
ExternSymbol        = [ Ident "as" ] Ident ":" "(" [ ParamList ] ")" [ ":" Type ] ;

TypeParams          = "[" TypeParam { "," TypeParam } [ "," ] "]" ;
TypeParam           = Ident [ ":" InterfaceBounds ] ;
InterfaceBounds     = TypeIdent { "+" TypeIdent } ;

TypeArgs            = "[" Type { "," Type } [ "," ] "]" ;

Type                = PrimitiveType
                    | "string"
                    | "?" Type
                    | "[" Type "]"
                    | "[" IntLit ":" Type "]"
                    | "#[" Type "]"
                    | "#[" Type ":" Type "]"
                    | Ident [ TypeArgs ]
                    | "(" [ TypeList ] ")" [ ":" Type ]
                    | "ref" "[" Type "]"
                    | "ptr" "[" Type "]"
                    ;

PrimitiveType       = "int" | "i8" | "i16" | "i32" | "i64"
                    | "u8" | "u16" | "u32" | "u64"
                    | "float" | "f32" | "f64"
                    | "bool" | "char" ;

Expr                = UnwindExpr
                    | AssignmentExpr
                    | LogicalOrExpr ;

AssignmentExpr      = PlaceExpr ( "=" | "+=" | "-=" | "*=" | "/=" | "%="
                                | "&=" | "|=" | "^=" | "<<=" | ">>=" ) Expr ;

LogicalOrExpr       = LogicalAndExpr { "or" LogicalAndExpr } ;
LogicalAndExpr      = BitwiseOrExpr { "and" BitwiseOrExpr } ;
BitwiseOrExpr       = BitwiseXorExpr { "|" BitwiseXorExpr } ;
BitwiseXorExpr      = BitwiseAndExpr { "^" BitwiseAndExpr } ;
BitwiseAndExpr      = EqualityExpr { "&" EqualityExpr } ;
EqualityExpr        = RelationalExpr [ ( "==" | "!=" ) RelationalExpr ] ;
RelationalExpr      = ShiftExpr [ ( "<" | "<=" | ">" | ">=" ) ShiftExpr ] ;
ShiftExpr           = AdditiveExpr { ( "<<" | ">>" ) AdditiveExpr } ;
AdditiveExpr        = MultiplicativeExpr { ( "+" | "-" ) MultiplicativeExpr } ;
MultiplicativeExpr  = CoalesceExpr { ( "*" | "/" | "%" ) CoalesceExpr } ;
CoalesceExpr        = UnaryExpr { "??" UnaryExpr } ;

UnaryExpr           = ( "-" | "not" | "~" | "*" | "&" | "own" | "unsafe" ) UnaryExpr
                    | PostfixExpr ;

PostfixExpr         = PrimaryExpr { MemberAccess
                                  | OptionalMemberAccess
                                  | CallExpr
                                  | IndexExpr
                                  | SliceExpr } ;

MemberAccess         = "." Ident ;
OptionalMemberAccess = "?." Ident ;
CallExpr             = [ TypeArgs ] "(" [ ArgList ] ")" ;
IndexExpr            = "[" Expr "]" ;
SliceExpr            = "[" [ Expr ] ".." [ Expr ] "]"
                     | "[" [ Expr ] "..=" Expr "]" ;

PrimaryExpr         = Literal
                    | Ident
                    | OptIdent
                    | StructLit
                    | ListLit
                    | ArrayLit
                    | SetOrMapLit
                    | BlockExpr
                    | IfExpr
                    | ForExpr
                    | MatchExpr
                    | LambdaExpr
                    | "(" Expr ")" ;

Literal             = IntLit | FloatLit | BoolLit | CharLit | StringLit | "none" ;

StructLit           = Ident [ TypeArgs ] "[" [ StructFieldInitList ] "]" ;
StructFieldInitList = StructFieldInit { "," StructFieldInit } [ "," ] ;
StructFieldInit     = ( Ident | OptIdent ) [ ":" Expr ] ;

ListLit             = "[" [ ExprList | ListComprehension ] "]" ;
ListComprehension   = "for" "(" ForHeader ")" ActionBody ;
ArrayLit            = "[" IntLit ":" Expr "]" ;

SetOrMapLit         = "#[" [ SetElements | MapEntries | MapComprehension ] "]" ;
SetElements         = Expr { "," Expr } [ "," ] ;
MapEntries          = MapEntry { "," MapEntry } [ "," ] ;
MapEntry            = Expr ":" Expr ;
MapComprehension    = "for" "(" ForHeader ")" Expr ":" Expr ;

BlockExpr           = "{" { Statement } [ Expr ] "}" ;

Statement           = LetDecl
                    | BindDecl
                    | Expr ( ";" | AutoSemi ) ;

IfExpr              = "if" "(" IfCondition ")" ActionBody [ "else" ActionBody ] ;
IfCondition         = [ "&" ] OptIdent
                    | [ "&" ] Expr "as" Ident
                    | Expr ;

ForExpr             = "for" "(" ForHeader ")" ActionBody [ "else" ActionBody ] ;
ForHeader           = [ "&" ] ( Ident | OptIdent ) [ "," [ "&" ] ( Ident | OptIdent ) ] "in" ( RangeExpr | Expr )
                    | Expr ;

RangeExpr           = Expr ( ".." | "..=" ) Expr ;

MatchExpr           = "match" "(" Expr ")" { MatchArm } MatchElseArm ;
MatchArm            = "when" PatternList ":" ActionBody ;
MatchElseArm        = "else" [ PatternList ] ":" ActionBody ;
PatternList         = Pattern { "," Pattern } [ "," ] ;
Pattern             = Literal
                    | "_"
                    | Ident
                    | Ident "." Ident [ "(" Pattern ")" | "[" [ PatternFieldList ] "]" ] ;

ActionBody          = BlockExpr | Expr ;

UnwindExpr          = "<-" [ Expr ]
                    | "if" "<-" [ Expr ]
                    | "match" "<-" [ Expr ]
                    | "for" "<-" [ Expr ]
                    | "next" "<-" [ Expr ]
                    | Ident "<-" [ Expr ]
                    | "next" Ident "<-" [ Expr ]
                    | "func" "<-" [ Expr ] ;

LambdaExpr          = "(" [ ParamList ] ")" [ ":" Type ] ActionBody ;

ParamList           = Param { "," Param } [ "," ] ;
Param               = [ "own" | "&" ] ( Ident | OptIdent ) ":" Type ;

ArgList             = Arg { "," Arg } [ "," ] ;
Arg                 = [ "own" | "&" ] Expr ;
```

---
*End of Lowlight Language Specification.*
