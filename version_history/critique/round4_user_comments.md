# Round 4 User Comments on `spec_critique.md` (v0.3)

**Date**: October 21, 2026 (23:33 PDT)
**Target Document**: `spec_critique.md` (v0.3 — Updated with Round 2 & Round 3 Feedback)

---

## Verbatim Inline Comments (1–31)

### Comment 1 (Part 1, Section A — Lifetimes: Option 2 vs. Option 2.5 & Copying Borrows)
* **Selected Text**:
  > `#### Option 2: Strict Second-Class References (Hylo / Val style)`
* **User Comment**:
  > It seems like option 2 or 2.5 is the way to go. I still don't quite understand the two though or quite where the difference lies. Also, what happens if a borrowed value needs to be saved in a struct or something? Is there a way to copy the borrowed value so that the value is still stored without the borrow being stored?

---

### Comment 2 (Part 1, Section B — Reborrowing / Reference Flattening)
* **Selected Text**:
  > `* **How Reborrowing Should Work (My Recommendation):**`
  > `  ...`
  > `  * **Recommended Rule — Reference Flattening (Reborrowing):**`
  > `    1. Nested reference types (&&T, &-&-T, & &-T) do not exist.`
  > `    2. Writing &-x when x is already a &-T **reborrows** x as &-T for the duration of that call (temporarily lending exclusive access to the callee, then restoring it when the call finishes). In C, it simply passes the pointer value through.`
  > `    3. Writing &x when x is &T or &-T reborrows x as a read-only &T.`
  > `    4. Writing &-x when x is an immutable &T is a compile error.`
* **User Comment**:
  > Yes. I like reborrowing as you've outlined it

---

### Comment 3 (Part 1, Section C — Go-Style Semicolon / Newline Rules)
* **Selected Text**:
  > `### C. Go-Style Semicolon / Newline Rules Adapted for Lowlight (Pending Your Review)`
* **User Comment**:
  > This is good

---

### Comment 4 (Part 1, Section D — Terminating `match`: Overall)
* **Selected Text**:
  > `### D. Terminating match: end vs. Indentation vs. Better Fits (Pending Your Review)`
* **User Comment**:
  > None of these are quite satisfying. Give me some more ideas

---

### Comment 5 (Part 1, Section D — Terminating `match`: Option 1 `{ ... }`)
* **Selected Text**:
  > `1. **Option 1: Wrap match in { ... } (Fits Principle 4!)**`
  > `   ...`
* **User Comment**:
  > This breaks the rules for blocks. Blocks are "{ … }" in which is a series of statemensts/expressions from which a value is returned. The match expression as a whole returns a value, but the brackes do not house a list of statements/expressions

---

### Comment 6 (Part 1, Section D — Terminating `match`: Option 2 `[ ... ]`)
* **Selected Text**:
  > `2. **Option 2: Wrap match in [ ... ] with commas (Treat match as a Pattern Map)**`
  > `   ...`
* **User Comment**:
  > This is the most tolerable of the options, but I still don't like it.

---

### Comment 7 (Part 1, Section D — Terminating `match`: Option 3 Greedy `when`/`else`)
* **Selected Text**:
  > `3. **Option 3: Greedy when/else Continuation + (...) for Nested Matches**`
  > `   ...`
* **User Comment**:
  > This just simply isn't satisfying. It doesn't look good and it isn't intuitive.

---

### Comment 8 (Part 1, Section E Follow-Up — `if (user?)` Borrow Syntax)
* **Selected Text**:
  > `1. **Option 1: Implicit Immutable Borrow if (user?), Explicit Mutable Borrow if (&-user?) (Recommended)**`
  > `   ...`
* **User Comment**:
  > Let's do this one, though if someone wants to write "if (&user?) …" that's okay too

---

### Comment 9 (Part 1, Section F — Rethinking `for` Loops Around Expressions)
* **Selected Text**:
  > `### F. Unwinding (<-) & Rethinking for Loops Around Expressions`
