package markdown

import (
	"regexp"
	"strings"
)

var (
	reEntity   = regexp.MustCompile(`^&(#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[a-zA-Z][a-zA-Z0-9]{1,31});`)
	reHTMLTag  = regexp.MustCompile(`^(</?[a-zA-Z][a-zA-Z0-9-]*(\s+[a-zA-Z_:][-a-zA-Z0-9_:.]*(\s*=\s*("[^"]*"|'[^']*'|[^\s"'=<>` + "`" + `]+))?)*\s*/?>|<!--.*?-->)`)
	reAutolink = regexp.MustCompile(`^<(https?://[^\s<>]+|mailto:[^\s<>]+)>`)
)

func isPunct(c byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// codeSpanEnd returns the index just past the code span starting at s[i] (a backtick
// run), and the span's content, or ok=false if the run is unmatched.
func codeSpanEnd(s string, i int) (end int, content string, ok bool) {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	j := i + n
	for j < len(s) {
		k := strings.IndexByte(s[j:], '`')
		if k < 0 {
			return 0, "", false
		}
		k += j
		m := 0
		for k+m < len(s) && s[k+m] == '`' {
			m++
		}
		if m == n {
			c := strings.ReplaceAll(s[i+n:k], "\n", " ")
			if len(c) >= 2 && c[0] == ' ' && c[len(c)-1] == ' ' && strings.TrimSpace(c) != "" {
				c = c[1 : len(c)-1]
			}
			return k + m, c, true
		}
		j = k + m
	}
	return 0, "", false
}

// matchBracket finds the ']' closing the '[' at s[i], skipping code spans and escapes.
func matchBracket(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '`':
			if end, _, ok := codeSpanEnd(s, j); ok {
				j = end - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// parseDest parses "(dest "title")" at s[i] == '(' and returns the end index.
func parseDest(s string, i int) (dest, title string, end int, ok bool) {
	j := i + 1
	for j < len(s) && isSpace(s[j]) {
		j++
	}
	start := j
	if j < len(s) && s[j] == '<' {
		k := strings.IndexByte(s[j:], '>')
		if k < 0 {
			return "", "", 0, false
		}
		dest = s[j+1 : j+k]
		j += k + 1
	} else {
		depth := 0
		for j < len(s) && !isSpace(s[j]) {
			if s[j] == '\\' && j+1 < len(s) {
				j += 2
				continue
			}
			if s[j] == '(' {
				depth++
			} else if s[j] == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
			j++
		}
		dest = s[start:j]
	}
	for j < len(s) && isSpace(s[j]) {
		j++
	}
	if j < len(s) && (s[j] == '"' || s[j] == '\'') {
		q := s[j]
		k := strings.IndexByte(s[j+1:], q)
		if k < 0 {
			return "", "", 0, false
		}
		title = s[j+1 : j+1+k]
		j += k + 2
		for j < len(s) && isSpace(s[j]) {
			j++
		}
	}
	if j >= len(s) || s[j] != ')' {
		return "", "", 0, false
	}
	return dest, title, j + 1, true
}

// findCloser finds a closing delimiter `delim` for emphasis opened just before s[from].
func findCloser(s string, from int, delim string) int {
	for j := from; j+len(delim) <= len(s); j++ {
		switch s[j] {
		case '\\':
			j++
			continue
		case '`':
			if end, _, ok := codeSpanEnd(s, j); ok {
				j = end - 1
				continue
			}
		case '[':
			// Do not let emphasis close inside link text that is still open.
		}
		if !strings.HasPrefix(s[j:], delim) {
			continue
		}
		if j == from || isSpace(s[j-1]) {
			continue
		}
		after := j + len(delim)
		// A single-char delimiter must not be part of a longer run.
		if len(delim) == 1 && after < len(s) && s[after] == delim[0] {
			j++
			continue
		}
		if delim[0] == '_' && after < len(s) && isAlnum(s[after]) {
			continue
		}
		return j
	}
	return -1
}

func (r *renderer) rewrite(dest string) string {
	if r.opts.RewriteLink != nil {
		return r.opts.RewriteLink(dest)
	}
	return dest
}

func unescapeBackslashes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
			i++
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// inline renders inline Markdown.
func (r *renderer) inline(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) && s[i+1] == '\n' {
				sb.WriteString("<br />\n")
				i += 2
				continue
			}
			if i+1 < len(s) && isPunct(s[i+1]) {
				sb.WriteString(Escape(string(s[i+1])))
				i += 2
				continue
			}
			sb.WriteByte('\\')
			i++
		case c == '`':
			if end, content, ok := codeSpanEnd(s, i); ok {
				sb.WriteString("<code>")
				sb.WriteString(Escape(content))
				sb.WriteString("</code>")
				i = end
				continue
			}
			n := 0
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			sb.WriteString(s[i : i+n])
			i += n
		case c == '!' && i+1 < len(s) && s[i+1] == '[':
			if close := matchBracket(s, i+1); close > 0 && close+1 < len(s) && s[close+1] == '(' {
				if dest, title, end, ok := parseDest(s, close+1); ok {
					alt := PlainText(r.inline(s[i+2 : close]))
					sb.WriteString(`<img src="` + Escape(r.rewrite(unescapeBackslashes(dest))) + `" alt="` + Escape(alt) + `"`)
					if title != "" {
						sb.WriteString(` title="` + Escape(title) + `"`)
					}
					sb.WriteString(" />")
					i = end
					continue
				}
			}
			sb.WriteByte('!')
			i++
		case c == '[':
			if out, end, ok := r.link(s, i); ok {
				sb.WriteString(out)
				i = end
				continue
			}
			sb.WriteByte('[')
			i++
		case c == '<':
			if m := reAutolink.FindStringSubmatch(s[i:]); m != nil {
				sb.WriteString(`<a href="` + Escape(m[1]) + `">` + Escape(strings.TrimPrefix(m[1], "mailto:")) + `</a>`)
				i += len(m[0])
				continue
			}
			if m := reHTMLTag.FindString(s[i:]); m != "" {
				sb.WriteString(m)
				i += len(m)
				continue
			}
			sb.WriteString("&lt;")
			i++
		case c == '&':
			if m := reEntity.FindString(s[i:]); m != "" {
				sb.WriteString(m)
				i += len(m)
				continue
			}
			sb.WriteString("&amp;")
			i++
		case c == '*' || c == '_' || c == '~':
			out, end, ok := r.emphasis(s, i)
			if ok {
				sb.WriteString(out)
				i = end
				continue
			}
			n := 0
			for i+n < len(s) && s[i+n] == c {
				n++
			}
			sb.WriteString(s[i : i+n])
			i += n
		case c == '\n':
			// Two trailing spaces make a hard break.
			trimmed := strings.TrimRight(sb.String(), " ")
			if len(sb.String())-len(trimmed) >= 2 {
				rest := trimmed
				sb.Reset()
				sb.WriteString(rest)
				sb.WriteString("<br />\n")
			} else {
				sb.WriteByte('\n')
			}
			i++
		case c == '>':
			sb.WriteString("&gt;")
			i++
		case c == '"':
			sb.WriteString("&quot;")
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String()
}

func (r *renderer) emphasis(s string, i int) (string, int, bool) {
	c := s[i]
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	if c == '~' {
		if n != 2 {
			return "", 0, false
		}
	}
	// Left-flanking: next char exists and is not whitespace.
	if i+n >= len(s) || isSpace(s[i+n]) {
		return "", 0, false
	}
	if c == '_' && i > 0 && isAlnum(s[i-1]) {
		return "", 0, false
	}
	try := func(width int, open, close string) (string, int, bool) {
		delim := strings.Repeat(string(c), width)
		j := findCloser(s, i+width, delim)
		if j < 0 {
			return "", 0, false
		}
		return open + r.inline(s[i+width:j]) + close, j + width, true
	}
	switch {
	case c == '~':
		return try(2, "<del>", "</del>")
	case n >= 3:
		if out, end, ok := try(3, "<strong><em>", "</em></strong>"); ok {
			return out, end, true
		}
		fallthrough
	case n == 2:
		if out, end, ok := try(2, "<strong>", "</strong>"); ok {
			return out, end, true
		}
		return "", 0, false
	default:
		return try(1, "<em>", "</em>")
	}
}

// link parses a link or reference starting at s[i] == '['.
func (r *renderer) link(s string, i int) (string, int, bool) {
	close := matchBracket(s, i)
	if close < 0 {
		return "", 0, false
	}
	text := s[i+1 : close]
	// Inline link [text](dest).
	if close+1 < len(s) && s[close+1] == '(' {
		if dest, title, end, ok := parseDest(s, close+1); ok {
			href := r.rewrite(unescapeBackslashes(dest))
			out := `<a href="` + Escape(href) + `"`
			if title != "" {
				out += ` title="` + Escape(title) + `"`
			}
			out += ">" + r.inline(text) + "</a>"
			return out, end, true
		}
	}
	if r.opts.ResolveRef == nil {
		return "", 0, false
	}
	// [label][target]
	if close+1 < len(s) && s[close+1] == '[' {
		if c2 := matchBracket(s, close+1); c2 > 0 {
			target := stripRefTarget(s[close+2 : c2])
			if href, title, ok := r.opts.ResolveRef(target); ok {
				return r.refLink(href, title, r.inline(text)), c2 + 1, true
			}
		}
	}
	// [`target`]
	target := stripRefTarget(text)
	if target != "" && strings.HasPrefix(strings.TrimSpace(text), "`") {
		if href, title, ok := r.opts.ResolveRef(target); ok {
			return r.refLink(href, title, r.inline(text)), close + 1, true
		}
	}
	return "", 0, false
}

func (r *renderer) refLink(href, title, inner string) string {
	out := `<a class="ref" href="` + Escape(href) + `"`
	if title != "" {
		out += ` title="` + Escape(title) + `"`
	}
	return out + ">" + inner + "</a>"
}

func stripRefTarget(t string) string {
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "`")
	return strings.TrimSpace(t)
}
