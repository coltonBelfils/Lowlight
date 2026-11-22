// Package markdown renders the Markdown dialect used by Lowlight's documentation:
// CommonMark-style blocks (headings, paragraphs, block quotes, nested lists, fenced
// code, thematic breaks, raw HTML blocks), GitHub tables, and inline code, links,
// images, emphasis, strikethrough, and line breaks.
//
// It exists because the Lowlight toolchain has no third-party dependencies. It is not a
// complete CommonMark implementation, but it renders every construct the Lowlight
// specification, guides, and doc comments use. Hooks let callers highlight code,
// rewrite link targets, and resolve doc-comment symbol links (`[`List.push`]`).
package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Options customizes rendering. Every field is optional.
type Options struct {
	// Highlight renders the body of a fenced code block as HTML. It returns ok=false to
	// fall back to escaped text. lang is the first word of the info string.
	Highlight func(lang, code string) (html string, ok bool)
	// RewriteLink maps a link or image destination to the href to emit.
	RewriteLink func(dest string) string
	// ResolveRef resolves a bracketed reference with no destination, such as
	// "[`List.push`]" (target "List.push") or "[the list][`List`]". It returns the href,
	// a title, and ok=false when the text should be rendered literally.
	ResolveRef func(target string) (href, title string, ok bool)
	// HeadingIDPrefix is prepended to generated heading ids.
	HeadingIDPrefix string
	// ShiftHeadings adds this many levels to every heading (capped at h6).
	ShiftHeadings int
	// CodeBlockClass is added to every <pre> element.
	CodeBlockClass string
}

// Heading is one heading of a rendered document.
type Heading struct {
	Level int
	Text  string // plain text
	HTML  string // inline HTML
	ID    string
}

// Doc is a rendered document.
type Doc struct {
	HTML     string
	Headings []Heading
	Meta     map[string]string // front matter, when present
}

// Render converts Markdown source into HTML.
func Render(src string, opts Options) *Doc {
	r := &renderer{opts: opts, ids: map[string]int{}}
	meta, body := SplitFrontMatter(src)
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var sb strings.Builder
	r.blocks(&sb, lines, false)
	return &Doc{HTML: sb.String(), Headings: r.headings, Meta: meta}
}

// SplitFrontMatter separates a leading "---" block of "key: value" lines from the body.
func SplitFrontMatter(src string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(src, "---\n") && !strings.HasPrefix(src, "---\r\n") {
		return meta, src
	}
	rest := src[strings.IndexByte(src, '\n')+1:]
	end := -1
	off := 0
	for _, ln := range strings.SplitAfter(rest, "\n") {
		if strings.TrimRight(ln, "\r\n") == "---" {
			end = off
			break
		}
		off += len(ln)
	}
	if end < 0 {
		return meta, src
	}
	for _, ln := range strings.Split(rest[:end], "\n") {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			v = strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"') {
				if uq, err := strconv.Unquote(v); err == nil {
					v = uq
				}
			}
			meta[strings.TrimSpace(k)] = v
		}
	}
	body := rest[end:]
	body = body[strings.IndexByte(body, '\n')+1:]
	return meta, body
}

// Slug returns the GitHub-style anchor for a heading's plain text.
func Slug(text string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r == ' ':
			sb.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

type renderer struct {
	opts     Options
	headings []Heading
	ids      map[string]int
}

var (
	reATX       = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	reFence     = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})[ \t]*([^`]*)$")
	reHR        = regexp.MustCompile(`^ {0,3}((\*[ \t]*){3,}|(-[ \t]*){3,}|(_[ \t]*){3,})$`)
	reBullet    = regexp.MustCompile(`^( {0,3})([-*+])( +|\t|$)`)
	reOrdered   = regexp.MustCompile(`^( {0,3})(\d{1,9})([.)])( +|\t|$)`)
	reTableSep  = regexp.MustCompile(`^ {0,3}\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
	reHTMLBlock = regexp.MustCompile(`^ {0,3}<(/?)(div|section|article|aside|nav|header|footer|details|summary|table|thead|tbody|tr|td|th|ul|ol|li|p|h[1-6]|figure|figcaption|pre|blockquote|form|iframe|hr|main|dl|dt|dd|!--)(\s|>|/>|$)`)
)

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// indentWidth counts leading columns, expanding tabs to 4.
func indentWidth(s string) int {
	w := 0
	for _, c := range s {
		switch c {
		case ' ':
			w++
		case '\t':
			w += 4 - w%4
		default:
			return w
		}
	}
	return w
}