* **User Comment**:
  > I'm intrigued by both design 1 and design 2. In design 1 I'm concerned that a lot of the utility of a traditional for loop will be lost. I'm simply unsure and unconvinced on design 2 so far. Expound on them both and give a few more options that fall within the constraints.

---

### Comment 10 (Part 2, §1 Principles — "Zero Hidden Runtime Overhead")
* **Selected Text**:
  > `* **"Zero hidden runtime overhead" vs. Heap Defaults:**`
  > `  ...`
* **User Comment**:
  > You are correct. I want the language to be able to be pretty low level, but yes zero hidden overhead is probably misleading

---

### Comment 11 (Part 2, §1 Principles — "Explicit Pointers" & C Interop)
* **Selected Text**:
  > `* **"Explicit pointers" & C Interop:** The spec mentions explicit pointers in Principle 1, but there is no raw pointer type or extern C function syntax yet.`
* **User Comment**:
  > There aren't explicit pointers. We should get to C interop, but yes that hasn't happened yet.

---

### Comment 12 (Part 2, §1 Principles — Principle 3 & 4 Wording & Refining §1)
* **Selected Text**:
  > `* **Principle 3 & 4 Wording:**`
  > `  * Update Principle 3 to reflect your clarification that declarations, assignments, and <- unwinds are statements, and functions can be void (non-value-returning).`
  > `  * Update Principle 4 to note that [] is also used for indexing (ages["Diana"]), destructuring patterns (Circle[r]), and possibly match (if you pick [...] for match).`
* **User Comment**:
  > Yes. Please update the spec accordingly. The principle as written there is very pithy almost to the point of being unhelpful. Please refine it to actually be informitive. Make the principles actually outline the core aims of the language.

---

### Comment 13 (Part 2, §2 Lexical — Comments, Escapes, Chars, Numerics)
* **Selected Text**:
  > `* Still to specify when you're ready:`
  > `  * Multi-line comments (/* ... */?).`
  > `  * Escape sequences in strings/chars (\n, \t, \", \\).`
  > `  * Character literal syntax ('a') vs. string literals ("a").`
  > `  * Numeric literal formats: hex (0xFF), binary (0b1010), underscores (1_000_000).`
* **User Comment**:
  > /* */ for multiline comments is good.
  > Yes there will be string/char escape characters, those four and more. We just haven't gotten there yet. We'll have to tackle strings more fully.
  > Yes there should be chars and ' is for char while " is for strings
  > Hex yes. Bin yes. Why underscores, just readabality? I think we'll need to flesh out numeric stuff more sometime

---

### Comment 14 (Part 2, §3 `let` / `bind` — Deep Immutability for `bind` Everywhere)
* **Selected Text**:
  > `* **Deep vs. Shallow Immutability for bind Collections (§3.2):**`
  > `  ...`
  > `  * **Suggestion:** Make bind **deeply immutable for everything** (including Vectors, Maps, Sets, and Strings). If you want to insert into a map or push to a vector, you declare it with let cache = #["a": 1]. That makes let = mutable and bind = immutable with zero exceptions!`
* **User Comment**:
  > Yes. This sounds good

---

### Comment 15 (Part 2, §3 `let` / `bind` — Parameter Colon Consistency)
* **Selected Text**:
  > `* **Type Annotation Colon Consistency:**`
  > `  * Struct fields use a colon: x: int`
  > `  * Function return types use a colon: (a int, b int): int`
  > `  * Explicit local variable types use a colon: let count?: ?int = none`
  > `  * Function parameters do **not** use a colon: (a int, b int)`
  > `  * Is omitting the colon in parameters intentional, or would you rather have (a: int, b: int) everywhere?`
* **User Comment**:
  > Yes add the colon syntax to function calls

---

### Comment 16 (Part 2, §3 `let` / `bind` — Variable Shadowing)
* **Selected Text**:
  > `* **Variable Shadowing:** Since if (user?) shadows an outer user, can regular let or bind declarations in an inner block (or the same block) also shadow an existing variable?`
* **User Comment**:
  > yes a let or bind can shadow. Is there any reason this would be a bad idea?

---

