// Package lexer tokenizes Lowlight source files, handling string interpolation,
// whitespace-delimited unwind operators, and automatic semicolon insertion.
package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"lowlight/internal/diag"
	"lowlight/internal/token"
)

type delimKind int

const (
	delimBrace   delimKind = iota // '{'
	delimParen                    // '('
	delimBracket                  // '['
	delimInterp                   // '${' inside an interpolated string
)

type rawToken struct {
	tok           token.Token
	newlineBefore bool
	// blockLevelAfter is true when, immediately after this token is consumed,
	// we are either at top-level or the innermost enclosing delimiter is '{'.
	blockLevelAfter bool
}

// Lexer scans a single Lowlight source file into tokens.
type Lexer struct {
	file   string
	src    string
	offset int
	line   int
	col    int
	rep    *diag.Reporter

	delims  []delimKind
	rawToks []rawToken

	sawNewlineSinceLastTok bool
	prevWasSpaceOrStart    bool
}

// Lex tokenizes src and returns the final token slice (ending with token.EOF).
func Lex(filename, src string, rep *diag.Reporter) []token.Token {
	l := &Lexer{
		file:                filename,
		src:                 src,
		line:                1,
		col:                 1,
		rep:                 rep,
		prevWasSpaceOrStart: true,
	}
	l.scanAll()
	return l.insertSemicolons()
}

func (l *Lexer) pos() diag.Pos {
	return diag.Pos{
		File:   l.file,
		Offset: l.offset,
		Line:   l.line,
		Col:    l.col,
	}
}

func (l *Lexer) eof() bool {
	return l.offset >= len(l.src)
}

func (l *Lexer) peek() byte {
	if l.offset >= len(l.src) {
		return 0
	}
	return l.src[l.offset]
}

func (l *Lexer) peekAt(delta int) byte {
	idx := l.offset + delta
	if idx < 0 || idx >= len(l.src) {
		return 0
	}
	return l.src[idx]
}

func (l *Lexer) advance() rune {
	if l.offset >= len(l.src) {
		return 0
	}
	r, w := utf8.DecodeRuneInString(l.src[l.offset:])
	l.offset += w
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) isBlockLevel() bool {
	if len(l.delims) == 0 {
		return true
	}
	return l.delims[len(l.delims)-1] == delimBrace
}

func (l *Lexer) pushDelim(d delimKind) {
	l.delims = append(l.delims, d)
}

func (l *Lexer) popDelim(expected delimKind) {
	if len(l.delims) > 0 && l.delims[len(l.delims)-1] == expected {
		l.delims = l.delims[:len(l.delims)-1]
	}
}

func (l *Lexer) emit(kind token.Kind, text string, start, end diag.Pos) {
	l.rawToks = append(l.rawToks, rawToken{
		tok: token.Token{
			Kind: kind,
			Text: text,
			Span: diag.Span{Start: start, End: end},
		},
		newlineBefore:   l.sawNewlineSinceLastTok,
		blockLevelAfter: l.isBlockLevel(),
	})
	l.sawNewlineSinceLastTok = false
	l.prevWasSpaceOrStart = false
}

func (l *Lexer) skipWhitespaceAndComments() {
	for !l.eof() {
		ch := l.peek()
		if ch == ' ' || ch == '\t' || ch == '\r' {
			l.advance()
			l.prevWasSpaceOrStart = true
			continue
		}
		if ch == '\n' {
			l.advance()
			l.sawNewlineSinceLastTok = true
			l.prevWasSpaceOrStart = true
			continue
		}
		// Single-line comment //
		if ch == '/' && l.peekAt(1) == '/' {
			for !l.eof() && l.peek() != '\n' {
				l.advance()
			}
			l.prevWasSpaceOrStart = true
			continue
		}
		// Multi-line comment /* ... */
		if ch == '/' && l.peekAt(1) == '*' {
			start := l.pos()
			l.advance()
			l.advance()
			closed := false
			for !l.eof() {
				if l.peek() == '\n' {
					l.sawNewlineSinceLastTok = true
				}
				if l.peek() == '*' && l.peekAt(1) == '/' {
					l.advance()
					l.advance()
					closed = true
					break
				}
				l.advance()
			}
			if !closed {
				l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "unterminated multi-line comment")
			}
			l.prevWasSpaceOrStart = true
			continue
		}
		break
	}
}

