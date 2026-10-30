# Round 1 User Comments on `spec_critique.md` (Draft v0.1)

**Date:** October 20, 2026 (17:39 PDT)  
**Target Document:** `v0.1_spec_critique.md` (Part 1: Structural Concerns)

---

### 1. On Lifetimes (Section 1.A)
> **Selection:**  
> `You have roughly three options:`
>
> **User Comment:**  
> "It seems like the option is between option 1 and 2. Breakdown each and the pros and cons of each. I think I want to lean more toward 2, but I'm not sure."

---

### 2. On Call-Site `&-`, Method Auto-Borrowing, and Reborrowing (Section 1.A)
> **Selection:**  
> `There is also a contradiction in §5.2. It says &- is mandatory at the call site, but method calls auto-borrow with no annotation: pt.translate(5, 5) (L306), v.push(4), cache.insert(...). Either methods are an exception or the rule needs rewording. Reborrowing is also unspecified: inside push, list is already a &-[int]. Does passing it on to a helper need &-list, and does that produce a &-&-[int]?`
>
> **User Comment:**  
> "`&-` is manditory at the call site when applicable. Method calls where `pt` and `v` implicitly have something like `self` as the first paramater auto borrow. If written the long way, `"Point.translate(&-pt, 5, 5)"`, the barrowing would need to be explicit. Does that work?. I'm unsure on the reborrowing. What are your thoughts there?"

---

### 3. On Go-Style Semicolon / Newline Rules (Section 1.B)
> **Selection:**  
> `You also need to say whether newlines are ignored inside () and []. The trailing-comma rule in §7.3 implies they are, but the spec never says so. Go's rule is a proven model: insert a separator only after a line that ends in an identifier, a literal, a closing bracket, or certain keywords.`
>
> **User Comment:**  
> "I like how Go does it. Implament the same idea here and run it by me. How would that be translated into Lightweight?"

---

### 4. On Terminating `match` (Section 1.C)
> **Selection:**  
> `A match that is exhaustive without an else (every enum variant covered) has no visible end at all. And if else is required as the terminator, the enum example's else: 0.0 is unreachable code that the language would force you to write. Your options are to require braces around the arms (bending the {} rule), add an end keyword, or use indentation.`
>
> **User Comment:**  
> "What are your suggestions? Could you give me a few more? Having an `end` keyword is probably going to have to be the move, but it feels out of place in the language that doesn't use `end` anywhere else. What are your thoughts between `end` and indentation?"

---

### 5. On `if (flag?)` with Optional Booleans (Section 1.D)
> **Selection:**  
> `if (flag?) where flag holds an optional bool: does it test presence or the value?`
>
> **User Comment:**  
> "It would test the presence of a value, not the bool itself. The optional wrapper must be dealt with before the value is interacted with."

---

### 6. On `?T` vs. `name?` Optional System (Section 1.D)
> **Selection:**  
> `Suggestion: make ?T the single source of truth. The name suffix can stay as a required marker that the compiler checks against the type. Or drop one of the two systems.`
>
> **User Comment:**  
> "Yes. This is what I meant. The underlying type is `?T`, but a variable that holds an optional must have a `?` suffix."

---

### 7. On `user` and `user?` Being Separate Identifiers (Section 1.D)
> **Selection:**  
> `Are user and user? separate identifiers? Can both be in scope at once?`
>
> **User Comment:**  
> "Yes. Those are two seperate identified. They may represent something quite similar, but one certainly holds an optional value and the other certainly does not"

---

### 8. On Explicit Function Return Types (Section 1.D)
> **Selection:**  
> `Fields, parameters, return types. Is it email?: string, (u? User), () ?User?`
>
> **User Comment:**  
> "Return types must be specified. `():string` OR `():?string`"

---

### 9. On Nested Optionals `??int` vs. Flattening (Section 1.D)
> **Selection:**  
> `Nested optionals. A lookup in #[string: ?int] gives ??int. Is that flattened?`
>
> **User Comment:**  
> "I'm not sure here. Give me the pros and cons of both."

---

### 10. On `user?.name` vs. `?.` Operator (Section 1.D)
> **Selection:**  
> `user?.name. Is ?. a chaining operator, or is it the identifier user? followed by .name with implicit chaining? What does find_user(id)?.name mean?`
>
> **User Comment:**  
> "The `?` would be a part of the name, not an `?.` operator. Is it worth haveing something like a `?.` operator?"

---

### 11. On Narrowing Edge Cases (Section 1.D)
> **Selection:**  
> `Narrowing. Is narrowing undone if user? = none happens inside the if? Does it work for a guard like if (!user?) { f <- none } followed by user.name? For && conditions? For fields such as a.b??`
>
> **User Comment:**  
> "I'm not sure what you mean here. Please explain."