// stripIndent removes up to n columns of leading whitespace.
func stripIndent(s string, n int) string {
	w := 0
	for i, c := range s {
		if w >= n {
			return s[i:]
		}
		switch c {
		case ' ':
			w++
		case '\t':
			nw := w + 4 - w%4
			if nw > n {
				return strings.Repeat(" ", nw-n) + s[i+1:]
			}
			w = nw
		default:
			return s[i:]
		}
	}
	return ""
}

// blocks renders a sequence of lines as block content. tight suppresses <p> around
// paragraphs (tight list items).
func (r *renderer) blocks(sb *strings.Builder, lines []string, tight bool) {
	i := 0
	for i < len(lines) {
		ln := lines[i]
		if isBlank(ln) {
			i++
			continue
		}
		// Fenced code.
		if m := reFence.FindStringSubmatch(ln); m != nil {
			indent := len(m[1])
			fence := m[2]
			info := strings.TrimSpace(m[3])
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, fence[:3]) && strings.Trim(t, string(fence[0])) == "" && len(t) >= len(fence) {
					break
				}
				body = append(body, stripIndent(lines[j], indent))
			}
			r.codeBlock(sb, info, strings.Join(body, "\n"))
			i = j + 1
			continue
		}
		// ATX heading.
		if m := reATX.FindStringSubmatch(ln); m != nil {
			r.heading(sb, len(m[1]), m[2])
			i++
			continue
		}
		// Thematic break (checked before lists so "- - -" is a rule).
		if reHR.MatchString(ln) {
			sb.WriteString("<hr />\n")
			i++
			continue
		}
		// Raw HTML block: runs until a blank line.
		if reHTMLBlock.MatchString(ln) {
			j := i
			for j < len(lines) && !isBlank(lines[j]) {
				sb.WriteString(lines[j])
				sb.WriteByte('\n')
				j++
			}
			i = j
			continue
		}
		// Block quote.
		if strings.HasPrefix(strings.TrimLeft(ln, " "), ">") && indentWidth(ln) < 4 {
			var inner []string
			j := i
			for j < len(lines) {
				t := strings.TrimLeft(lines[j], " ")
				if strings.HasPrefix(t, ">") {
					t = t[1:]
					if strings.HasPrefix(t, " ") {
						t = t[1:]
					}
					inner = append(inner, t)
					j++
					continue
				}
				// Lazy continuation of a paragraph.
				if !isBlank(lines[j]) && len(inner) > 0 && !isBlank(inner[len(inner)-1]) && !r.startsBlock(lines[j]) {
					inner = append(inner, lines[j])
					j++
					continue
				}
				break
			}
			cls := ""
			if len(inner) > 0 {
				first := strings.TrimSpace(inner[0])
				switch {
				case strings.HasPrefix(first, "**At a glance**"):
					cls = ` class="glance"`
				case strings.HasPrefix(first, "**Note"), strings.HasPrefix(first, "[!NOTE]"):
					cls = ` class="note"`
				case strings.HasPrefix(first, "**Warning"), strings.HasPrefix(first, "[!WARNING]"):
					cls = ` class="warning"`
				case strings.HasPrefix(first, "**Tip"), strings.HasPrefix(first, "[!TIP]"):
					cls = ` class="tip"`
				}
				if strings.HasPrefix(first, "[!") {
					inner = inner[1:]
				}
			}
			sb.WriteString("<blockquote" + cls + ">\n")
			r.blocks(sb, inner, false)
			sb.WriteString("</blockquote>\n")
			i = j
			continue
		}
		// List.
		if _, _, _, ok := listMarker(ln); ok && indentWidth(ln) < 4 {
			i = r.list(sb, lines, i)
			continue
		}
		// Table.
		if i+1 < len(lines) && strings.Contains(ln, "|") && reTableSep.MatchString(lines[i+1]) {
			hdr := splitRow(ln)
			if len(hdr) == len(splitRow(lines[i+1])) {
				i = r.table(sb, lines, i)
				continue
			}
		}
		// Indented code block (only outside tight contexts, and not paragraph continuation).
		if indentWidth(ln) >= 4 {
			var body []string
			j := i
			for j < len(lines) && (isBlank(lines[j]) || indentWidth(lines[j]) >= 4) {
				body = append(body, stripIndent(lines[j], 4))
				j++
			}
			for len(body) > 0 && isBlank(body[len(body)-1]) {
				body = body[:len(body)-1]
			}
			r.codeBlock(sb, "", strings.Join(body, "\n"))
			i = j
			continue
		}
		// Paragraph.
		var para []string
		j := i
		for j < len(lines) && !isBlank(lines[j]) {
			if j > i && r.startsBlock(lines[j]) {
				break
			}
			para = append(para, strings.TrimLeft(lines[j], " \t"))
			j++
		}
		text := strings.Join(para, "\n")
		if tight {
			sb.WriteString(r.inline(text))
			sb.WriteByte('\n')
		} else {
			sb.WriteString("<p>")
			sb.WriteString(r.inline(text))
			sb.WriteString("</p>\n")
		}
		i = j
	}
}