func (l *Lexer) scanAll() {
	for {
		l.skipWhitespaceAndComments()
		if l.eof() {
			pos := l.pos()
			l.emit(token.EOF, "", pos, pos)
			return
		}
		l.scanOne()
	}
}

func (l *Lexer) scanOne() {
	start := l.pos()
	ch := l.peek()

	// Identifiers & Keywords
	if isIdentStart(ch) {
		l.scanIdent()
		return
	}

	// Numeric Literals
	if ch >= '0' && ch <= '9' {
		l.scanNumber()
		return
	}

	// Character Literal
	if ch == '\'' {
		l.scanChar()
		return
	}

	// String Literal (plain or interpolated)
	if ch == '"' {
		l.scanString()
		return
	}

	// Hash-bracket #[
	if ch == '#' && l.peekAt(1) == '[' {
		l.advance()
		l.advance()
		l.pushDelim(delimBracket)
		l.emit(token.HashBrack, "#[", start, l.pos())
		return
	}

	switch ch {
	case '(':
		l.advance()
		l.pushDelim(delimParen)
		l.emit(token.LParen, "(", start, l.pos())
	case ')':
		l.advance()
		l.popDelim(delimParen)
		l.emit(token.RParen, ")", start, l.pos())
	case '[':
		l.advance()
		l.pushDelim(delimBracket)
		l.emit(token.LBracket, "[", start, l.pos())
	case ']':
		l.advance()
		l.popDelim(delimBracket)
		l.emit(token.RBracket, "]", start, l.pos())
	case '{':
		l.advance()
		l.pushDelim(delimBrace)
		l.emit(token.LBrace, "{", start, l.pos())
	case '}':
		// If the innermost delimiter is '${' from string interpolation,
		// return control to scanStringRest instead of emitting RBrace!
		if len(l.delims) > 0 && l.delims[len(l.delims)-1] == delimInterp {
			l.advance()
			l.popDelim(delimInterp)
			l.scanStringContinuation()
			return
		}
		l.advance()
		l.popDelim(delimBrace)
		l.emit(token.RBrace, "}", start, l.pos())
	case ',':
		l.advance()
		l.emit(token.Comma, ",", start, l.pos())
	case ':':
		l.advance()
		l.emit(token.Colon, ":", start, l.pos())
	case ';':
		l.advance()
		l.emit(token.Semi, ";", start, l.pos())
	case '.':
		l.advance()
		if l.peek() == '.' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.emit(token.DotDotEq, "..=", start, l.pos())
				return
			}
			l.emit(token.DotDot, "..", start, l.pos())
			return
		}
		l.emit(token.Dot, ".", start, l.pos())
	case '?':
		l.advance()
		if l.peek() == '?' {
			l.advance()
			l.emit(token.QuestQuest, "??", start, l.pos())
			return
		}
		if l.peek() == '.' {
			l.advance()
			l.emit(token.QuestDot, "?.", start, l.pos())
			return
		}
		l.emit(token.Question, "?", start, l.pos())
	case '=':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.Eq, "==", start, l.pos())
			return
		}
		l.emit(token.Assign, "=", start, l.pos())
	case '!':
		if l.peekAt(1) == '=' {
			l.advance()
			l.advance()
			l.emit(token.Neq, "!=", start, l.pos())
			return
		}
		l.advance()
		l.rep.ErrorWithHint(
			diag.Span{Start: start, End: l.pos()},
			"unexpected '!'; logical negation in Lowlight uses 'not'",
			"write `not expr` instead of `!expr`",
		)
		l.emit(token.Illegal, "!", start, l.pos())
	case '+':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.PlusEq, "+=", start, l.pos())
			return
		}
		l.emit(token.Plus, "+", start, l.pos())
	case '-':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.MinusEq, "-=", start, l.pos())
			return
		}
		l.emit(token.Minus, "-", start, l.pos())
	case '*':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.StarEq, "*=", start, l.pos())
			return
		}
		l.emit(token.Star, "*", start, l.pos())
	case '/':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.SlashEq, "/=", start, l.pos())
			return
		}
		l.emit(token.Slash, "/", start, l.pos())
	case '%':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.PercentEq, "%=", start, l.pos())
			return
		}
		l.emit(token.Percent, "%", start, l.pos())
	case '&':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.AmpEq, "&=", start, l.pos())
			return
		}
		l.emit(token.Amp, "&", start, l.pos())
	case '|':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.PipeEq, "|=", start, l.pos())
			return
		}
		l.emit(token.Pipe, "|", start, l.pos())
	case '^':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.CaretEq, "^=", start, l.pos())
			return
		}
		l.emit(token.Caret, "^", start, l.pos())
	case '~':
		l.advance()
		l.emit(token.Tilde, "~", start, l.pos())
	case '<':
		// Check whitespace-delimited `<-` rule (Spec §10.4):
		// `<-` requires preceding whitespace (or start of file) and trailing whitespace
		// (or statement-ending delimiter `;`, `}`, `)`, `,`, comment, or EOF).
		if l.peekAt(1) == '-' && l.prevWasSpaceOrStart && l.validAfterUnwind(l.peekAt(2), l.peekAt(3)) {
			l.advance()
			l.advance()
			l.emit(token.Unwind, "<-", start, l.pos())
			return
		}
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.Lte, "<=", start, l.pos())
			return
		}
		if l.peek() == '<' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.emit(token.ShlEq, "<<=", start, l.pos())
				return
			}
			l.emit(token.Shl, "<<", start, l.pos())
			return
		}
		l.emit(token.Lt, "<", start, l.pos())
	case '>':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.emit(token.Gte, ">=", start, l.pos())
			return
		}
		if l.peek() == '>' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.emit(token.ShrEq, ">>=", start, l.pos())
				return
			}
			l.emit(token.Shr, ">>", start, l.pos())
			return
		}
		l.emit(token.Gt, ">", start, l.pos())
	default:
		r := l.advance()
		l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "unexpected character %q", r)
		l.emit(token.Illegal, string(r), start, l.pos())
	}
}

