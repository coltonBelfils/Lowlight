# Round 3 User Comments on `spec_critique.md` (v0.3)

**Date**: October 21, 2026 (15:23 PDT)
**Target Document**: `spec_critique.md` (v0.3 — Updated with Round 2 Feedback)

---

## Verbatim Inline Comment

### Comment 1 (Section F — Follow-Up 2: Where Can `<-` Unwind Statements Appear?)
* **Selected Text**:
  > `#### Follow-Up 2: Where Can <- Unwind Statements Appear?`
  > `You asked:`
  > `> *"Or would it be better to not let unwind statements in expressions, but just in assignment statements or as bare unwind statements?"*`
  >
  > `**Yes—restricting <- to statement positions and top-level ?? in assignments/declarations is much cleaner!**`
  >
  > `Specifically, if the rule is:`
  > `1. **Standalone unwind statement:** target <- val (or bare target <-) as its own statement in a block, or as the body of an if / match arm:`
  > `   if (n == 999) func <- none`
  > `2. **Right-hand side of a top-level ?? in a let / bind / assignment:**`
  > `   bind user = find_user(id) ?? func <- none`
  > `3. **Forbidden inside nested sub-expressions or function call arguments:**`
  > `   // COMPILE ERROR: cannot put <- inside function call arguments or math expressions`
  > `   print_two_strings(make_header(), user?.name ?? func <- -1)`
  > `   // Instead, write it clearly in two steps:`
  > `   bind name = user?.name ?? func <- -1`
  > `   print_two_strings(make_header(), name)`
* **User Comment**:
  > This all looks good

---

## Verbatim Accompanying Message from User
> Update the specification document to make it fully up to date with our changes.
