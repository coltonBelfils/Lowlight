---
title: Tutorials
summary: Step-by-step tutorials that build complete programs.
order: 0
---

# Tutorials

Each tutorial builds a complete program from scratch, explaining each step and the language features it uses. They assume you have [installed Lowlight](/start/install.html) and skimmed the [tour](/tour/index.html).

<div class="cards">
<a class="card" href="word-count.html"><span class="tag">Beginner</span><h3>A command-line tool</h3><p>Read a file, count words with a map, sort them, and print a table. Strings, collections, arguments, and exit codes.</p></a>
<a class="card" href="calculator.html"><span class="tag">Intermediate</span><h3>A calculator</h3><p>Tokenizer, recursive-descent parser, and evaluator. Enums, <code>match</code>, methods, and <code>Result</code>.</p></a>
<a class="card" href="config-loader.html"><span class="tag">Intermediate</span><h3>Handling errors</h3><p>A config file parser with its own result type, precise failure handling, and error context.</p></a>
<a class="card" href="web-service.html"><span class="tag">Intermediate</span><h3>A web service</h3><p>An HTTP API with <code>std/http</code>, tested in-process and then served.</p></a>
<a class="card" href="calling-c.html"><span class="tag">Advanced</span><h3>Calling C</h3><p>Declare C functions, pass strings and buffers, and wrap unsafe calls safely.</p></a>
</div>

## More ways to learn

- **[Lowlight by Example](/examples/index.html)**: short annotated programs, one feature each.
- **[Guides](/guides/index.html)**: in-depth explanations of ownership, failure handling, generics, testing, and more.
- **The `examples/` directory** of the repository has larger showcase programs: an arena-allocated graph, a lexer and evaluator, HTTP servers, and a TCP echo server.
