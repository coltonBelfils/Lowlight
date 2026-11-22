package site

import (
	"regexp"
	"sort"
	"strings"

	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/markdown"
	"lowlight/internal/token"
)

// Highlight renders a fenced code block. It understands Lowlight (using the real
// lexer, so highlighting always matches the language), TOML, shell sessions, and EBNF.
func Highlight(lang, code string) (string, bool) {
	switch lang {
	case "lowlight", "ll", "":
		if lang == "" && !looksLikeLowlight(code) {
			return "", false
		}
		return HighlightLowlight(code), true
	case "toml":
		return highlightRegex(code, tomlRules), true
	case "bash", "sh", "shell", "console":
		return highlightRegex(code, shellRules), true
	case "ebnf":
		return highlightRegex(code, ebnfRules), true
	}
	return "", false
}

func looksLikeLowlight(code string) bool {
	return strings.Contains(code, "bind ") || strings.Contains(code, "define ") || strings.Contains(code, "let ")
}

var primitiveNames = map[string]bool{
	"int": true, "i8": true, "i16": true, "i32": true, "i64": true,
	"u8": true, "u16": true, "u32": true, "u64": true,
	"float": true, "f32": true, "f64": true, "bool": true, "char": true,
	"string": true, "void": true, "never": true, "ptr": true, "ref": true,
}

// HighlightLowlight returns Lowlight source as HTML with token classes.
func HighlightLowlight(code string) string {
	rep := diag.NewReporter()
	toks, comments := lexer.LexWithComments("<doc>", code, rep)
	type span struct {
		start, end int
		cls        string
	}
	var spans []span
	var real []token.Token
	for _, tk := range toks {
		if tk.AutoSemi || tk.Kind == token.EOF || tk.Span.End.Offset <= tk.Span.Start.Offset || tk.Span.End.Offset > len(code) {
			continue
		}
		real = append(real, tk)
	}
	for i, tk := range real {
		var cls string
		next := func(k int) token.Token {
			if i+k < len(real) {
				return real[i+k]
			}
			return token.Token{}
		}
		switch {
		case tk.Kind == token.KwTrue || tk.Kind == token.KwFalse || tk.Kind == token.KwNone:
			cls = "c"
		case tk.Kind >= token.KwLet && tk.Kind <= token.KwImport, tk.Kind == token.KwSelf, tk.Kind == token.KwSelfType:
			cls = "k"
		case tk.Kind == token.IntLit || tk.Kind == token.FloatLit:
			cls = "n"
		case tk.Kind == token.CharLit || tk.Kind == token.StringLit || tk.Kind == token.InterpStart || tk.Kind == token.InterpMid || tk.Kind == token.InterpEnd:
			cls = "s"
		case tk.Kind == token.At:
			cls = "a"
			if n := next(1); n.Kind == token.Ident && n.Span.Start.Offset == tk.Span.End.Offset {
				spans = append(spans, span{tk.Span.Start.Offset, n.Span.End.Offset, "a"})
				continue
			}
		case tk.Kind == token.Ident || tk.Kind == token.OptIdent:
			txt := tk.Text
			switch {
			case (txt == "default" || txt == "next") && (next(1).Kind == token.Colon || next(1).Kind == token.Ident || next(1).Text == "<-"):
				cls = "k"
			case txt == "panic" && i > 0 && real[i-1].Text == "default":
				cls = "k"
			case txt == "result" || txt == "packed":
				if i > 0 && (real[i-1].Kind == token.Ident || real[i-1].Kind == token.RBracket || real[i-1].Kind == token.KwDefine) {
					cls = "k"
				}
			case len(txt) > 0 && txt[0] >= 'A' && txt[0] <= 'Z':
				cls = "t"
			case primitiveNames[txt]:
				cls = "t"
			case next(1).Kind == token.LParen:
				cls = "f"
			}
		default:
			if tk.Text == "<-" {
				cls = "u"
			}
		}
		if cls == "" {
			continue
		}
		s, e := tk.Span.Start.Offset, tk.Span.End.Offset
		if tk.Kind == token.StringLit || tk.Kind == token.InterpStart {
			if s > 0 && (code[s-1] == '"' || code[s-1] == '`') {
				s--
			}
		}
		spans = append(spans, span{s, e, cls})
	}
	for _, c := range comments {
		if c.Span.End.Offset > c.Span.Start.Offset && c.Span.End.Offset <= len(code) {
			spans = append(spans, span{c.Span.Start.Offset, c.Span.End.Offset, "m"})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	var sb strings.Builder
	pos := 0
	for _, sp := range spans {
		if sp.start < pos || sp.end > len(code) {
			continue
		}
		sb.WriteString(markdown.Escape(code[pos:sp.start]))
		sb.WriteString(`<span class="` + sp.cls + `">`)
		sb.WriteString(markdown.Escape(code[sp.start:sp.end]))
		sb.WriteString(`</span>`)
		pos = sp.end
	}
	sb.WriteString(markdown.Escape(code[pos:]))
	return sb.String()
}

type hlRule struct {
	re  *regexp.Regexp
	cls string
}

var (
	tomlRules = []hlRule{
		{regexp.MustCompile(`^#.*`), "m"},
		{regexp.MustCompile(`^\[\[?[^\]]*\]\]?`), "t"},
		{regexp.MustCompile(`^"(?:[^"\\]|\\.)*"`), "s"},
		{regexp.MustCompile(`^\b(true|false)\b`), "c"},
		{regexp.MustCompile(`^\b[0-9][0-9_.]*\b`), "n"},
		{regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*(\s*=)`), "k"},
	}
	shellRules = []hlRule{
		{regexp.MustCompile(`^#.*`), "m"},
		{regexp.MustCompile(`^"(?:[^"\\]|\\.)*"`), "s"},
		{regexp.MustCompile(`^'[^']*'`), "s"},
		{regexp.MustCompile(`^\$ `), "u"},
		{regexp.MustCompile(`^\b(lowlight|go|git|clang|cd|export|brew|apt|sudo|nvim|code)\b`), "f"},
		{regexp.MustCompile(`^--?[a-zA-Z][a-zA-Z0-9-]*`), "a"},
	}
	ebnfRules = []hlRule{
		{regexp.MustCompile(`^\(\*.*?\*\)`), "m"},
		{regexp.MustCompile(`^"[^"]*"`), "s"},
		{regexp.MustCompile(`^'[^']*'`), "s"},
		{regexp.MustCompile(`^[A-Z][A-Za-z]*`), "t"},
	}
)

func highlightRegex(code string, rules []hlRule) string {
	var sb strings.Builder
	i := 0
	atWordStart := true
	for i < len(code) {
		matched := false
		if atWordStart || code[i] == '"' || code[i] == '\'' || code[i] == '#' || code[i] == '(' || code[i] == '[' || code[i] == '-' || code[i] == '$' {
			for _, r := range rules {
				if loc := r.re.FindStringSubmatchIndex(code[i:]); loc != nil && loc[1] > 0 {
					end := loc[1]
					// For "key =" rules, color only the key.
					if len(loc) >= 4 && loc[2] >= 0 && r.cls == "k" {
						end = loc[2]
					}
					sb.WriteString(`<span class="` + r.cls + `">` + markdown.Escape(code[i:i+end]) + `</span>`)
					i += end
					matched = true
					break
				}
			}
		}
		if matched {
			atWordStart = false
			continue
		}
		c := code[i]
		atWordStart = !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_')
		sb.WriteString(markdown.Escape(string(c)))
		i++
	}
	return sb.String()
}