func (l *Lexer) validAfterUnwind(c1, c2 byte) bool {
	switch c1 {
	case 0, ' ', '\t', '\r', '\n', ';', '}', ')', ',':
		return true
	case '/':
		return c2 == '/' || c2 == '*'
	default:
		return false
	}
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentCont(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func (l *Lexer) scanIdent() {
	start := l.pos()
	startOff := l.offset
	for !l.eof() && isIdentCont(l.peek()) {
		l.advance()
	}
	word := l.src[startOff:l.offset]

	// Any identifier word immediately followed by `?` (and not `??`, unless `???`)
	// is an optional identifier (`user?`, `next?`), never a bare keyword!
	if l.peek() == '?' && (l.peekAt(1) != '?' || l.peekAt(2) == '?') {
		l.advance()
		l.emit(token.OptIdent, word+"?", start, l.pos())
		return
	}

	kw := token.LookupKeyword(word)
	if kw != token.Ident {
		l.emit(kw, word, start, l.pos())
		return
	}

	l.emit(token.Ident, word, start, l.pos())
}

func (l *Lexer) scanNumber() {
	start := l.pos()
	startOff := l.offset

	if l.peek() == '0' && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X') {
		l.advance()
		l.advance()
		digits := 0
		for !l.eof() && (isHexDigit(l.peek()) || l.peek() == '_') {
			if isHexDigit(l.peek()) {
				digits++
			}
			l.advance()
		}
		if digits == 0 {
			l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "hexadecimal literal requires at least one digit")
		}
		l.emit(token.IntLit, l.src[startOff:l.offset], start, l.pos())
		return
	}

	if l.peek() == '0' && (l.peekAt(1) == 'b' || l.peekAt(1) == 'B') {
		l.advance()
		l.advance()
		digits := 0
		for !l.eof() && (l.peek() == '0' || l.peek() == '1' || l.peek() == '_') {
			if l.peek() == '0' || l.peek() == '1' {
				digits++
			}
			l.advance()
		}
		if digits == 0 {
			l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "binary literal requires at least one digit")
		}
		l.emit(token.IntLit, l.src[startOff:l.offset], start, l.pos())
		return
	}

	isFloat := false
	for !l.eof() && ((l.peek() >= '0' && l.peek() <= '9') || l.peek() == '_') {
		l.advance()
	}

	// Fractional part `.digits`, careful not to consume `..` range or `.method()`!
	if l.peek() == '.' && l.peekAt(1) >= '0' && l.peekAt(1) <= '9' {
		isFloat = true
		l.advance()
		for !l.eof() && ((l.peek() >= '0' && l.peek() <= '9') || l.peek() == '_') {
			l.advance()
		}
	}

	// Scientific exponent `e` / `E`
	if l.peek() == 'e' || l.peek() == 'E' {
		isFloat = true
		l.advance()
		if l.peek() == '+' || l.peek() == '-' {
			l.advance()
		}
		expDigits := 0
		for !l.eof() && ((l.peek() >= '0' && l.peek() <= '9') || l.peek() == '_') {
			if l.peek() >= '0' && l.peek() <= '9' {
				expDigits++
			}
			l.advance()
		}
		if expDigits == 0 {
			l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "malformed exponent in floating-point literal")
		}
	}

	raw := l.src[startOff:l.offset]
	if isFloat {
		l.emit(token.FloatLit, raw, start, l.pos())
	} else {
		l.emit(token.IntLit, raw, start, l.pos())
	}
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func (l *Lexer) scanChar() {
	start := l.pos()
	l.advance() // consume opening '\''
	if l.eof() || l.peek() == '\n' {
		l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "unterminated character literal")
		l.emit(token.CharLit, "\x00", start, l.pos())
		return
	}
	if l.peek() == '\'' {
		l.advance()
		l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "empty character literal")
		l.emit(token.CharLit, "\x00", start, l.pos())
		return
	}
	var r rune
	if l.peek() == '\\' {
		r = l.scanEscape(false)
	} else {
		r = l.advance()
	}
	if l.peek() != '\'' {
		for !l.eof() && l.peek() != '\'' && l.peek() != '\n' {
			l.advance()
		}
		if l.peek() == '\'' {
			l.advance()
		}
		l.rep.Errorf(diag.Span{Start: start, End: l.pos()}, "character literal must contain a single character")
	} else {
		l.advance() // closing '\''
	}
	l.emit(token.CharLit, string(r), start, l.pos())
}

