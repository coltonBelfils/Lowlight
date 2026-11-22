// Package token defines the lexical tokens and reserved keywords of Lowlight.
package token

import "lowlight/internal/diag"

// Kind identifies the category of a lexical token.
type Kind int

const (
	EOF Kind = iota
	Illegal

	// Identifiers & Literals
	Ident       // foo, Point, int
	OptIdent    // user?, next? (identifier with mandatory '?' suffix)
	IntLit      // 42, 0xFF, 0b1010, 1_000
	FloatLit    // 3.14, 1.5e-3
	CharLit     // 'a', '\n'
	StringLit   // plain string without interpolation: "hello"
	InterpStart // start of interpolated string segment before first $...
	InterpMid   // literal text segment between two interpolations
	InterpEnd   // trailing literal text segment after last interpolation

	// Delimiters
	LParen    // (
	RParen    // )
	LBracket  // [
	RBracket  // ]
	HashBrack // #[
	PlusBrack // +[
	LBrace    // {
	RBrace    // }
	Comma     // ,
	Colon     // :
	Semi      // ; (explicit or automatically inserted at newline)
	Dot       // .
	QuestDot  // ?.
	DotDot    // ..
	DotDotEq  // ..=
	At        // @

	// Operators & Unwind
	Unwind     // <- (whitespace-delimited)
	Question   // ?  (prefix optional type `?T`)
	QuestQuest // ?? (optional fallback)
	Assign     // =
	Plus       // +
	Minus      // -
	Star       // *
	Slash      // /
	Percent    // %
	Amp        // &  (prefix mutable borrow OR infix bitwise AND)
	Pipe       // |
	Caret      // ^
	Tilde      // ~
	Shl        // <<
	Shr        // >>

	// Compound Assignments
	PlusEq    // +=
	MinusEq   // -=
	StarEq    // *=
	SlashEq   // /=
	PercentEq // %=
	AmpEq     // &=
	PipeEq    // |=
	CaretEq   // ^=
	ShlEq     // <<=
	ShrEq     // >>=

	// Comparisons
	Eq  // ==
	Neq // !=
	Lt  // <
	Lte // <=
	Gt  // >
	Gte // >=

	// Reserved Keywords (30)
	KwLet
	KwBind
	KwDefine
	KwStruct
	KwEnum
	KwInterface
	KwIf
	KwElse
	KwFor
	KwWhile
	KwIn
	KwMatch
	KwWhen
	KwFunc
	KwNone
	KwTrue
	KwFalse
	KwSelf
	KwSelfType // Self
	KwOwn
	KwAnd
	KwOr
	KwNot
	KwAs
	KwUnsafe
	KwExtern
	KwUnpack
	KwFn // fn: a function value with no captures
	KwCl // cl: a closure
	KwImport
)