---

### 12. On String Interpolation with `?` (Section 1.D)
> **Selection:**  
> `Interpolation. In "Are you $ready?", the ? gets swallowed into the identifier.`
>
> **User Comment:**  
> "I'm not quite sure how that should be handeled. Thoughts?"

---

### 13. On Braceless `if` and `<-` Unwinding (Section 1.E)
> **Selection:**  
> `"Immediate block": does the body of a braceless if count as a block? Inside a loop, is if (c) <- 5 a continue, or does it yield 5 from the if?`
>
> **User Comment:**  
> "No. The `<-` token is only a feature of a block `"{ … }"`. `"if(c) <- 5"` would break out of the nearest set of brackets, the nearest block"

---

### 14. On Topmost `<-` in a `for` Loop Acting as `continue` (Section 1.E)
> **Selection:**  
> `"Topmost <- in a for acts as continue": continue with what value? You need a bare <- or a unit value.`
>
> **User Comment:**  
> "You're saying that implicitly something like `"<- null"` or `"<- unit"` would have to happen under the hood, since a block must evaluate to a value? I don't quite understand what the concern is here."

---

### 15. On `func <-`, Shadowing, Concise Method Returns, and Lexical Enclosing (Section 1.E)
> **Selection:**  
> `func_name <-:`
> - `Anonymous lambdas have no name to target.`
> - `A local variable could shadow the function's name.`
> - `Methods would need Point.length <- x.`
> - `The rule must say the target has to be lexically enclosing. Otherwise you get non-local returns across calls or closures, and in C that means setjmp/longjmp.`
>
> **User Comment:**  
> "have `func` also be a return receiver, so `"func <- x"` would return `x` to the nearest function.
>
> Can you give me an example of how a varibale shadowing the function name would be relevent?
>
> I don't like the the return receiver would need to be `Point.length`. It makes sense that that is what it would need to be, but I'd like it to be more concise. Thoughts?
>
> Tell me more about this. What does it mean that it must be lexically enclosing? I don't think a return would be able to across function calls."

---

### 16. On Freeing Temporaries When Jumping via `<-` (Section 1.E)
> **Selection:**  
> `Temporaries: in f(make_string(), { g <- 1 }), the string has already been built when the jump happens, so it has to be freed first.`
>
> **User Comment:**  
> "I don't understand what you mean here. You'll have to give a fuller explination"

---

### 17. On Void Functions (Section 1.F)
> **Selection:**  
> `The body of append_tag (L103).`
>
> **User Comment:**  
> "I guess this as written probably wouldn't have a retrun type. It would simply be void, or whatever value `s.push` returns. What should it return or what are my options? Whould every function have to return something or does having functions that simply mutate a value fit?"

---

### 18. On `for` Loops Used as Statements (Section 1.F)
> **Selection:**  
> `A for loop used as a statement.`
>
> **User Comment:**  
> "This still may or may not return something, but the value, if any is just discarded. I guess it would evaluate to null or something and it's just that that null isn't ever used."

---

### 19. On `for (true)` with No `else` (Section 1.F)
> **Selection:**  
> `for (true) with no else. The rule makes it ?T, but it never finishes normally.`
>
> **User Comment:**  
> "It would be a `?T` then. What do you mean it never finishes normally?"

---

### 20. On `<-` Being a Statement, `if <-`, and `??` Unwinding (Section 1.F)
> **Selection:**  
> `The <- expression itself. It needs to be a never type (one that matches any other type) so that if (c) f <- 1 else 2 type-checks.`
>
> **User Comment:**  
> "`<-` is not an expression, but a statement. `if <- 5` makes all blocks until it hits an `if` evaluate to a `5` so that that `if` that has a block as its expresion evaluate to `5`
>
> ```
> let b? = maybe_null()
> ():Int {
>   if(true) {
>       let a = b? ?? if <- 0
>       a
>     }
> }
> ```
> This code is obviously contribed and not ideal, but the if statement would resolve to either `a` or `0`."

---

### 21. On Statements vs. Expressions (Section 1.F)
> **Selection:**  
> `§6.4 also talks about "statements without a value," which contradicts principle 3 ("everything is an expression").`
>
> **User Comment:**  
> "Assignments, definitions, and returns at least aren't expressions. Basically everything is an expression, but not *everything*"

---

### Overall Message Accompanying Round 1 Comments
> "This is just my comments on the first part of your critique. I still need to comment on the section-by-section part. Take in my comments, see how that informs the rest, give any subsequent or new thoughts, and then present me with a new updated critique document."