func (l *Lexer) scanEscape(inString bool) rune {
	escStart := l.pos()
	l.advance() // consume '\\'
	if l.eof() {
		l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, "unterminated escape sequence")
		return 0
	}
	ch := l.advance()
	switch ch {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case '\\':
		return '\\'
	case '\'':
		return '\''
	case '"':
		return '"'
	case '0':
		return 0
	case '$':
		if inString {
			return '$'
		}
		l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, `invalid escape \$ in character literal`)
		return '$'
	case 'x':
		h1 := l.peek()
		h2 := l.peekAt(1)
		if !isHexDigit(h1) || !isHexDigit(h2) {
			l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, `\x escape requires two hex digits`)
			return 0
		}
		l.advance()
		l.advance()
		val, _ := strconv.ParseUint(string([]byte{h1, h2}), 16, 8)
		return rune(val)
	case 'u':
		if l.peek() != '{' {
			l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, `\u escape requires braces: \u{HEX}`)
			return 0
		}
		l.advance() // '{'
		hexStart := l.offset
		for !l.eof() && isHexDigit(l.peek()) {
			l.advance()
		}
		hexStr := l.src[hexStart:l.offset]
		if l.peek() != '}' || len(hexStr) == 0 || len(hexStr) > 6 {
			l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, `invalid Unicode escape \u{...}`)
			if l.peek() == '}' {
				l.advance()
			}
			return 0
		}
		l.advance() // '}'
		val, err := strconv.ParseUint(hexStr, 16, 32)
		if err != nil || val > unicode.MaxRune || (val >= 0xD800 && val <= 0xDFFF) {
			l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, "invalid Unicode scalar value in escape")
			return 0
		}
		return rune(val)
	default:
		l.rep.Errorf(diag.Span{Start: escStart, End: l.pos()}, "unknown escape sequence \\%c", ch)
		return ch
	}
}