### Comment 17 (Part 2, §3.3 / §10 Functions — Function Type Syntax)
* **Selected Text**:
  > `* **Function Type Syntax:** With return types written :Type, how do you write the type of a function variable or callback parameter? E.g., (int, int): int and (): string?`
* **User Comment**:
  > I don't quite understand what you're asking here

---

### Comment 18 (Part 2, §3.3 / §10 Functions — Capturing Lambdas)
* **Selected Text**:
  > `* **Can Anonymous Functions (Lambdas) Capture Variables?**`
  > `  ...`
* **User Comment**:
  > Yes, lambdas can capture variables

---

### Comment 19 (Part 2, §3.3 / §10 Functions — `let Type.method`)
* **Selected Text**:
  > `* **Forbid let Type.method:** In §9, methods are declared with bind Point.length = .... You probably want to forbid let Point.length = ... so methods on a type cannot be reassigned at runtime.`
* **User Comment**:
  > Why should let type.method be forbiden? Why would we want them to not be able to be reassigned at runtime?

---

### Comment 20 (Part 2, §4.1 Numerics — Default Literal Types)
* **Selected Text**:
  > `* **"Smallest Fitting Type" vs. [1, 2, 3] // type: [int]:**`
  > `  ...`
  > `  * **Likely intent:** Numeric literals adapt to a known target type if they fit (let b: u8 = 10 or take_u16(500) works without a cast), and **default to int (i64) / float (f64)** when no target type is specified (let x = 10 is int).`
* **User Comment**:
  > Yes. The default shold be int/i64 and float f64, not the smallest fitting size of the relevent type.

---

### Comment 21 (Part 2, §4.1 Numerics — Explicit Type Conversions)
* **Selected Text**:
  > `* **Type Conversions / Casting:** How do you convert int to float or u8 to int? In §9, sqrt(self.x * self.x + self.y * self.y) passes an int into sqrt. Are numeric conversions explicit (e.g., float(x) or x as float)?`
* **User Comment**:
  > float(x)

---

### Comment 22 (Part 2, §4.1 Numerics — Copy vs. Move Types)
* **Selected Text**:
  > `* **Copy vs. Move Types:**`
  > `  * §5.1 says scalar primitives (int, float, bool, char) are copied rather than moved.`
  > `  * What about a stack struct or enum that only contains primitives, like Point[x: int, y: int] or [3: int]? In C, small structs are copied by value; in Rust, types must opt into Copy. Should structs/arrays made purely of copyable fields also be copied by value, or moved?`
* **User Comment**:
  > Give me the pros and cons of each. I want to lean toward defaulting to borrowing, but have copying as an option, but I don't know what's best

---

### Comment 23 (Part 2, §4.2 Strings — `&string` vs. `&-string` Wording)
* **Selected Text**:
  > `* **&string vs. &-string Representation:**`
  > `  ...`
  > `  * Note: Change the wording on L101 from *"allowing in-place modification without reallocating"* to *"allowing in-place mutation of the caller's string without moving ownership"* (since .push() may reallocate the heap buffer when capacity is exceeded).`
* **User Comment**:
  > I'm slightly fuzzy here still, but I think I'm with you. Change the wording

---

### Comment 24 (Part 2, §4.2 Strings — Dollar Sign Escape)
* **Selected Text**:
  > `* **Duplicate Dollar Escape:** L110 lists both \$ and $$ to escape $. Pick one (\$ is standard alongside \n and \").`
* **User Comment**:
  > Yes. \$ and not $$

---

### Comment 25 (Part 2, §4.2 Strings — Predefined `to_string` on Built-In Primitives)
* **Selected Text**:
  > `* **Infinite Recursion in §9.1:**`
  > `  ...`
  > `  If "${self}" works by calling self.to_string(), then int.to_string calls itself infinitely! Built-in primitives should either have to_string built in, or "${...}" on primitives should call the C runtime formatter directly.`
* **User Comment**:
  > built-in primitives should have a predefined bound to_string

---

