package markdown

import (
	"strings"
	"testing"
)

func render(src string) string { return Render(src, Options{}).HTML }

func TestBlocks(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"heading", "## 3.1 Plain values", `<h2 id="31-plain-values">`},
		{"para", "a *b* **c** `d`", "<p>a <em>b</em> <strong>c</strong> <code>d</code></p>"},
		{"snake", "must_use and is_ok", "<p>must_use and is_ok</p>"},
		{"fence", "```lowlight\nbind x = 1\n```", `<pre class="lang-lowlight" data-lang="lowlight"><code>bind x = 1</code></pre>`},
		{"table", "| a | b |\n|---|---|\n| `x \\| y` | 2 |", "<td><code>x | y</code></td><td>2</td>"},
		{"tight list", "- one\n- two", "<ul>\n<li>one\n</li>\n<li>two\n</li>\n</ul>"},
		{"ordered", "3. x\n4. y", `<ol start="3">`},
		{"nested", "1. a\n   - b\n2. c", "<li>a\n<ul>\n<li>b\n</li>\n</ul>\n</li>"},
		{"loose", "1. a\n\n   more\n2. b", "<li><p>a</p>\n<p>more</p>\n</li>"},
		{"quote", "> **At a glance**\n> - x", `<blockquote class="glance">`},
		{"link", "[§3](#3-types) and <https://x.dev>", `<a href="#3-types">§3</a> and <a href="https://x.dev">https://x.dev</a>`},
		{"escape", "a < b && `<-`", "<p>a &lt; b &amp;&amp; <code>&lt;-</code></p>"},
		{"hr", "a\n\n---\n\nb", "<hr />"},
		{"double backtick", "`` `x` ``", "<code>`x`</code>"},
		{"html", "<div class=\"x\">\nhi\n</div>", "<div class=\"x\">\nhi\n</div>"},
	}
	for _, c := range cases {
		got := render(c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want substring\n%s\ngot\n%s", c.name, c.want, got)
		}
	}
}

func TestFrontMatterAndHeadings(t *testing.T) {
	d := Render("---\ntitle: Hello\norder: 2\n---\n# A\n## A\n", Options{})
	if d.Meta["title"] != "Hello" || d.Meta["order"] != "2" {
		t.Fatalf("meta: %v", d.Meta)
	}
	if len(d.Headings) != 2 || d.Headings[1].ID != "a-1" {
		t.Fatalf("headings: %+v", d.Headings)
	}
}

func TestResolveRef(t *testing.T) {
	opts := Options{ResolveRef: func(t string) (string, string, bool) {
		if t == "List.push" {
			return "core.html#List.push", "", true
		}
		return "", "", false
	}}
	got := Render("See [`List.push`] and [`Nope`].", opts).HTML
	if !strings.Contains(got, `<a class="ref" href="core.html#List.push"><code>List.push</code></a>`) {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(got, "[<code>Nope</code>]") {
		t.Fatalf("unresolved ref should stay literal: %s", got)
	}
}
