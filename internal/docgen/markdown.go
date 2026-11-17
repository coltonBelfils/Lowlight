package docgen

import (
	"fmt"
	"strings"
)

// RenderDocHTML converts a Markdown doc comment into HTML, resolving `[`Symbol`]`
// intra-doc links relative to currentModPath and currentType using idx.
func RenderDocHTML(doc, currentModPath, currentType string, idx *SymbolIndex) string {
	if strings.TrimSpace(doc) == "" {
		return ""
	}
	lines := strings.Split(doc, "\n")
	var out strings.Builder

	var paraBuf []string
	var listItems []string
	inCode := false
	codeLang := ""
	var codeLines []string

	flushPara := func() {
		if len(paraBuf) == 0 {
			return
		}
		text := strings.Join(paraBuf, " ")
		out.WriteString("<p>")
		out.WriteString(renderInlineMarkdown(text, currentModPath, currentType, idx))
		out.WriteString("</p>\n")
		paraBuf = nil
	}

	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		out.WriteString("<ul>\n")
		for _, item := range listItems {
			out.WriteString("<li>")
			out.WriteString(renderInlineMarkdown(item, currentModPath, currentType, idx))
			out.WriteString("</li>\n")
		}
		out.WriteString("</ul>\n")
		listItems = nil
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)

		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				flushPara()
				flushList()
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				codeLines = nil
			} else {
				inCode = false
				code := strings.Join(codeLines, "\n")
				out.WriteString(`<pre class="doc-code"><code>`)
				if codeLang == "" || codeLang == "lowlight" || codeLang == "ll" {
					out.WriteString(HighlightLowlightHTML(code))
				} else {
					out.WriteString(htmlEscape(code))
				}
				out.WriteString("</code></pre>\n")
				codeLines = nil
				codeLang = ""
			}
			continue
		}

		if inCode {
			codeLines = append(codeLines, rawLine)
			continue
		}

		if trimmed == "" {
			flushPara()
			flushList()
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			flushPara()
			flushList()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			title := strings.TrimSpace(trimmed[level:])
			hTag := "h4"
			if level >= 3 {
				hTag = "h5"
			}
			out.WriteString(fmt.Sprintf(`<%s class="doc-section">%s</%s>`+"\n", hTag, renderInlineMarkdown(title, currentModPath, currentType, idx), hTag))
			continue
		}

		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushPara()
			listItems = append(listItems, strings.TrimSpace(trimmed[2:]))
			continue
		}

		flushList()
		paraBuf = append(paraBuf, trimmed)
	}

	if inCode && len(codeLines) > 0 {
		code := strings.Join(codeLines, "\n")
		out.WriteString(`<pre class="doc-code"><code>`)
		out.WriteString(HighlightLowlightHTML(code))
		out.WriteString("</code></pre>\n")
	}
	flushPara()
	flushList()
	return out.String()
}

func renderInlineMarkdown(s, currentModPath, currentType string, idx *SymbolIndex) string {
	// Resolve `[`Symbol`]` and `[label][`Symbol`]` links first using placeholder tokens,
	// or render segment-by-segment around doc links so HTML links aren't double-escaped.
	links := ParseDocLinks(s)
	if len(links) == 0 {
		return renderInlinePlainMarkdown(s)
	}
	var sb strings.Builder
	pos := 0
	for _, lk := range links {
		if lk.Start > pos {
			sb.WriteString(renderInlinePlainMarkdown(s[pos:lk.Start]))
		}
		entry, isBuiltin := idx.ResolveLink(lk.Target, currentModPath, currentType)
		if entry != nil && entry.HTMLHref != "" {
			sb.WriteString(fmt.Sprintf(`<a class="doc-link" href="%s" title="%s"><code>%s</code></a>`,
				htmlEscape(entry.HTMLHref), htmlEscape(entry.Signature), htmlEscape(lk.Label)))
		} else if isBuiltin {
			sb.WriteString(fmt.Sprintf(`<code class="doc-builtin" title="%s">%s</code>`,
				htmlEscape(builtinUniverseTargets[stripLinkDecorations(lk.Target)]), htmlEscape(lk.Label)))
		} else {
			sb.WriteString(fmt.Sprintf(`<code class="doc-unresolved">%s</code>`, htmlEscape(lk.Label)))
		}
		pos = lk.End
	}
	if pos < len(s) {
		sb.WriteString(renderInlinePlainMarkdown(s[pos:]))
	}
	return sb.String()
}

func renderInlinePlainMarkdown(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		// Inline code `...`
		if s[i] == '`' {
			end := strings.IndexByte(s[i+1:], '`')
			if end >= 0 {
				code := s[i+1 : i+1+end]
				sb.WriteString("<code>")
				sb.WriteString(htmlEscape(code))
				sb.WriteString("</code>")
				i = i + 1 + end + 1
				continue
			}
		}
		// Standard markdown link [label](url)
		if s[i] == '[' {
			closeBr := strings.IndexByte(s[i+1:], ']')
			if closeBr >= 0 {
				after := i + 1 + closeBr + 1
				if after < len(s) && s[after] == '(' {
					closeParen := strings.IndexByte(s[after+1:], ')')
					if closeParen >= 0 {
						label := s[i+1 : i+1+closeBr]
						url := s[after+1 : after+1+closeParen]
						sb.WriteString(fmt.Sprintf(`<a href="%s">%s</a>`, htmlEscape(url), htmlEscape(label)))
						i = after + 1 + closeParen + 1
						continue
					}
				}
			}
		}
		// Bold **...**
		if i+1 < len(s) && s[i] == '*' && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "**")
			if end > 0 {
				sb.WriteString("<strong>")
				sb.WriteString(htmlEscape(s[i+2 : i+2+end]))
				sb.WriteString("</strong>")
				i = i + 2 + end + 2
				continue
			}
		}
		// Escape normal byte-by-byte / up to next special
		switch s[i] {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&quot;")
		default:
			sb.WriteByte(s[i])
		}
		i++
	}
	return sb.String()
}