// scanString scans a string starting at opening `"`.
func (l *Lexer) scanString() {
	l.advance() // consume opening `"`
	l.scanStringSegment(true)
}

// scanStringContinuation resumes scanning a string literal immediately after the closing `}` of `${...}`.
func (l *Lexer) scanStringContinuation() {
	l.scanStringSegment(false)
}

func (l *Lexer) scanStringSegment(isFirst bool) {
	segStart := l.pos()
	var sb strings.Builder

	for {
		if l.eof() {
			l.rep.Errorf(diag.Span{Start: segStart, End: l.pos()}, "unterminated string literal")
			if isFirst {
				l.emit(token.StringLit, sb.String(), segStart, l.pos())
			} else {
				l.emit(token.InterpEnd, sb.String(), segStart, l.pos())
			}
			return
		}

		ch := l.peek()
		if ch == '"' {
			l.advance()
			if isFirst {
				l.emit(token.StringLit, sb.String(), segStart, l.pos())
			} else {
				l.emit(token.InterpEnd, sb.String(), segStart, l.pos())
			}
			return
		}

		if ch == '\\' {
			if l.peekAt(1) == 'x' {
				// \xHH in UTF-8 / raw byte strings writes the exact byte
				escPos := l.pos()
				l.advance() // '\\'
				l.advance() // 'x'
				h1, h2 := l.peek(), l.peekAt(1)
				if !isHexDigit(h1) || !isHexDigit(h2) {
					l.rep.Errorf(diag.Span{Start: escPos, End: l.pos()}, `\x escape requires two hex digits`)
				} else {
					l.advance()
					l.advance()
					val, _ := strconv.ParseUint(string([]byte{h1, h2}), 16, 8)
					sb.WriteByte(byte(val))
				}
				continue
			}
			r := l.scanEscape(true)
			sb.WriteRune(r)
			continue
		}

		if ch == '$' {
			// `${expr}` interpolation
			if l.peekAt(1) == '{' {
				segEnd := l.pos()
				if isFirst {
					l.emit(token.InterpStart, sb.String(), segStart, segEnd)
				} else {
					l.emit(token.InterpMid, sb.String(), segStart, segEnd)
				}
				l.advance() // '$'
				l.advance() // '{'
				l.pushDelim(delimInterp)
				l.prevWasSpaceOrStart = true
				return
			}

			// `$ident` interpolation (never consumes trailing `?`, per Spec §4.2!)
			if isIdentStart(l.peekAt(1)) {
				segEnd := l.pos()
				if isFirst {
					l.emit(token.InterpStart, sb.String(), segStart, segEnd)
				} else {
					l.emit(token.InterpMid, sb.String(), segStart, segEnd)
				}
				l.advance() // consume '$'
				idStart := l.pos()
				idStartOff := l.offset
				for !l.eof() && isIdentCont(l.peek()) {
					l.advance()
				}
				idWord := l.src[idStartOff:l.offset]
				kw := token.LookupKeyword(idWord)
				if kw != token.Ident && kw != token.KwSelf {
					l.rep.Errorf(diag.Span{Start: idStart, End: l.pos()}, "cannot interpolate reserved keyword %q", idWord)
				}
				if kw == token.KwSelf {
					l.emit(token.KwSelf, idWord, idStart, l.pos())
				} else {
					l.emit(token.Ident, idWord, idStart, l.pos())
				}
				// Immediately continue scanning the rest of this string literal!
				isFirst = false
				segStart = l.pos()
				sb.Reset()
				continue
			}

			// Bare `$` not followed by `{` or identifier start is an error (use `\$`)
			errStart := l.pos()
			l.advance()
			l.rep.ErrorWithHint(
				diag.Span{Start: errStart, End: l.pos()},
				"unescaped '$' in string literal",
				`write \$ for a literal dollar sign, or $name / ${expr} for interpolation`,
			)
			sb.WriteByte('$')
			continue
		}

		r := l.advance()
		sb.WriteRune(r)
	}
}