### Comment 26 (Part 2, §5 Ownership — Recursive Structs / Heap Allocation)
* **Selected Text**:
  > `* **Recursive Structs / Heap Allocation:**`
  > `  * How do you define a recursive struct like a tree node or linked list (define Node struct [ val: int, next?: ?Node ])? In C, a struct cannot contain itself by value (infinite size); it needs a heap pointer. Does Lowlight have a way to heap-allocate a single struct, or are recursive structures built using vectors/arenas?`
* **User Comment**:
  > Give me some options here. I think I kinda understand the problem, but not fully. It's been a minute since I've used C or something that low level

---

### Comment 27 (Part 2, §7 Compound Data Structures — Stack Array Syntax `[N: T]`)
* **Selected Text**:
  > `* **Stack Array Syntax [N: ...] (§7.1):**`
  > `  ...`
  > `  * **Cleaner idea:** What if [3: 0] **always** means *"repeat 0 three times on the stack"*, and listing explicit elements on the stack uses a slightly different marker (or type annotation let s: [3: int] = [1, 2, 3])?`
* **User Comment**:
  > I like that. The type is [(len): type]

---

### Comment 28 (Part 2, §7 Compound Data Structures — Vector Out-of-Bounds Indexing)
* **Selected Text**:
  > `* **Vector Out-of-Bounds Indexing:**`
  > `  * Map lookup ages["Alice"] returns an optional ?int.`
  > `  * What does vector/array indexing v[i] return? Does v[i] return T (and panic/abort at runtime if i is out of bounds, plus allow v[i] = 10), or does it return ?T?`
* **User Comment**:
  > I'm not sure yet. I know we haven't got to errors yet, so this may be easier to solve then. I think an index that is out of bounds should return something different than ?T.

---

### Comment 29 (Part 2, §7 Compound Data Structures — Empty Collection Literals)
* **Selected Text**:
  > `* **Empty Collection Literals:**`
  > `  * An empty vector [] doesn't reveal its element type, and #[] doesn't reveal whether it's an empty Set or an empty Map.`
  > `  * Require an explicit type on the variable when initializing empty collections (let v: [int] = [], let m: #[string: int] = #[])?`
* **User Comment**:
  > Sounds good

---

### Comment 30 (Part 2, §8 `define` — Section 8.2 Enums & Tagged Unions Explanation)
* **Selected Text**:
  > `* **Enum Variant Naming Collision (§8.2):**`
  > `  ...`
* **User Comment**:
  > Really, I don't quite know what's going on in section 8.2. What is it getting at? Once I know more, I'll be able to give you more difinitive answer

---

### Comment 31 (Part 2, §8 `define` — Interface Method Receivers `&self` / `&-self`)
* **Selected Text**:
  > `* **Interface Method Receivers (§8.3):**`
  > `  ...`
* **User Comment**:
  > Yes. Interface definitions should have to explicitly write out &self or &-self

---

### Comment 32 (Part 2, §8 `define` — `define UserID int` as Distinct Type)
* **Selected Text**:
  > `* **define UserID int — Alias or Distinct Type? (§8.4):**`
  > `  * Can you pass a regular int to a function expecting UserID without converting it?`
  > `  * If you write bind UserID.to_string = ..., does that define a method only on UserID, or on all ints?`
* **User Comment**:
  > This woul be a distinct type. You must convert an int to a UserID. It would only define a method for UserID. These distinct types would be able to overide builtin methods like to_string/Printable.

---

### Comment 33 (Part 2, §11 Control Flow — Lexing `<-` and `&-`, Range Syntax, and Vector Index Iteration)
* **Selected Text (1)**:
  > `* **Lexing <- and &- Without Ambiguity:**`
* **User Comment (1)**:
  > I like that <- must have white space around it. If &- can't be used as a binary operator is something lost?
* **Selected Text (2)**:
  > `* **Range Syntax (0..10):**`
* **User Comment (2)**:
  > I'm not sure yet. give me some options
* **Selected Text (3)**:
  > `* **for (k, v in my_map) vs. Vectors:**`
* **User Comment (3)**:
  > Loops still have a lot of work to do. Bring this back up once loop have more been sorted out