// startsBlock reports whether ln interrupts a paragraph.
func (r *renderer) startsBlock(ln string) bool {
	if indentWidth(ln) >= 4 {
		return false
	}
	if reFence.MatchString(ln) || reATX.MatchString(ln) || reHR.MatchString(ln) || reHTMLBlock.MatchString(ln) {
		return true
	}
	if strings.HasPrefix(strings.TrimLeft(ln, " "), ">") {
		return true
	}
	if _, ordered, start, ok := listMarker(ln); ok {
		// An ordered list interrupts a paragraph only when it starts at 1.
		rest := strings.TrimSpace(ln)
		if ordered && start != 1 {
			return false
		}
		_ = rest
		return true
	}
	return false
}

// listMarker parses a list item marker, returning the content column, whether it is
// ordered, and the start number.
func listMarker(ln string) (contentCol int, ordered bool, start int, ok bool) {
	if m := reBullet.FindStringSubmatch(ln); m != nil {
		pad := len(m[3])
		if pad == 0 || pad > 4 {
			pad = 1
		}
		if strings.TrimSpace(ln[len(m[0]):]) == "" && m[3] == "" {
			pad = 1
		}
		return len(m[1]) + 1 + pad, false, 0, true
	}
	if m := reOrdered.FindStringSubmatch(ln); m != nil {
		pad := len(m[4])
		if pad == 0 || pad > 4 {
			pad = 1
		}
		n, _ := strconv.Atoi(m[2])
		return len(m[1]) + len(m[2]) + 1 + pad, true, n, true
	}
	return 0, false, 0, false
}

func (r *renderer) list(sb *strings.Builder, lines []string, i int) int {
	_, ordered, start, _ := listMarker(lines[i])
	type item struct{ lines []string }
	var items []item
	loose := false
	j := i
	for j < len(lines) {
		col, ord, _, ok := listMarker(lines[j])
		if !ok || ord != ordered || indentWidth(lines[j]) >= 4 {
			break
		}
		// Item content: first line after the marker, then lines indented at least col.
		first := lines[j][min(col, len(lines[j])):]
		if col > len(lines[j]) {
			first = ""
		}
		it := item{lines: []string{first}}
		k := j + 1
		sawBlank := false
		for k < len(lines) {
			l := lines[k]
			if isBlank(l) {
				it.lines = append(it.lines, "")
				sawBlank = true
				k++
				continue
			}
			if indentWidth(l) >= col {
				if sawBlank {
					loose = true
				}
				it.lines = append(it.lines, stripIndent(l, col))
				sawBlank = false
				k++
				continue
			}
			// Lazy paragraph continuation (never across a new list marker).
			if _, _, _, isItem := listMarker(l); !sawBlank && !isItem && !r.startsBlock(l) {
				it.lines = append(it.lines, strings.TrimLeft(l, " \t"))
				k++
				continue
			}
			break
		}
		// Trailing blank lines belong between items.
		trail := 0
		for len(it.lines) > 0 && it.lines[len(it.lines)-1] == "" {
			it.lines = it.lines[:len(it.lines)-1]
			trail++
		}
		items = append(items, it)
		j = k
		if trail > 0 {
			if j < len(lines) {
				if _, ord2, _, ok2 := listMarker(lines[j]); ok2 && ord2 == ordered && indentWidth(lines[j]) < 4 {
					loose = true
				}
			}
		}
	}
	tag := "ul"
	attr := ""
	if ordered {
		tag = "ol"
		if start != 1 {
			attr = fmt.Sprintf(` start="%d"`, start)
		}
	}
	sb.WriteString("<" + tag + attr + ">\n")
	for _, it := range items {
		sb.WriteString("<li>")
		r.blocks(sb, it.lines, !loose)
		sb.WriteString("</li>\n")
	}
	sb.WriteString("</" + tag + ">\n")
	return j
}

