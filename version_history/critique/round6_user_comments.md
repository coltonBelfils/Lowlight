# Round 6 User Comments on `spec_critique.md` (v0.5)

**Date**: October 22, 2026 (15:48 PDT)
**Target Document**: `spec_critique.md` (v0.5 — Updated with Round 5 Feedback)

---

## Verbatim Inline Comments (1–6)

### Comment 1 (Part 1, Section D — `match` Termination: Idea 1 Locked In)
* **Selected Text**:
  > `Would you like to lock in **Idea 1** for match?`
* **User Comment**:
  > Yes. Lets lock in idea 1

---

### Comment 2 (Part 1, Section F.1 — Question on `next <- val` Passing `val` to Next Iteration)
* **Selected Text**:
  > `1. **What Each Iteration Evaluates To:**`
  > `   * The body of a for loop is either a single expression (for (x in list) expr) or a block (for (x in list) { ... }).`
  > `   * Inside the loop body, next <- val (or <- val at the top level of the loop's {} block) ends the **current iteration** early with val and starts the next iteration.`
  > `   * for <- val aborts the **entire for loop** immediately and makes the whole for expression evaluate to val.`
* **User Comment**:
  > Does next <- val pass val to the next iterations expression?

---

### Comment 3 (Part 1, Section F.1 — Question on How Compiler/User Knows Whether `for` Is `T` or `?T`)
* **Selected Text**:
  > `2. **What the Whole for Loop Evaluates To:**`
  > `   * **If the loop body ends in a value expression of type T**:`
  > `     Each iteration produces a T. When the loop finishes running, the for expression evaluates to **the value produced by the last iteration that ran** (or for <- val if it broke early).`
  > `     * What if the loop ran **0 times** (e.g., numbers was empty [])?`
  > `       * If there is an else clause (for (...) { ... } else { fallback }), the loop evaluates to fallback (so the loop's type is guaranteed **T**).`
  > `       * If there is **no else clause**, the loop evaluates to none (so the loop's type is **?T**).`
  > `   ...`
* **User Comment**:
  > How is the compiler or the user supposed to know if the type is T or ?T?

---

### Comment 4 (Part 2, §1 — Variable Shadowing: Option 1 Locked In)
* **Selected Text**:
  > `Now that you see how Option 2 works, do you prefer **Option 1 (Unrestricted Shadowing)** or **Option 2 (Same-Block Shadowing Only)**?`
* **User Comment**:
  > Go with option 1.

---

### Comment 5 (Part 2, §2 — Model 2 Selected, 2A vs. 2B Question, & Simplifying Borrow Syntax to No Marker vs. `&` for Mutable Borrow)
* **Selected Text**:
  > `How does seeing **Model 1** vs. **Model 2 (2A)** side-by-side feel to you?`
* **User Comment**:
  > Would you recomend 2A over 2B then? Let's go with Model 2, it's just if it should be 2A or 2B. Either way, now that the amount of markers has been simplified and borrowing is the default, let's have borrowing not have any marker and mutable borrowing just use &

---

### Comment 6 (Part 2, §3 — Enums: Option 1 Robust C-Style Enums Locked In)
* **Selected Text**:
  > `#### Option 1: Robust C-Style Enums Only (Pure Tags, No Per-Variant Struct Fields)`
* **User Comment**:
  > Do option 1
