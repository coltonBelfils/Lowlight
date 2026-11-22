---
title: Guides
summary: In-depth explanations of Lowlight's core ideas and everyday practices.
order: 0
---

# Guides

Guides go deeper than the [tour](/tour/index.html). Each one explains a part of the language or toolchain: why it works the way it does, the patterns that work well, and the mistakes to avoid. Every runnable example is tested against the compiler.

## Writing Lowlight

<div class="cards">
<a class="card" href="effective-lowlight.html"><h3>Effective Lowlight</h3><p>The style guide: naming, formatting, failure handling, and idioms.</p></a>
<a class="card" href="coming-from.html"><h3>Coming from C, Rust, or Go</h3><p>What carries over, what's different, and a translation table.</p></a>
</div>

## Core ideas

<div class="cards">
<a class="card" href="ownership.html"><h3>Ownership and borrowing</h3><p>Moves, borrows, destruction, closures, and how to design APIs around them.</p></a>
<a class="card" href="errors.html"><h3>Failure handling</h3><p>Choosing between optionals, results, and panics; designing result types; adding context.</p></a>
<a class="card" href="collections.html"><h3>Collections and iteration</h3><p>Lists, maps, sets, and arrays; collecting loops; writing your own iterators.</p></a>
<a class="card" href="generics.html"><h3>Interfaces and generics</h3><p>Structural interfaces, bounds, monomorphization, and the built-in interfaces.</p></a>
<a class="card" href="memory.html"><h3>Memory: arenas and pools</h3><p>Graphs, trees, and other shared structures without a garbage collector.</p></a>
</div>

## Working with code

<div class="cards">
<a class="card" href="packages.html"><h3>Packages and dependencies</h3><p>Projects, imports, visibility, manifests, lockfiles, workspaces, and vendoring.</p></a>
<a class="card" href="testing.html"><h3>Testing</h3><p>Writing tests, assertions, caller locations, the test runner, and editor integration.</p></a>
<a class="card" href="unsafe-ffi.html"><h3>Unsafe code and C</h3><p>Raw pointers, memory intrinsics, packed structs, and foreign functions.</p></a>
<a class="card" href="performance.html"><h3>Performance</h3><p>Release builds, backends, data layout, and what safety checks cost.</p></a>
</div>