func splitRow(ln string) []string {
	s := strings.TrimSpace(ln)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	inCode := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if c == '`' {
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			if inCode == 0 {
				inCode = n
			} else if inCode == n {
				inCode = 0
			}
			cur.WriteString(s[i : i+n])
			i += n - 1
			continue
		}
		if c == '|' && inCode == 0 {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func (r *renderer) table(sb *strings.Builder, lines []string, i int) int {
	hdr := splitRow(lines[i])
	seps := splitRow(lines[i+1])
	aligns := make([]string, len(seps))
	for k, s := range seps {
		l, rr := strings.HasPrefix(s, ":"), strings.HasSuffix(s, ":")
		switch {
		case l && rr:
			aligns[k] = ` style="text-align:center"`
		case rr:
			aligns[k] = ` style="text-align:right"`
		}
	}
	sb.WriteString("<div class=\"table-wrap\"><table>\n<thead><tr>")
	for k, h := range hdr {
		sb.WriteString("<th" + aligns[min(k, len(aligns)-1)] + ">" + r.inline(h) + "</th>")
	}
	sb.WriteString("</tr></thead>\n<tbody>\n")
	j := i + 2
	for ; j < len(lines); j++ {
		if isBlank(lines[j]) || !strings.Contains(lines[j], "|") {
			break
		}
		cells := splitRow(lines[j])
		sb.WriteString("<tr>")
		for k := 0; k < len(hdr); k++ {
			c := ""
			if k < len(cells) {
				c = cells[k]
			}
			sb.WriteString("<td" + aligns[min(k, len(aligns)-1)] + ">" + r.inline(c) + "</td>")
		}
		sb.WriteString("</tr>\n")
	}
	sb.WriteString("</tbody>\n</table></div>\n")
	return j
}

func (r *renderer) heading(sb *strings.Builder, level int, text string) {
	inner := r.inline(text)
	plain := PlainText(inner)
	base := r.opts.HeadingIDPrefix + Slug(plain)
	id := base
	if n := r.ids[base]; n > 0 {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	r.ids[base]++
	lv := level + r.opts.ShiftHeadings
	if lv > 6 {
		lv = 6
	}
	r.headings = append(r.headings, Heading{Level: level, Text: plain, HTML: inner, ID: id})
	fmt.Fprintf(sb, "<h%d id=\"%s\"><a class=\"anchor\" href=\"#%s\" aria-hidden=\"true\">#</a>%s</h%d>\n", lv, Escape(id), Escape(id), inner, lv)
}

func (r *renderer) codeBlock(sb *strings.Builder, info, code string) {
	lang := info
	if k := strings.IndexAny(lang, " \t{"); k >= 0 {
		lang = lang[:k]
	}
	cls := r.opts.CodeBlockClass
	langAttr := ""
	if lang != "" {
		langAttr = fmt.Sprintf(` data-lang="%s"`, Escape(lang))
		if cls != "" {
			cls += " "
		}
		cls += "lang-" + Escape(lang)
	}
	clsAttr := ""
	if cls != "" {
		clsAttr = fmt.Sprintf(` class="%s"`, cls)
	}
	body := ""
	if r.opts.Highlight != nil {
		if h, ok := r.opts.Highlight(lang, code); ok {
			body = h
		}
	}
	if body == "" {
		body = Escape(code)
	}
	fmt.Fprintf(sb, "<pre%s%s><code>%s</code></pre>\n", clsAttr, langAttr, body)
}

// Escape escapes text for HTML.
func Escape(s string) string {
	if !strings.ContainsAny(s, `&<>"'`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&quot;")
		case '\'':
			sb.WriteString("&#39;")
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

var reTag = regexp.MustCompile(`<[^>]*>`)

// PlainText strips tags and decodes the few entities Escape produces.
func PlainText(html string) string {
	s := reTag.ReplaceAllString(html, "")
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
}

// RenderInline renders a single line of inline Markdown.
func RenderInline(s string, opts Options) string {
	r := &renderer{opts: opts, ids: map[string]int{}}
	return r.inline(s)
}