func (l *Lexer) insertSemicolons() []token.Token {
	out := make([]token.Token, 0, len(l.rawToks)+16)
	for i, rt := range l.rawToks {
		out = append(out, rt.tok)
		if rt.tok.Kind == token.EOF {
			break
		}
		if i+1 >= len(l.rawToks) {
			continue
		}
		next := l.rawToks[i+1]

		// Spec §2.3:
		// 1. Only at top-level or when innermost enclosing delimiter is '{'.
		if !rt.blockLevelAfter {
			continue
		}
		// 2. Only when a newline (or EOF) separates this token from the next.
		if !next.newlineBefore && next.tok.Kind != token.EOF {
			continue
		}
		// 3. Only when this token triggers semicolon insertion.
		if !rt.tok.Kind.TriggersAutoSemi() {
			continue
		}
		// 4. Suppressed if the next line begins with `else`, `when`, `.`, or `?.`.
		if next.tok.Kind.SuppressesAutoSemiOnNextLine() {
			continue
		}
		// Avoid redundant `;` right before `}` so single-expression blocks stay clean,
		// wait: inside `{ let a = 1\n }`, DOES `let a = 1` before `}` want `;` or does parser handle optional `;` before `}`?
		// Wait: Think carefully!
		// In `{ let a = 1; a + 2 }`:
		// - `let a = 1\n a + 2\n }`:
		//   If `;` is inserted after `a + 2` before `}`, how does the parser distinguish:
		//   `{ a + 2 }` (single-line value block, NO `;`) vs
		//   `{\n a + 2\n }` (multiline value block, newline before `}`) vs
		//   `{\n a + 2;\n }` (explicit `;` discarding value? Wait: in Lowlight statements vs expressions are distinct! `let`, `=`, `<-` are statements; `a + 2` is an expression!).
		// Checking `AutoSemi`: `Token.AutoSemi = true` tells the parser whether a `;` was explicit or newline-synthesized!
		semiPos := rt.tok.Span.End
		out = append(out, token.Token{
			Kind:     token.Semi,
			Text:     ";",
			Span:     diag.Span{Start: semiPos, End: semiPos},
			AutoSemi: true,
		})
	}
	return out
}

// ParseIntLiteral parses a Lowlight integer literal (`42`, `0xFF_00`, `0b1010`) into uint64.
func ParseIntLiteral(raw string) (uint64, error) {
	clean := strings.ReplaceAll(raw, "_", "")
	if strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X") {
		return strconv.ParseUint(clean[2:], 16, 64)
	}
	if strings.HasPrefix(clean, "0b") || strings.HasPrefix(clean, "0B") {
		return strconv.ParseUint(clean[2:], 2, 64)
	}
	val, err := strconv.ParseUint(clean, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("integer literal %s out of range", raw)
	}
	return val, nil
}

// ParseFloatLiteral parses a Lowlight floating-point literal (`3.141_592`, `1.5e-3`) into float64.
func ParseFloatLiteral(raw string) (float64, error) {
	clean := strings.ReplaceAll(raw, "_", "")
	return strconv.ParseFloat(clean, 64)
}
