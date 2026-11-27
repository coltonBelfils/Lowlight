---
title: Lowlight — systems programming with nothing hidden
summary: Lowlight is a systems language with C-level control, deterministic memory, and no null, garbage collector, or lifetime annotations.
---

<section class="hero"><div class="hero-inner"><div>
<h1>Systems programming with <em>nothing hidden</em>.</h1>
<p class="lead">Lowlight gives you C-level control over memory and data layout with no garbage collector, no null, and no lifetime annotations. Every mutation, move, failure, and early exit is marked right where it happens, so code reads the way it runs.</p>
<div class="buttons"><a class="btn btn-primary" href="start/index.html">Get started</a><a class="btn btn-ghost" href="tour/index.html">Take the tour</a><a class="btn btn-ghost" href="spec/index.html">Read the spec</a></div>
</div><div>

```lowlight
define Lookup result [
	value: int,
	missing: string,
]

bind find = fn(stock: #[string: int], item: string) Lookup:
	if(stock[item] as n): value(n)
	else: missing(item.copy())

bind restock = fn(stock: &#[string: int], item: string, n: int): {
	stock[item.copy()] = (stock[item] ?? 0) + n
}

bind main = fn(): {
	let stock = #["apples": 3]
	restock(&stock, "pears", 5)       // `&` marks the mutation
	let pears = find(stock, "pears"):
		default missing(what): {
			println("no $what")
			func <-
		}
	println("pears: $pears")
}
```

</div></div></section>

<section class="home-section">
<h2>Why Lowlight</h2>
<p class="sub">A small set of rules, each visible in the code you read.</p>
<div class="features">
<div class="feature"><span class="icon">⏚</span><h3>Deterministic memory</h3><p>Values are freed at the end of the scope that owns them, in reverse order. No collector, no reference counting, no pauses.</p></div>
<div class="feature"><span class="icon">&amp;</span><h3>Visible effects</h3><p>A call can change your variable only if you write <code>&amp;x</code>, and take it only if you write <code>own x</code>. Nothing happens behind your back.</p></div>
<div class="feature"><span class="icon">?</span><h3>No null</h3><p>Absence is an optional type <code>?T</code>, and every name holding one ends in <code>?</code>. You cannot forget to check.</p></div>
<div class="feature"><span class="icon">!</span><h3>Failures are values</h3><p>Operations that can fail return a result type; names holding one end in <code>!</code>. Result chains handle each failure by name.</p></div>
<div class="feature"><span class="icon">⇠</span><h3>Expressions everywhere</h3><p><code>if</code>, <code>match</code>, loops, and blocks produce values. Early exits are one operator, <code>&lt;-</code>, aimed at exactly the construct you leave.</p></div>
<div class="feature"><span class="icon">⚙</span><h3>Down to the metal</h3><p>Raw pointers, packed structs, and C functions are one <code>unsafe</code> away. Compiles to LLVM IR or portable C11.</p></div>
</div>
</section>

<section class="home-section">
<h2>Read code, see what it does</h2>
<p class="sub">Three small programs. Each one runs exactly as shown.</p>
<div class="showcase"><div>
<h3>Ownership you can see</h3>
<p>Plain values copy. Resources such as strings and lists have one owner, and moving one is spelled <code>own</code>. A read-only parameter borrows; <code>&amp;</code> lends it for changing.</p>
<p><a href="tour/ownership.html">Ownership in the tour →</a></p>
</div><div>

```lowlight
bind shout = fn(s: &string): s.push("!")

bind keep = fn(s: own string) [string]: [s]

bind main = fn(): {
	let word = "hello" + ""
	shout(&word)              // borrowed mutably, visibly
	let all = keep(own word)  // moved: `word` is gone
	println(all[0])
}
```

</div></div>
<div class="showcase"><div>
<h3>Patterns, not ceremony</h3>
<p>Enums carry data; <code>match</code> takes them apart. Variants are named bare, and every <code>match</code> ends with a <code>default</code> arm the compiler checks for completeness.</p>
<p><a href="tour/matching.html">Pattern matching →</a></p>
</div><div>

```lowlight
define Shape enum [
	circle: float,
	rect: +[float, float],
	dot,
]

bind area = fn(s: Shape) float:
	match(s):
		when circle(r): 3.14159 * r * r
		when rect(wh): wh.0 * wh.1
		default dot: 0.0

bind main = fn(): println(area(Shape.rect(+[3.0, 4.0])))
```

</div></div>
<div class="showcase"><div>
<h3>Loops that build things</h3>
<p>A loop with a result type collects its body's values. The unwind <code>next &lt;-</code> skips an item. No accumulator boilerplate.</p>
<p><a href="tour/collections.html">Collections and loops →</a></p>
</div><div>

```lowlight
bind main = fn(): {
	let words = ["apple", "", "kiwi", "fig"]
	let lengths = for(w in words) [int]: {
		if(w.is_empty()): next <-
		w.len()
	}
	println(lengths)
}
```

</div></div>
</section>

<section class="home-section">
<h2>Find your way</h2>
<p class="sub">Documentation for every stage, from your first program to writing your own compiler.</p>
<div class="cards">
<a class="card" href="start/index.html"><span class="tag">Start</span><h3>Get started</h3><p>Install the toolchain, run your first program, and set up your editor.</p></a>
<a class="card" href="tour/index.html"><span class="tag">Learn</span><h3>A tour of Lowlight</h3><p>The whole language in thirteen short, runnable lessons.</p></a>
<a class="card" href="learn/index.html"><span class="tag">Learn</span><h3>Tutorials</h3><p>Build real programs step by step: a word counter, a calculator, a config loader, a web service.</p></a>
<a class="card" href="examples/index.html"><span class="tag">Learn</span><h3>Lowlight by Example</h3><p>Annotated, runnable programs, one idea at a time.</p></a>
<a class="card" href="guides/index.html"><span class="tag">Guides</span><h3>Guides</h3><p>In-depth explanations: ownership, failure handling, generics, packages, testing, performance.</p></a>
<a class="card" href="guides/effective-lowlight.html"><span class="tag">Guides</span><h3>Effective Lowlight</h3><p>The style guide: naming, formatting, and idioms that make code read well.</p></a>
<a class="card" href="spec/index.html"><span class="tag">Reference</span><h3>Language specification</h3><p>The complete, precise definition of the language, with a quick reference.</p></a>
<a class="card" href="std/index.html"><span class="tag">Reference</span><h3>Standard library</h3><p>Every package, type, and function, generated from the source.</p></a>
<a class="card" href="internals/index.html"><span class="tag">Internals</span><h3>How it's built</h3><p>The compiler pipeline, runtime, and tooling, for contributors and implementers.</p></a>
</div>
</section>

<section class="home-cta">
<div class="buttons" style="justify-content:center"><a class="btn btn-primary" href="start/install.html">Install Lowlight</a></div>
</section>
