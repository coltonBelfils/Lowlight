# Round 2 User Comments on `spec_critique.md` (v0.2)

**Date**: October 21, 2026 (12:41 PDT)
**Target Document**: `spec_critique.md` (v0.2 — Updated with Round 1 Feedback)

---

## Verbatim Inline Comments (1–7)

### Comment 1 (Section E.1 — Nested Optionals vs. Flattening)
* **Selected Text**:
  > `* **Recommendation:** **Flatten ?(?T) to ?T** (or disallow ?T inside collections/optionals when not flattened). It keeps the name? / name guarantee airtight.`
* **User Comment**:
  > Agreed. Flatten ??T to ?T. Allow optional types in collections/optionals

---

### Comment 2 (Section E.2 — Is a `?.` Chaining Operator Worth Having?)
* **Selected Text**:
  > `* **How to make this completely unified:**`
  > `  * If a struct field holds an optional, its field name also ends in ? (address?: ?Address), so u?.address?.city is literally just the identifier u?, dot, the field name address?, dot, city!`
  > `  * And if a function returns ?User, allowing find_user(id)?.name (putting ? before . on non-variable ?T expressions) keeps the visual rule 100% consistent: **any dot access that can short-circuit on none has ?. at the dot.**`
* **User Comment**:
  > Then yes allow chainging like this. It's all done with the simple dot operator, but the operator kinda does one thing for optionals and one thing for non optionals.

---

### Comment 3 (Section E.3 — Narrowing Edge Cases: Mutating `user?` Inside `if (user?)`)
* **Selected Text**:
  > `1. **What if someone changes user? inside if (user?)?**`
  > `   ...`
  > `   * **Easy fix:** Inside if (user?) { ... }, treat user as borrowing (or copying/moving) from user?. Your normal borrow-checker rules will then automatically forbid user? = none while user is in use inside the block!`
* **User Comment**:
  > Then yes treat it as a borrow. Should there be syntax to show it is a borrow?

---

### Comment 4 (Section E.3 — Narrowing Edge Cases: Both `user` and `user?` in Outer Scope)
* **Selected Text**:
  > `2. **What if both user and user? are declared in the same scope before the if?**`
  > `   ...`
  > `   * Either if (user?) shadows the outer user inside the block, or the compiler forbids having both user and user? in the same scope before an if (user?).`
* **User Comment**:
  > the outer user would be shadowed inside the if expression

---

### Comment 5 (Section F.1 — Why `<- value` as `continue` in a `for` Loop Needs a Tweaked Rule)
* **Selected Text**:
  > `#### 1. Why <- value as continue in a for Loop Needs a Tweaked Rule`
  > `...`
  > `  * Allow **bare <-** (with no value) to exit a non-value {} block early (or func <- with no value to return early from a void function, and for <- with no value to break out of a void loop).`
  > `  * For skipping to the next iteration of a for loop from inside nested {} blocks, give it an explicit target (e.g. next <- or continue), OR state that if (n < 0) <- (without braces on the if) exits the for body's {} block and starts the next iteration.`
* **User Comment**:
  > An unwind target like next sounds like a good Idea. I think we may need to rethink how loops work generally though. The for loop itself is an expression, but so is each iteration—each one uses block expressions or a single non block expression. Fore loops should be made with that consideration in mind. I think that'll mean for loops look and behave considerably different than a traditional loop. I want the expression nature of both the loop itself and the expression nature of each iteration to be accounted for. Blocks as expressions and expressions generally should be used and represented consistantly accross the language.

---

### Comment 6 (Section F.3 — What "Lexically Enclosing" Means)
* **Selected Text**:
  > `Even though inner is written inside outer, calling inner() is a function call with its own C stack frame. So the rule simply needs to state: **An unwind (<-) cannot cross a function or lambda boundary.** You can unwind out of any number of {} blocks, ifs, and for loops inside the *current* function, up to func <-, and no further. That guarantees every <- compiles to a simple C goto or return.`
* **User Comment**:
  > Agreed. The example above would cause a compile time error

---

### Comment 7 (Section F.4 — What "Freeing Temporaries on Jump" Means)
* **Selected Text**:
  > `* This doesn't require any syntax change in Lowlight—it's just a rule for **§13.2 (C Transpilation)**: the compiler must hoist heap-allocated expression temporaries into hidden C variables so it can free them if a later argument unwinds via <-.`
* **User Comment**:
  > This sounds good. Or would it be better to not let unwind statements in expressions, but just in assignment statements or as bare unwind statements?

---

## Verbatim Accompanying Message from User
> Here are some more questions and comments from part 1. The parts of part 1 I didn't comment on will need addressed. I just haven't gotten to them yet. I still haven't looked at part two. We'll get there soon. Take in my comments, see how that informs the rest, give any subsequent or new thoughts, and then present me with a new updated critique document.