var kindNames = map[Kind]string{
	EOF:         "EOF",
	Illegal:     "ILLEGAL",
	Ident:       "identifier",
	OptIdent:    "optional identifier",
	IntLit:      "integer literal",
	FloatLit:    "float literal",
	CharLit:     "char literal",
	StringLit:   "string literal",
	InterpStart: "interpolated string start",
	InterpMid:   "interpolated string middle",
	InterpEnd:   "interpolated string end",
	LParen:      "(",
	RParen:      ")",
	LBracket:    "[",
	RBracket:    "]",
	HashBrack:   "#[",
	PlusBrack:   "+[",
	LBrace:      "{",
	RBrace:      "}",
	Comma:       ",",
	Colon:       ":",
	Semi:        ";",
	Dot:         ".",
	QuestDot:    "?.",
	DotDot:      "..",
	DotDotEq:    "..=",
	At:          "@",
	Unwind:      "<-",
	Question:    "?",
	QuestQuest:  "??",
	Assign:      "=",
	Plus:        "+",
	Minus:       "-",
	Star:        "*",
	Slash:       "/",
	Percent:     "%",
	Amp:         "&",
	Pipe:        "|",
	Caret:       "^",
	Tilde:       "~",
	Shl:         "<<",
	Shr:         ">>",
	PlusEq:      "+=",
	MinusEq:     "-=",
	StarEq:      "*=",
	SlashEq:     "/=",
	PercentEq:   "%=",
	AmpEq:       "&=",
	PipeEq:      "|=",
	CaretEq:     "^=",
	ShlEq:       "<<=",
	ShrEq:       ">>=",
	Eq:          "==",
	Neq:         "!=",
	Lt:          "<",
	Lte:         "<=",
	Gt:          ">",
	Gte:         ">=",
	KwLet:       "let",
	KwBind:      "bind",
	KwDefine:    "define",
	KwStruct:    "struct",
	KwEnum:      "enum",
	KwInterface: "interface",
	KwIf:        "if",
	KwElse:      "else",
	KwFor:       "for",
	KwWhile:     "while",
	KwIn:        "in",
	KwMatch:     "match",
	KwWhen:      "when",
	KwFunc:      "func",
	KwNone:      "none",
	KwTrue:      "true",
	KwFalse:     "false",
	KwSelf:      "self",
	KwSelfType:  "Self",
	KwOwn:       "own",
	KwAnd:       "and",
	KwOr:        "or",
	KwNot:       "not",
	KwAs:        "as",
	KwUnsafe:    "unsafe",
	KwExtern:    "extern",
	KwUnpack:    "unpack",
	KwFn:        "fn",
	KwCl:        "cl",
	KwImport:    "import",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

var keywords = map[string]Kind{
	"let":       KwLet,
	"bind":      KwBind,
	"define":    KwDefine,
	"struct":    KwStruct,
	"enum":      KwEnum,
	"interface": KwInterface,
	"if":        KwIf,
	"else":      KwElse,
	"for":       KwFor,
	"while":     KwWhile,
	"in":        KwIn,
	"match":     KwMatch,
	"when":      KwWhen,
	"func":      KwFunc,
	"none":      KwNone,
	"true":      KwTrue,
	"false":     KwFalse,
	"self":      KwSelf,
	"Self":      KwSelfType,
	"own":       KwOwn,
	"and":       KwAnd,
	"or":        KwOr,
	"not":       KwNot,
	"as":        KwAs,
	"unsafe":    KwUnsafe,
	"extern":    KwExtern,
	"unpack":    KwUnpack,
	"fn":        KwFn,
	"cl":        KwCl,
	"import":    KwImport,
}

// LookupKeyword maps an identifier string to a keyword Kind, or Ident if not a keyword.
func LookupKeyword(s string) Kind {
	if k, ok := keywords[s]; ok {
		return k
	}
	return Ident
}

// Token is a single lexed item with its source span and decoded literal/raw text.
type Token struct {
	Kind     Kind
	Text     string // decoded text for string/char segments, or raw source text otherwise
	Span     diag.Span
	AutoSemi bool // true if this Semi token was automatically synthesized from a newline
}

// TriggersAutoSemi reports whether a newline after a token of this Kind
// may insert an automatic semicolon under Spec §2.3 Rule 2.
func (k Kind) TriggersAutoSemi() bool {
	switch k {
	case Ident, OptIdent,
		IntLit, FloatLit, CharLit, StringLit, InterpEnd,
		RParen, RBracket, RBrace,
		KwTrue, KwFalse, KwNone, KwSelf, KwSelfType,
		Unwind:
		return true
	default:
		return false
	}
}

// SuppressesAutoSemiOnNextLine reports whether a token starting the next line
// suppresses an automatic semicolon from the previous line under Spec §2.3 Rule 3.
func (k Kind) SuppressesAutoSemiOnNextLine() bool {
	switch k {
	case KwElse, KwWhen, Dot, QuestDot:
		return true
	default:
		return false
	}
}
