// Package parser implements a recursive-descent and Pratt expression parser for Lowlight v1.0.
package parser

import (
	"strings"
	"unicode/utf8"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/token"
)

// Parser parses a slice of tokens into an ast.File.
type Parser struct {
	file        string
	toks        []token.Token
	pos         int
	rep         *diag.Reporter
	inMatchArm  int // > 0 when parsing the body of a `match` arm (for dangling-`else` disambiguation)
}

// ParseFile lexes and parses a Lowlight source string into an AST.
func ParseFile(filename, src string, rep *diag.Reporter) *ast.File {
	rep.AddSource(filename, src)
	toks := lexer.Lex(filename, src, rep)
	p := &Parser{
		file: filename,
		toks: toks,
		rep:  rep,
	}
	return p.parseFile()
}

func (p *Parser) cur() token.Token {
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos]
}

func (p *Parser) peekAt(delta int) token.Token {
	idx := p.pos + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[idx]
}

func (p *Parser) advance() token.Token {
	tk := p.cur()
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return tk
}

func (p *Parser) match(kinds ...token.Kind) (token.Token, bool) {
	c := p.cur()
	for _, k := range kinds {
		if c.Kind == k {
			return p.advance(), true
		}
	}
	return token.Token{}, false
}

func (p *Parser) expect(k token.Kind, context string) token.Token {
	if p.cur().Kind == k {
		return p.advance()
	}
	got := p.cur()
	p.rep.Errorf(got.Span, "expected %s %s, got %s (%q)", k.String(), context, got.Kind.String(), got.Text)
	return p.advance()
}

func (p *Parser) skipSemis() {
	for p.cur().Kind == token.Semi {
		p.advance()
	}
}

func (p *Parser) checkTrailingComma(openTok, closeTok token.Token, lastItemEnd diag.Pos, hadTrailingComma bool, what string) {
	if closeTok.Span.Start.Line > openTok.Span.Start.Line && closeTok.Span.Start.Line > lastItemEnd.Line && !hadTrailingComma {
		p.rep.ErrorWithHint(
			diag.Span{Start: lastItemEnd, End: lastItemEnd},
			"missing trailing comma in multiline "+what,
			"multiline [...] and (...) lists require a trailing comma on the final item",
		)
	}
}

// -----------------------------------------------------------------------------
// File & Top-Level Declarations
// -----------------------------------------------------------------------------

func (p *Parser) parseFile() *ast.File {
	start := p.cur().Span.Start
	var decls []ast.Decl
	p.skipSemis()
	for p.cur().Kind != token.EOF {
		d := p.parseTopDecl()
		if d != nil {
			decls = append(decls, d)
		}
		p.skipSemis()
	}
	end := p.cur().Span.End
	return &ast.File{
		Path:  p.file,
		Decls: decls,
		Span:  diag.Span{Start: start, End: end},
	}
}

func (p *Parser) parseTopDecl() ast.Decl {
	switch p.cur().Kind {
	case token.KwImport:
		return p.parseImportDecl()
	case token.KwExtern:
		return p.parseExternDecl()
	case token.KwDefine:
		return p.parseDefineDecl()
	case token.KwBind, token.KwLet:
		return p.parseTopVarOrMethodDecl()
	default:
		bad := p.advance()
		p.rep.Errorf(bad.Span, "expected top-level declaration (`import`, `extern`, `define`, `bind`, or `let`), got %s (%q)", bad.Kind.String(), bad.Text)
		p.syncToStmtBoundary()
		return nil
	}
}

func (p *Parser) syncToStmtBoundary() {
	for p.cur().Kind != token.EOF && p.cur().Kind != token.Semi && p.cur().Kind != token.RBrace {
		p.advance()
	}
}

func (p *Parser) parseImportDecl() *ast.ImportDecl {
	kw := p.expect(token.KwImport, "in import")
	pathTok := p.expect(token.StringLit, "after `import`")
	alias := ""
	end := pathTok.Span.End
	if _, ok := p.match(token.KwAs); ok {
		alTok := p.expect(token.Ident, "after `as` in import")
		alias = alTok.Text
		end = alTok.Span.End
	}
	return &ast.ImportDecl{
		Path:  pathTok.Text,
		Alias: alias,
		Span:  diag.Span{Start: kw.Span.Start, End: end},
	}
}

func (p *Parser) parseExternDecl() ast.Decl {
	kw := p.expect(token.KwExtern, "in extern")
	if p.cur().Kind == token.StringLit {
		hdr := p.advance()
		return &ast.ExternHeaderDecl{
			Header: hdr.Text,
			Span:   diag.Span{Start: kw.Span.Start, End: hdr.Span.End},
		}
	}
	nameTok := p.expect(token.Ident, "function name after `extern`")
	alias := ""
	if _, ok := p.match(token.KwAs); ok {
		alias = p.expect(token.Ident, "alias identifier after `as`").Text
	}
	p.expect(token.Colon, "after extern function name")
	params, endPos := p.parseParamList(true)
	var ret ast.TypeExpr
	if _, ok := p.match(token.Colon); ok {
		ret = p.parseTypeExpr()
		endPos = ret.GetSpan().End
	}
	return &ast.ExternFnDecl{
		Name:   nameTok.Text,
		Alias:  alias,
		Params: params,
		Ret:    ret,
		Span:   diag.Span{Start: kw.Span.Start, End: endPos},
	}
}

func (p *Parser) parseDefineDecl() ast.Decl {
	kw := p.expect(token.KwDefine, "in define")
	packed := false
	if p.cur().Kind == token.Ident && p.cur().Text == "packed" && p.peekAt(1).Kind == token.Ident {
		packed = true
		p.advance()
	}

	nameTok := p.expect(token.Ident, "type name after `define`")
	var tparams []ast.TypeParam
	if p.cur().Kind == token.LBracket && p.looksLikeTypeParamsInDefine() {
		tparams = p.parseTypeParams()
	}

	switch p.cur().Kind {
	case token.KwStruct:
		p.advance()
		openBr := p.expect(token.LBracket, "after `struct`")
		var fields []ast.StructField
		var lastEnd diag.Pos
		hadComma := false
		for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
			fStart := p.cur().Span.Start
			var fname string
			isOpt := false
			if p.cur().Kind == token.OptIdent {
				isOpt = true
				fname = p.advance().Text
			} else {
				fname = p.expect(token.Ident, "struct field name").Text
			}
			p.expect(token.Colon, "after struct field name")
			fty := p.parseTypeExpr()
			fEnd := fty.GetSpan().End
			var defExpr ast.Expr
			if _, ok := p.match(token.Assign); ok {
				defExpr = p.parseExpr()
				fEnd = defExpr.GetSpan().End
			}
			fields = append(fields, ast.StructField{
				Name:    fname,
				IsOpt:   isOpt,
				Type:    fty,
				Default: defExpr,
				Span:    diag.Span{Start: fStart, End: fEnd},
			})
			lastEnd = fEnd
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
		closeBr := p.expect(token.RBracket, "closing struct definition")
		if len(fields) > 0 {
			p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "struct definition")
		}
		return &ast.StructDecl{
			Name:       nameTok.Text,
			Packed:     packed,
			TypeParams: tparams,
			Fields:     fields,
			Span:       diag.Span{Start: kw.Span.Start, End: closeBr.Span.End},
		}

	case token.KwEnum:
		if packed {
			p.rep.Errorf(kw.Span, "`packed` modifier is only valid on `struct` definitions")
		}
		if len(tparams) > 0 {
			p.rep.Errorf(kw.Span, "enums cannot have generic type parameters (enums are pure tag types)")
		}
		p.advance()
		var baseTy ast.TypeExpr
		if p.cur().Kind == token.Ident {
			btTok := p.advance()
			baseTy = &ast.NamedType{Name: btTok.Text, Span: btTok.Span}
		}
		openBr := p.expect(token.LBracket, "opening enum variants `[`")
		var variants []ast.EnumVariant
		var lastEnd diag.Pos
		hadComma := false
		for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
			vTok := p.expect(token.Ident, "enum variant name")
			vEnd := vTok.Span.End
			var valExpr ast.Expr
			if _, ok := p.match(token.Assign); ok {
				valExpr = p.parseExpr()
				vEnd = valExpr.GetSpan().End
			}
			variants = append(variants, ast.EnumVariant{
				Name:  vTok.Text,
				Value: valExpr,
				Span:  diag.Span{Start: vTok.Span.Start, End: vEnd},
			})
			lastEnd = vEnd
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
		closeBr := p.expect(token.RBracket, "closing enum definition")
		if len(variants) > 0 {
			p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "enum definition")
		}
		return &ast.EnumDecl{
			Name:     nameTok.Text,
			BaseType: baseTy,
			Variants: variants,
			Span:     diag.Span{Start: kw.Span.Start, End: closeBr.Span.End},
		}

	case token.KwInterface:
		if packed || len(tparams) > 0 {
			p.rep.Errorf(kw.Span, "interfaces do not support `packed` or type parameters in v1.0")
		}
		p.advance()
		openBr := p.expect(token.LBracket, "after `interface`")
		var methods []ast.InterfaceMethod
		var lastEnd diag.Pos
		hadComma := false
		for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
			mTok := p.expect(token.Ident, "interface method name")
			p.expect(token.Colon, "after interface method name")
			recvMode, _, params, mEnd := p.parseMethodParams()
			var ret ast.TypeExpr
			if _, ok := p.match(token.Colon); ok {
				ret = p.parseTypeExpr()
				mEnd = ret.GetSpan().End
			}
			methods = append(methods, ast.InterfaceMethod{
				Name:     mTok.Text,
				RecvMode: recvMode,
				Params:   params,
				Ret:      ret,
				Span:     diag.Span{Start: mTok.Span.Start, End: mEnd},
			})
			lastEnd = mEnd
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
		closeBr := p.expect(token.RBracket, "closing interface definition")
		if len(methods) > 0 {
			p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "interface definition")
		}
		return &ast.InterfaceDecl{
			Name:    nameTok.Text,
			Methods: methods,
			Span:    diag.Span{Start: kw.Span.Start, End: closeBr.Span.End},
		}

	default:
		// Distinct derived type: `define UserID int`
		base := p.parseTypeExpr()
		return &ast.DistinctTypeDecl{
			Name: nameTok.Text,
			Base: base,
			Span: diag.Span{Start: kw.Span.Start, End: base.GetSpan().End},
		}
	}
}

func (p *Parser) looksLikeTypeParamsInDefine() bool {
	// `define Name[T, ...]` is followed after `]` by `struct`, `enum`, `interface`, or a base type.
	// Note that `define Alias [int]` (distinct type over vector `[int]`) would have `[int]` at the end of the line (`Semi`/`EOF`)!
	depth := 0
	i := p.pos
	for i < len(p.toks) {
		k := p.toks[i].Kind
		if k == token.LBracket {
			depth++
		} else if k == token.RBracket {
			depth--
			if depth == 0 {
				next := token.EOF
				if i+1 < len(p.toks) {
					next = p.toks[i+1].Kind
				}
				return next == token.KwStruct || next == token.KwEnum || next == token.KwInterface
			}
		} else if k == token.EOF {
			break
		}
		i++
	}
	return false
}

func (p *Parser) parseTypeParams() []ast.TypeParam {
	openBr := p.expect(token.LBracket, "opening type parameters `[`")
	var params []ast.TypeParam
	var lastEnd diag.Pos
	hadComma := false
	for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
		id := p.expect(token.Ident, "type parameter name")
		endPos := id.Span.End
		var bounds []string
		if _, ok := p.match(token.Colon); ok {
			b := p.expect(token.Ident, "interface bound name")
			bounds = append(bounds, b.Text)
			endPos = b.Span.End
			for p.cur().Kind == token.Plus {
				p.advance()
				b2 := p.expect(token.Ident, "interface bound name")
				bounds = append(bounds, b2.Text)
				endPos = b2.Span.End
			}
		}
		params = append(params, ast.TypeParam{
			Name:   id.Text,
			Bounds: bounds,
			Span:   diag.Span{Start: id.Span.Start, End: endPos},
		})
		lastEnd = endPos
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
		} else {
			hadComma = false
			break
		}
	}
	closeBr := p.expect(token.RBracket, "closing type parameters `]`")
	if len(params) > 0 {
		p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "type parameter list")
	}
	return params
}

func (p *Parser) parseTopVarOrMethodDecl() ast.Decl {
	kw := p.advance() // KwBind or KwLet
	isBind := kw.Kind == token.KwBind

	// Check for namespaced method: `bind TypeName.method = ...` or `bind TypeName[A, B].method[C] = ...`
	if p.cur().Kind == token.Ident && (p.peekAt(1).Kind == token.Dot || (p.peekAt(1).Kind == token.LBracket && p.bracketFollowedByDot())) {
		if !isBind {
			p.rep.ErrorWithHint(
				kw.Span,
				"methods must be declared with `bind`, not `let`",
				"write `bind Type.method = ...` (use function-typed struct fields for runtime-swappable per-instance behavior)",
			)
		}
		recvTok := p.advance()
		var recvTParams []string
		if p.cur().Kind == token.LBracket {
			tp := p.parseTypeParams()
			for _, t := range tp {
				recvTParams = append(recvTParams, t.Name)
			}
		}
		p.expect(token.Dot, "in method declaration")
		mNameTok := p.expect(token.Ident, "method name after `.`")
		var methodTParams []ast.TypeParam
		if p.cur().Kind == token.LBracket {
			methodTParams = p.parseTypeParams()
		}
		p.expect(token.Assign, "after method name")

		// Note: associated functions WITHOUT `self` (like `bind Arena.new = (): Arena` or `bind Pool[T].new = (): Pool[T]`)
		// are also super idiomatic! Let's check if the parameter list starts with `self` / `&self` / `own self`!
		if p.paramListStartsWithSelf() {
			recvMode, recvExplicitTy, params, _ := p.parseMethodParams()
			var ret ast.TypeExpr
			if _, ok := p.match(token.Colon); ok {
				ret = p.parseTypeExpr()
			}
			body := p.parseExpr()
			return &ast.MethodDecl{
				RecvType:       recvTok.Text,
				RecvTypeParams: recvTParams,
				Name:           mNameTok.Text,
				TypeParams:     methodTParams,
				RecvMode:       recvMode,
				RecvExplicitTy: recvExplicitTy,
				Params:         params,
				Ret:            ret,
				Body:           body,
				Span:           diag.Span{Start: kw.Span.Start, End: body.GetSpan().End},
			}
		}

		// Associated static function on a type (`bind Arena.new = (): Arena { ... }` or `bind Pool[T].new = ...`):
		// Represented with RecvExplicitTy == nil and Name, or let's add IsStatic to MethodDecl?
		// Even simpler: let's parse `(params)[: Ret] body` as a FuncLit and store a static MethodDecl (RecvMode = -1)!
		params, _ := p.parseParamList(true)
		var ret ast.TypeExpr
		if _, ok := p.match(token.Colon); ok {
			ret = p.parseTypeExpr()
		}
		body := p.parseExpr()
		return &ast.MethodDecl{
			RecvType:       recvTok.Text,
			RecvTypeParams: recvTParams,
			Name:           mNameTok.Text,
			TypeParams:     methodTParams,
			RecvMode:       ast.ReceiverMode(-1), // associated static function (`Type.fn(...)`)
			Params:         params,
			Ret:            ret,
			Body:           body,
			Span:           diag.Span{Start: kw.Span.Start, End: body.GetSpan().End},
		}
	}

	// Regular or generic top-level `bind` / `let` declaration
	var name string
	isOpt := false
	if p.cur().Kind == token.OptIdent {
		isOpt = true
		name = p.advance().Text
	} else {
		name = p.expect(token.Ident, "variable or function name").Text
	}
	var tparams []ast.TypeParam
	if p.cur().Kind == token.LBracket {
		tparams = p.parseTypeParams()
	}
	var ty ast.TypeExpr
	if _, ok := p.match(token.Colon); ok {
		ty = p.parseTypeExpr()
	}
	p.expect(token.Assign, "in variable/function declaration")
	val := p.parseExpr()
	return &ast.VarDecl{
		IsBind:     isBind,
		Name:       name,
		IsOptName:  isOpt,
		TypeParams: tparams,
		Type:       ty,
		Value:      val,
		Span:       diag.Span{Start: kw.Span.Start, End: val.GetSpan().End},
	}
}

func (p *Parser) bracketFollowedByDot() bool {
	depth := 0
	for i := p.pos + 1; i < len(p.toks); i++ {
		k := p.toks[i].Kind
		if k == token.LBracket {
			depth++
		} else if k == token.RBracket {
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == token.Dot
			}
		} else if k == token.EOF {
			return false
		}
	}
	return false
}

func (p *Parser) paramListStartsWithSelf() bool {
	if p.cur().Kind != token.LParen {
		return false
	}
	k1 := p.peekAt(1).Kind
	k2 := p.peekAt(2).Kind
	if k1 == token.KwSelf {
		return true
	}
	if (k1 == token.Amp || k1 == token.KwOwn) && k2 == token.KwSelf {
		return true
	}
	return false
}

// parseMethodParams parses `(self | &self | own self | self: T | self: &T | self: own T, rest...)`.
func (p *Parser) parseMethodParams() (ast.ReceiverMode, ast.TypeExpr, []ast.Param, diag.Pos) {
	openP := p.expect(token.LParen, "opening method parameters `(`")
	mode := ast.RecvRead
	var explicitTy ast.TypeExpr
	var lastEnd diag.Pos
	hadComma := false

	switch p.cur().Kind {
	case token.Amp:
		p.advance()
		selfTok := p.expect(token.KwSelf, "after `&` in receiver")
		mode = ast.RecvMut
		lastEnd = selfTok.Span.End
	case token.KwOwn:
		p.advance()
		selfTok := p.expect(token.KwSelf, "after `own` in receiver")
		mode = ast.RecvOwn
		lastEnd = selfTok.Span.End
	case token.KwSelf:
		selfTok := p.advance()
		lastEnd = selfTok.Span.End
		if _, ok := p.match(token.Colon); ok {
			ty := p.parseTypeExpr()
			explicitTy = ty
			lastEnd = ty.GetSpan().End
			if mb, ok := ty.(*ast.MutBorrowType); ok {
				mode = ast.RecvMut
				explicitTy = mb.Elem
			} else if ow, ok := ty.(*ast.OwnType); ok {
				mode = ast.RecvOwn
				explicitTy = ow.Elem
			}
		}
	default:
		p.rep.Errorf(p.cur().Span, "interface and method definitions must declare `self`, `&self`, or `own self` as first parameter")
	}

	var params []ast.Param
	if _, ok := p.match(token.Comma); ok {
		hadComma = true
		for p.cur().Kind != token.RParen && p.cur().Kind != token.EOF {
			pr := p.parseSingleParam(true)
			params = append(params, pr)
			lastEnd = pr.Span.End
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
	}

	closeP := p.expect(token.RParen, "closing method parameters `)`")
	p.checkTrailingComma(openP, closeP, lastEnd, hadComma, "parameter list")
	return mode, explicitTy, params, closeP.Span.End
}

func (p *Parser) parseParamList(requireNames bool) ([]ast.Param, diag.Pos) {
	openP := p.expect(token.LParen, "opening parameter list `(`")
	var params []ast.Param
	var lastEnd diag.Pos
	hadComma := false
	for p.cur().Kind != token.RParen && p.cur().Kind != token.EOF {
		pr := p.parseSingleParam(requireNames)
		params = append(params, pr)
		lastEnd = pr.Span.End
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
		} else {
			hadComma = false
			break
		}
	}
	closeP := p.expect(token.RParen, "closing parameter list `)`")
	if len(params) > 0 {
		p.checkTrailingComma(openP, closeP, lastEnd, hadComma, "parameter list")
	}
	return params, closeP.Span.End
}

func (p *Parser) parseSingleParam(requireName bool) ast.Param {
	start := p.cur().Span.Start
	if requireName || ((p.cur().Kind == token.Ident || p.cur().Kind == token.OptIdent) && p.peekAt(1).Kind == token.Colon) {
		isOpt := false
		var name string
		if p.cur().Kind == token.OptIdent {
			isOpt = true
			name = p.advance().Text
		} else {
			name = p.expect(token.Ident, "parameter name").Text
		}
		p.expect(token.Colon, "after parameter name")
		ty := p.parseTypeExpr()
		return ast.Param{
			Name:      name,
			IsOptName: isOpt,
			Type:      ty,
			Span:      diag.Span{Start: start, End: ty.GetSpan().End},
		}
	}
	ty := p.parseTypeExpr()
	return ast.Param{
		Type: ty,
		Span: ty.GetSpan(),
	}
}

// -----------------------------------------------------------------------------
// Type Expressions
// -----------------------------------------------------------------------------

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	start := p.cur().Span.Start
	switch p.cur().Kind {
	case token.Question:
		p.advance()
		elem := p.parseTypeExpr()
		return &ast.OptionalType{
			Elem: elem,
			Span: diag.Span{Start: start, End: elem.GetSpan().End},
		}
	case token.Amp:
		p.advance()
		elem := p.parseTypeExpr()
		return &ast.MutBorrowType{
			Elem: elem,
			Span: diag.Span{Start: start, End: elem.GetSpan().End},
		}
	case token.KwOwn:
		p.advance()
		elem := p.parseTypeExpr()
		return &ast.OwnType{
			Elem: elem,
			Span: diag.Span{Start: start, End: elem.GetSpan().End},
		}
	case token.LBracket:
		openBr := p.advance()
		// Could be `[T]` (VectorType) or `[N: T]` (StackArrayType)!
		// Notice in `[N: T]`, `N` is an integer literal or constant identifier followed by `:`!
		if (p.cur().Kind == token.IntLit || p.cur().Kind == token.Ident) && p.peekAt(1).Kind == token.Colon {
			sizeExpr := p.parsePrimaryExpr()
			p.expect(token.Colon, "in stack array type `[N: T]`")
			elem := p.parseTypeExpr()
			closeBr := p.expect(token.RBracket, "closing `[N: T]`")
			return &ast.StackArrayType{
				Size: sizeExpr,
				Elem: elem,
				Span: diag.Span{Start: openBr.Span.Start, End: closeBr.Span.End},
			}
		}
		elem := p.parseTypeExpr()
		closeBr := p.expect(token.RBracket, "closing `[T]`")
		return &ast.VectorType{
			Elem: elem,
			Span: diag.Span{Start: openBr.Span.Start, End: closeBr.Span.End},
		}
	case token.HashBrack:
		openHb := p.advance()
		first := p.parseTypeExpr()
		if _, ok := p.match(token.Colon); ok {
			val := p.parseTypeExpr()
			closeBr := p.expect(token.RBracket, "closing map type `#[K: V]`")
			return &ast.MapType{
				Key:   first,
				Value: val,
				Span:  diag.Span{Start: openHb.Span.Start, End: closeBr.Span.End},
			}
		}
		closeBr := p.expect(token.RBracket, "closing set type `#[T]`")
		return &ast.SetType{
			Elem: first,
			Span: diag.Span{Start: openHb.Span.Start, End: closeBr.Span.End},
		}
	case token.LParen:
		params, endPos := p.parseParamList(false)
		var ret ast.TypeExpr
		if _, ok := p.match(token.Colon); ok {
			ret = p.parseTypeExpr()
			endPos = ret.GetSpan().End
		}
		return &ast.FuncType{
			Params: params,
			Ret:    ret,
			Span:   diag.Span{Start: start, End: endPos},
		}
	case token.KwSelfType:
		tk := p.advance()
		return &ast.NamedType{
			Name: "Self",
			Span: tk.Span,
		}
	case token.Ident:
		id1 := p.advance()
		mod := ""
		name := id1.Text
		endPos := id1.Span.End
		if p.cur().Kind == token.Dot && p.peekAt(1).Kind == token.Ident {
			p.advance()
			id2 := p.advance()
			mod = name
			name = id2.Text
			endPos = id2.Span.End
		}
		var targs []ast.TypeExpr
		if p.cur().Kind == token.LBracket {
			openBr := p.advance()
			var lastEnd diag.Pos
			hadComma := false
			for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
				ta := p.parseTypeExpr()
				targs = append(targs, ta)
				lastEnd = ta.GetSpan().End
				if _, ok := p.match(token.Comma); ok {
					hadComma = true
				} else {
					hadComma = false
					break
				}
			}
			closeBr := p.expect(token.RBracket, "closing generic type arguments `]`")
			if len(targs) > 0 {
				p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "type argument list")
			}
			endPos = closeBr.Span.End
		}
		return &ast.NamedType{
			Module:   mod,
			Name:     name,
			TypeArgs: targs,
			Span:     diag.Span{Start: start, End: endPos},
		}
	default:
		bad := p.advance()
		p.rep.Errorf(bad.Span, "expected type, got %s (%q)", bad.Kind.String(), bad.Text)
		return &ast.NamedType{Name: "<error>", Span: bad.Span}
	}
}

// -----------------------------------------------------------------------------
// Blocks & Statements
// -----------------------------------------------------------------------------

func (p *Parser) parseBlockExpr(label string, isUnsafe bool, startPos diag.Pos) *ast.BlockExpr {
	openBrace := p.expect(token.LBrace, "opening block `{`")
	if startPos.Line == 0 {
		startPos = openBrace.Span.Start
	}

	var stmts []ast.Stmt
	var tail ast.Expr

	p.skipSemis()
	for p.cur().Kind != token.RBrace && p.cur().Kind != token.EOF {
		st := p.parseStmt()
		// Determine whether this statement is a trailing value expression!
		// It is a trailing value expression iff:
		// 1. It is an *ast.ExprStmt
		// 2. There is NO explicit semicolon after it (only AutoSemi or direct `}`), AND
		// 3. After skipping AutoSemi, the next token is `}`!
		if exprSt, ok := st.(*ast.ExprStmt); ok {
			explicitSemi := false
			for p.cur().Kind == token.Semi {
				if !p.cur().AutoSemi {
					explicitSemi = true
				}
				p.advance()
			}
			if !explicitSemi && p.cur().Kind == token.RBrace {
				tail = exprSt.Expr
				break
			}
			stmts = append(stmts, st)
			continue
		}
		if st != nil {
			stmts = append(stmts, st)
		}
		p.skipSemis()
	}

	closeBrace := p.expect(token.RBrace, "closing block `}`")
	return &ast.BlockExpr{
		Label:  label,
		Unsafe: isUnsafe,
		Stmts:  stmts,
		Tail:   tail,
		Span:   diag.Span{Start: startPos, End: closeBrace.Span.End},
	}
}

func (p *Parser) isUnwindStart() bool {
	c := p.cur().Kind
	n1 := p.peekAt(1).Kind
	n2 := p.peekAt(2).Kind
	if c == token.Unwind {
		return true
	}
	if (c == token.KwIf || c == token.KwMatch || c == token.KwFor || c == token.KwNext || c == token.KwFunc || c == token.Ident) && n1 == token.Unwind {
		return true
	}
	if c == token.KwNext && n1 == token.Ident && n2 == token.Unwind {
		return true
	}
	return false
}

func (p *Parser) parseUnwindStmt() *ast.UnwindStmt {
	start := p.cur().Span.Start
	target := ast.UnwindBlock
	label := ""

	switch p.cur().Kind {
	case token.Unwind:
		p.advance()
		target = ast.UnwindBlock
	case token.KwIf:
		p.advance()
		p.expect(token.Unwind, "after `if`")
		target = ast.UnwindIf
	case token.KwMatch:
		p.advance()
		p.expect(token.Unwind, "after `match`")
		target = ast.UnwindMatch
	case token.KwFor:
		p.advance()
		p.expect(token.Unwind, "after `for`")
		target = ast.UnwindFor
	case token.KwFunc:
		p.advance()
		p.expect(token.Unwind, "after `func`")
		target = ast.UnwindFunc
	case token.KwNext:
		p.advance()
		if p.cur().Kind == token.Ident {
			label = p.advance().Text
			p.expect(token.Unwind, "after `next <label>`")
			target = ast.UnwindNextLabel
		} else {
			p.expect(token.Unwind, "after `next`")
			target = ast.UnwindNext
		}
	case token.Ident:
		label = p.advance().Text
		p.expect(token.Unwind, "after label")
		target = ast.UnwindLabel
	}

	endPos := p.peekAt(-1).Span.End
	var val ast.Expr
	if !p.isStmtEnd() {
		val = p.parseExpr()
		endPos = val.GetSpan().End
	}
	return &ast.UnwindStmt{
		Target: target,
		Label:  label,
		Value:  val,
		Span:   diag.Span{Start: start, End: endPos},
	}
}

func (p *Parser) isStmtEnd() bool {
	switch p.cur().Kind {
	case token.Semi, token.RBrace, token.RParen, token.RBracket, token.Comma, token.EOF, token.KwElse, token.KwWhen:
		return true
	default:
		return false
	}
}

func (p *Parser) parseStmt() ast.Stmt {
	if p.isUnwindStart() {
		return p.parseUnwindStmt()
	}

	switch p.cur().Kind {
	case token.KwLet, token.KwBind:
		return p.parseVarStmt()
	case token.KwDefine:
		d := p.parseDefineDecl()
		return &ast.DeclStmt{Decl: d, Span: d.GetSpan()}
	default:
		expr := p.parseExpr()
		if isAssignOp(p.cur().Kind) {
			op := p.advance().Kind
			// Support `target = opt_expr ?? <UnwindStmt>` (Spec §7.2.4 / §10.4)
			val, fbUnwind := p.parseExprWithOptionalTopFallbackUnwind(op == token.Assign)
			endPos := val.GetSpan().End
			if fbUnwind != nil {
				endPos = fbUnwind.Span.End
			}
			return &ast.AssignStmt{
				Op:             op,
				Target:         expr,
				Value:          val,
				FallbackUnwind: fbUnwind,
				Span:           diag.Span{Start: expr.GetSpan().Start, End: endPos},
			}
		}
		return &ast.ExprStmt{
			Expr: expr,
			Span: expr.GetSpan(),
		}
	}
}

func isAssignOp(k token.Kind) bool {
	switch k {
	case token.Assign,
		token.PlusEq, token.MinusEq, token.StarEq, token.SlashEq, token.PercentEq,
		token.AmpEq, token.PipeEq, token.CaretEq, token.ShlEq, token.ShrEq:
		return true
	default:
		return false
	}
}

func (p *Parser) parseVarStmt() *ast.VarStmt {
	kw := p.advance()
	isBind := kw.Kind == token.KwBind
	isOpt := false
	var name string
	if p.cur().Kind == token.OptIdent {
		isOpt = true
		name = p.advance().Text
	} else {
		name = p.expect(token.Ident, "variable name").Text
	}
	var ty ast.TypeExpr
	if _, ok := p.match(token.Colon); ok {
		ty = p.parseTypeExpr()
	}
	p.expect(token.Assign, "after variable name in declaration")
	val, fbUnwind := p.parseExprWithOptionalTopFallbackUnwind(true)
	endPos := val.GetSpan().End
	if fbUnwind != nil {
		endPos = fbUnwind.Span.End
	}
	return &ast.VarStmt{
		IsBind:         isBind,
		Name:           name,
		IsOptName:      isOpt,
		Type:           ty,
		Value:          val,
		FallbackUnwind: fbUnwind,
		Span:           diag.Span{Start: kw.Span.Start, End: endPos},
	}
}

// parseExprWithOptionalTopFallbackUnwind parses `expr` and, if `allowUnwind` is true and
// the top-level `??` is followed by an unwind statement (`b? ?? func <- 0`), parses it into `*ast.UnwindStmt`.
func (p *Parser) parseExprWithOptionalTopFallbackUnwind(allowUnwind bool) (ast.Expr, *ast.UnwindStmt) {
	if !allowUnwind {
		return p.parseExpr(), nil
	}
	// Parse everything with precedence strictly higher than `??` (prec >= 2),
	// then handle `??` chains so the final `??` can check `isUnwindStart()`.
	left := p.parseBinaryExpr(2)
	for p.cur().Kind == token.QuestQuest {
		p.advance() // consume `??`
		if p.isUnwindStart() {
			uw := p.parseUnwindStmt()
			return left, uw
		}
		right := p.parseBinaryExpr(2)
		left = &ast.BinaryExpr{
			Op:    token.QuestQuest,
			Left:  left,
			Right: right,
			Span:  diag.Span{Start: left.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return left, nil
}

// parseActionBody parses the body of an `if` branch, `for` loop, or `match` arm,
// which may be a `{ ... }` block, any expression, OR a single braceless statement (`next <-`, `for <- val`, `x += 1`).
func (p *Parser) parseActionBody() ast.Expr {
	if p.isUnwindStart() {
		uw := p.parseUnwindStmt()
		return &ast.StmtBodyExpr{Stmt: uw, Span: uw.Span}
	}
	if p.cur().Kind == token.KwLet || p.cur().Kind == token.KwBind {
		vs := p.parseVarStmt()
		return &ast.StmtBodyExpr{Stmt: vs, Span: vs.Span}
	}
	expr := p.parseExpr()
	if isAssignOp(p.cur().Kind) {
		op := p.advance().Kind
		val, fb := p.parseExprWithOptionalTopFallbackUnwind(op == token.Assign)
		endPos := val.GetSpan().End
		if fb != nil {
			endPos = fb.Span.End
		}
		as := &ast.AssignStmt{
			Op:             op,
			Target:         expr,
			Value:          val,
			FallbackUnwind: fb,
			Span:           diag.Span{Start: expr.GetSpan().Start, End: endPos},
		}
		return &ast.StmtBodyExpr{Stmt: as, Span: as.Span}
	}
	return expr
}

// -----------------------------------------------------------------------------
// Pratt Expression Parser
// -----------------------------------------------------------------------------

func (p *Parser) parseExpr() ast.Expr {
	return p.parseBinaryExpr(1)
}

func binaryPrec(k token.Kind) (prec int, rightAssoc bool) {
	switch k {
	case token.QuestQuest:
		return 1, true
	case token.KwOr:
		return 2, false
	case token.KwAnd:
		return 3, false
	case token.Eq, token.Neq, token.Lt, token.Lte, token.Gt, token.Gte:
		return 4, false
	case token.Pipe:
		return 5, false
	case token.Caret:
		return 6, false
	case token.Amp:
		return 7, false
	case token.Shl, token.Shr:
		return 8, false
	case token.Plus, token.Minus:
		return 9, false
	case token.Star, token.Slash, token.Percent:
		return 10, false
	default:
		return 0, false
	}
}

func (p *Parser) parseBinaryExpr(minPrec int) ast.Expr {
	left := p.parseUnaryExpr()
	for {
		op := p.cur().Kind
		prec, rightAssoc := binaryPrec(op)
		if prec < minPrec {
			break
		}
		opTok := p.advance()
		if op == token.QuestQuest && p.isUnwindStart() {
			p.rep.ErrorWithHint(
				opTok.Span,
				"unwind statement after `??` is only allowed at the top level of a `let`, `bind`, or `=` statement",
				"extract the `?? <target> <- ...` into a `let` or `bind` statement",
			)
			uw := p.parseUnwindStmt()
			right := &ast.StmtBodyExpr{Stmt: uw, Span: uw.Span}
			left = &ast.BinaryExpr{
				Op:    op,
				Left:  left,
				Right: right,
				Span:  diag.Span{Start: left.GetSpan().Start, End: right.GetSpan().End},
			}
			continue
		}
		nextMin := prec + 1
		if rightAssoc {
			nextMin = prec
		}
		right := p.parseBinaryExpr(nextMin)
		left = &ast.BinaryExpr{
			Op:    op,
			Left:  left,
			Right: right,
			Span:  diag.Span{Start: left.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return left
}

func (p *Parser) parseUnaryExpr() ast.Expr {
	start := p.cur().Span.Start
	switch p.cur().Kind {
	case token.Minus, token.KwNot, token.Tilde, token.Amp, token.KwOwn, token.Star:
		op := p.advance().Kind
		operand := p.parseUnaryExpr()
		return &ast.UnaryExpr{
			Op:      op,
			Operand: operand,
			Span:    diag.Span{Start: start, End: operand.GetSpan().End},
		}
	default:
		if p.isUnwindStart() {
			uw := p.parseUnwindStmt()
			p.rep.Errorf(uw.Span, "`<-` unwind is a statement and cannot be used inside a sub-expression or argument list")
			return &ast.StmtBodyExpr{Stmt: uw, Span: uw.Span}
		}
		return p.parsePostfixExpr()
	}
}

func (p *Parser) parsePostfixExpr() ast.Expr {
	left := p.parsePrimaryExpr()
	for {
		switch p.cur().Kind {
		case token.Dot, token.QuestDot:
			opTok := p.advance()
			isQuestDot := opTok.Kind == token.QuestDot
			var member string
			hasOpt := false
			if p.cur().Kind == token.OptIdent {
				hasOpt = true
				member = p.advance().Text
			} else {
				member = p.expect(token.Ident, "field or method name after `.`").Text
			}
			left = &ast.MemberExpr{
				Base:         left,
				Member:       member,
				MemberHasOpt: hasOpt,
				QuestDot:     isQuestDot,
				Span:         diag.Span{Start: left.GetSpan().Start, End: p.peekAt(-1).Span.End},
			}

		case token.LParen:
			args, endPos := p.parseArgList()
			left = &ast.CallExpr{
				Callee: left,
				Args:   args,
				Span:   diag.Span{Start: left.GetSpan().Start, End: endPos},
			}

		case token.LBracket:
			// Check 1: Struct literal `TypeName[field: expr, ...]` or `TypeName[]`
			if mod, tname, ok := extractStructLitTarget(left); ok && p.looksLikeStructLitFields() {
				fields, endPos := p.parseStructLitFields()
				left = &ast.StructLit{
					Module:   mod,
					TypeName: tname,
					Fields:   fields,
					Span:     diag.Span{Start: left.GetSpan().Start, End: endPos},
				}
				continue
			}

			// Check 2: Generic type arguments `foo[T1, T2](...)`, `Pair[int, string][first: 1, ...]`, or `Pool[Node].new()`
			if p.looksLikeGenericTypeArgs() {
				targs := p.parseTypeArgBracket()
				if p.cur().Kind == token.LBracket {
					if mod, tname, ok := extractStructLitTarget(left); ok && p.looksLikeStructLitFields() {
						fields, endPos := p.parseStructLitFields()
						left = &ast.StructLit{
							Module:   mod,
							TypeName: tname,
							TypeArgs: targs,
							Fields:   fields,
							Span:     diag.Span{Start: left.GetSpan().Start, End: endPos},
						}
						continue
					}
				}
				if p.cur().Kind == token.LParen {
					args, endPos := p.parseArgList()
					left = &ast.CallExpr{
						Callee:   left,
						TypeArgs: targs,
						Args:     args,
						Span:     diag.Span{Start: left.GetSpan().Start, End: endPos},
					}
					continue
				}
				if p.cur().Kind == token.Dot {
					// e.g. `Pool[Node].new()` — encode generic base as a synthetic StructLit/Call or Member on generic Ident!
					p.advance()
					mTok := p.expect(token.Ident, "method name after generic type `.`")
					// Represent `Pool[Node].new(...)` cleanly: if followed by `(`, Callee is MemberExpr with TypeArgs on CallExpr!
					mem := &ast.MemberExpr{
						Base:   left,
						Member: mTok.Text,
						Span:   diag.Span{Start: left.GetSpan().Start, End: mTok.Span.End},
					}
					args, endPos := p.parseArgList()
					left = &ast.CallExpr{
						Callee:   mem,
						TypeArgs: targs,
						Args:     args,
						Span:     diag.Span{Start: left.GetSpan().Start, End: endPos},
					}
					continue
				}
			}

			// Check 3: Indexing `base[idx]` or Slicing `base[lo..hi]` / `base[lo..=hi]`
			p.advance() // consume `[`
			first := p.parseExpr()
			if p.cur().Kind == token.DotDot || p.cur().Kind == token.DotDotEq {
				incl := p.advance().Kind == token.DotDotEq
				high := p.parseExpr()
				closeBr := p.expect(token.RBracket, "closing slice `]`")
				left = &ast.SliceExpr{
					Base:      left,
					Low:       first,
					High:      high,
					Inclusive: incl,
					Span:      diag.Span{Start: left.GetSpan().Start, End: closeBr.Span.End},
				}
			} else {
				closeBr := p.expect(token.RBracket, "closing index `]`")
				left = &ast.IndexExpr{
					Base:  left,
					Index: first,
					Span:  diag.Span{Start: left.GetSpan().Start, End: closeBr.Span.End},
				}
			}

		default:
			return left
		}
	}
}

func extractStructLitTarget(e ast.Expr) (mod, name string, ok bool) {
	switch v := e.(type) {
	case *ast.IdentExpr:
		if !v.IsOpt {
			return "", v.Name, true
		}
	case *ast.MemberExpr:
		if id, ok2 := v.Base.(*ast.IdentExpr); ok2 && !id.IsOpt && !v.QuestDot && !v.MemberHasOpt {
			return id.Name, v.Member, true
		}
	}
	return "", "", false
}

func (p *Parser) looksLikeStructLitFields() bool {
	if p.cur().Kind != token.LBracket {
		return false
	}
	k1 := p.peekAt(1).Kind
	k2 := p.peekAt(2).Kind
	if k1 == token.RBracket {
		return true
	}
	return (k1 == token.Ident || k1 == token.OptIdent) && k2 == token.Colon
}

func (p *Parser) parseStructLitFields() ([]ast.StructFieldInit, diag.Pos) {
	openBr := p.expect(token.LBracket, "opening struct literal `[`")
	var fields []ast.StructFieldInit
	var lastEnd diag.Pos
	hadComma := false
	for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
		fStart := p.cur().Span.Start
		isOpt := false
		var fname string
		if p.cur().Kind == token.OptIdent {
			isOpt = true
			fname = p.advance().Text
		} else {
			fname = p.expect(token.Ident, "field name in struct literal").Text
		}
		p.expect(token.Colon, "after field name in struct literal")
		val := p.parseExpr()
		fields = append(fields, ast.StructFieldInit{
			Name:  fname,
			IsOpt: isOpt,
			Value: val,
			Span:  diag.Span{Start: fStart, End: val.GetSpan().End},
		})
		lastEnd = val.GetSpan().End
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
		} else {
			hadComma = false
			break
		}
	}
	closeBr := p.expect(token.RBracket, "closing struct literal `]`")
	if len(fields) > 0 {
		p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "struct literal")
	}
	return fields, closeBr.Span.End
}

// looksLikeGenericTypeArgs speculatively checks whether `[...]` parses cleanly as non-empty TypeExpr(s)
// followed immediately by `(`, `[` (struct lit), or `.` (`Pool[Node].new()`).
func (p *Parser) looksLikeGenericTypeArgs() bool {
	if p.cur().Kind != token.LBracket {
		return false
	}
	return p.scanGenericBracketLookahead(p.pos)
}

func (p *Parser) scanGenericBracketLookahead(startIdx int) bool {
	// Save real reporter and swap in a throwaway reporter so speculative parsing produces zero diagnostics.
	// Wait: `looksLikeGenericTypeArgs` already swapped `p.rep`; let's write this directly without touching `p.rep`!
	savePos := p.pos
	realRep := p.rep
	tempRep := diag.NewReporter()
	p.rep = tempRep
	defer func() {
		p.pos = savePos
		p.rep = realRep
	}()

	p.pos = startIdx
	p.advance() // `[`
	if p.cur().Kind == token.RBracket {
		return false
	}
	count := 0
	for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
		_ = p.parseTypeExpr()
		if tempRep.HasErrors() {
			return false
		}
		count++
		if p.cur().Kind == token.Comma {
			p.advance()
		} else {
			break
		}
	}
	if count == 0 || p.cur().Kind != token.RBracket {
		return false
	}
	p.advance() // `]`
	next := p.cur().Kind
	if next == token.LParen {
		return true
	}
	if next == token.LBracket && p.looksLikeStructLitFields() {
		return true
	}
	if next == token.Dot && p.peekAt(1).Kind == token.Ident && p.peekAt(2).Kind == token.LParen {
		return true
	}
	return false
}

func (p *Parser) parseTypeArgBracket() []ast.TypeExpr {
	openBr := p.expect(token.LBracket, "opening type arguments `[`")
	var targs []ast.TypeExpr
	var lastEnd diag.Pos
	hadComma := false
	for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
		ta := p.parseTypeExpr()
		targs = append(targs, ta)
		lastEnd = ta.GetSpan().End
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
		} else {
			hadComma = false
			break
		}
	}
	closeBr := p.expect(token.RBracket, "closing type arguments `]`")
	if len(targs) > 0 {
		p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "type argument list")
	}
	return targs
}

func (p *Parser) parseArgList() ([]ast.Expr, diag.Pos) {
	openP := p.expect(token.LParen, "opening argument list `(`")
	var args []ast.Expr
	var lastEnd diag.Pos
	hadComma := false
	for p.cur().Kind != token.RParen && p.cur().Kind != token.EOF {
		arg := p.parseExpr()
		args = append(args, arg)
		lastEnd = arg.GetSpan().End
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
		} else {
			hadComma = false
			break
		}
	}
	closeP := p.expect(token.RParen, "closing argument list `)`")
	if len(args) > 0 {
		p.checkTrailingComma(openP, closeP, lastEnd, hadComma, "argument list")
	}
	return args, closeP.Span.End
}

// -----------------------------------------------------------------------------
// Primary Expressions
// -----------------------------------------------------------------------------

func (p *Parser) parsePrimaryExpr() ast.Expr {
	tk := p.cur()
	switch tk.Kind {
	case token.IntLit:
		p.advance()
		val, err := lexer.ParseIntLiteral(tk.Text)
		if err != nil {
			p.rep.Errorf(tk.Span, "%v", err)
		}
		return &ast.IntLit{Raw: tk.Text, Val: val, Span: tk.Span}

	case token.FloatLit:
		p.advance()
		val, err := lexer.ParseFloatLiteral(tk.Text)
		if err != nil {
			p.rep.Errorf(tk.Span, "%v", err)
		}
		return &ast.FloatLit{Raw: tk.Text, Val: val, Span: tk.Span}

	case token.KwTrue:
		p.advance()
		return &ast.BoolLit{Val: true, Span: tk.Span}

	case token.KwFalse:
		p.advance()
		return &ast.BoolLit{Val: false, Span: tk.Span}

	case token.KwNone:
		p.advance()
		return &ast.NoneLit{Span: tk.Span}

	case token.CharLit:
		p.advance()
		r, _ := utf8.DecodeRuneInString(tk.Text)
		return &ast.CharLit{Val: r, Span: tk.Span}

	case token.StringLit:
		p.advance()
		return &ast.StringLit{Val: tk.Text, Span: tk.Span}

	case token.InterpStart:
		return p.parseInterpString()

	case token.KwSelf:
		p.advance()
		return &ast.IdentExpr{Name: "self", Span: tk.Span}

	case token.OptIdent:
		p.advance()
		return &ast.IdentExpr{Name: tk.Text, IsOpt: true, Span: tk.Span}

	case token.Ident:
		// Check for labeled loop or labeled block: `outer: for (...)` or `blk: { ... }`
		if p.peekAt(1).Kind == token.Colon && (p.peekAt(2).Kind == token.KwFor || p.peekAt(2).Kind == token.LBrace) {
			lblTok := p.advance()
			p.advance() // `:`
			if p.cur().Kind == token.KwFor {
				return p.parseForExpr(lblTok.Text, lblTok.Span.Start)
			}
			return p.parseBlockExpr(lblTok.Text, false, lblTok.Span.Start)
		}
		p.advance()
		return &ast.IdentExpr{Name: tk.Text, Span: tk.Span}

	case token.LParen:
		if p.looksLikeFuncLit() {
			return p.parseFuncLit()
		}
		p.advance()
		inner := p.parseExpr()
		p.expect(token.RParen, "closing grouped expression `)`")
		return inner

	case token.LBracket:
		return p.parseVecOrRepeatArrayLit()

	case token.HashBrack:
		return p.parseHashLit()

	case token.LBrace:
		return p.parseBlockExpr("", false, tk.Span.Start)

	case token.KwUnsafe:
		kw := p.advance()
		return p.parseBlockExpr("", true, kw.Span.Start)

	case token.KwIf:
		return p.parseIfExpr()

	case token.KwFor:
		return p.parseForExpr("", tk.Span.Start)

	case token.KwMatch:
		return p.parseMatchExpr()

	default:
		bad := p.advance()
		p.rep.Errorf(bad.Span, "unexpected token %s (%q) in expression", bad.Kind.String(), bad.Text)
		return &ast.IntLit{Raw: "0", Val: 0, Span: bad.Span}
	}
}

func (p *Parser) parseInterpString() *ast.InterpStringExpr {
	first := p.expect(token.InterpStart, "interpolated string")
	segments := []string{first.Text}
	var exprs []ast.Expr

	for {
		expr := p.parseExpr()
		exprs = append(exprs, expr)
		if mid, ok := p.match(token.InterpMid); ok {
			segments = append(segments, mid.Text)
			continue
		}
		endTok := p.expect(token.InterpEnd, "end of interpolated string")
		segments = append(segments, endTok.Text)
		return &ast.InterpStringExpr{
			Segments: segments,
			Exprs:    exprs,
			Span:     diag.Span{Start: first.Span.Start, End: endTok.Span.End},
		}
	}
}

func (p *Parser) looksLikeFuncLit() bool {
	if p.cur().Kind != token.LParen {
		return false
	}
	k1 := p.peekAt(1).Kind
	k2 := p.peekAt(2).Kind
	if k1 == token.RParen {
		return true // `(): int ...` or `() { ... }`
	}
	if (k1 == token.Ident || k1 == token.OptIdent) && k2 == token.Colon {
		return true // `(a: int, ...)`
	}
	return false
}

func (p *Parser) parseFuncLit() *ast.FuncLit {
	start := p.cur().Span.Start
	params, _ := p.parseParamList(true)
	var ret ast.TypeExpr
	if _, ok := p.match(token.Colon); ok {
		ret = p.parseTypeExpr()
	}
	body := p.parseExpr()
	return &ast.FuncLit{
		Params: params,
		Ret:    ret,
		Body:   body,
		Span:   diag.Span{Start: start, End: body.GetSpan().End},
	}
}

func (p *Parser) parseVecOrRepeatArrayLit() ast.Expr {
	openBr := p.expect(token.LBracket, "opening `[`")
	if closeBr, ok := p.match(token.RBracket); ok {
		return &ast.VecLit{
			Span: diag.Span{Start: openBr.Span.Start, End: closeBr.Span.End},
		}
	}
	first := p.parseExpr()
	if _, ok := p.match(token.Colon); ok {
		val := p.parseExpr()
		closeBr := p.expect(token.RBracket, "closing `[N: value]`")
		return &ast.RepeatArrayLit{
			Count: first,
			Value: val,
			Span:  diag.Span{Start: openBr.Span.Start, End: closeBr.Span.End},
		}
	}

	elems := []ast.Expr{first}
	lastEnd := first.GetSpan().End
	hadComma := false
	if _, ok := p.match(token.Comma); ok {
		hadComma = true
		for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
			el := p.parseExpr()
			elems = append(elems, el)
			lastEnd = el.GetSpan().End
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
	}
	closeBr := p.expect(token.RBracket, "closing `]`")
	p.checkTrailingComma(openBr, closeBr, lastEnd, hadComma, "vector/array literal")
	return &ast.VecLit{
		Elems: elems,
		Span:  diag.Span{Start: openBr.Span.Start, End: closeBr.Span.End},
	}
}

func (p *Parser) parseHashLit() ast.Expr {
	openHb := p.expect(token.HashBrack, "opening `#[`")
	if closeBr, ok := p.match(token.RBracket); ok {
		return &ast.EmptyHashLit{
			Span: diag.Span{Start: openHb.Span.Start, End: closeBr.Span.End},
		}
	}
	first := p.parseExpr()
	if _, ok := p.match(token.Colon); ok {
		firstVal := p.parseExpr()
		entries := []ast.MapEntry{{
			Key:   first,
			Value: firstVal,
			Span:  diag.Span{Start: first.GetSpan().Start, End: firstVal.GetSpan().End},
		}}
		lastEnd := firstVal.GetSpan().End
		hadComma := false
		if _, ok := p.match(token.Comma); ok {
			hadComma = true
			for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
				k := p.parseExpr()
				p.expect(token.Colon, "between map key and value")
				v := p.parseExpr()
				entries = append(entries, ast.MapEntry{
					Key:   k,
					Value: v,
					Span:  diag.Span{Start: k.GetSpan().Start, End: v.GetSpan().End},
				})
				lastEnd = v.GetSpan().End
				if _, ok := p.match(token.Comma); ok {
					hadComma = true
				} else {
					hadComma = false
					break
				}
			}
		}
		closeBr := p.expect(token.RBracket, "closing map literal `]`")
		p.checkTrailingComma(openHb, closeBr, lastEnd, hadComma, "map literal")
		return &ast.MapLit{
			Entries: entries,
			Span:    diag.Span{Start: openHb.Span.Start, End: closeBr.Span.End},
		}
	}

	elems := []ast.Expr{first}
	lastEnd := first.GetSpan().End
	hadComma := false
	if _, ok := p.match(token.Comma); ok {
		hadComma = true
		for p.cur().Kind != token.RBracket && p.cur().Kind != token.EOF {
			el := p.parseExpr()
			elems = append(elems, el)
			lastEnd = el.GetSpan().End
			if _, ok := p.match(token.Comma); ok {
				hadComma = true
			} else {
				hadComma = false
				break
			}
		}
	}
	closeBr := p.expect(token.RBracket, "closing set literal `]`")
	p.checkTrailingComma(openHb, closeBr, lastEnd, hadComma, "set literal")
	return &ast.SetLit{
		Elems: elems,
		Span:  diag.Span{Start: openHb.Span.Start, End: closeBr.Span.End},
	}
}

// -----------------------------------------------------------------------------
// Control Flow: `if`, `for`, `match`
// -----------------------------------------------------------------------------

func (p *Parser) parseIfExpr() *ast.IfExpr {
	kw := p.expect(token.KwIf, "in `if`")
	p.expect(token.LParen, "after `if`")

	var condKind ast.IfCondKind
	mutUnwrap := false
	optVar := ""
	bindName := ""
	var condExpr ast.Expr

	if p.cur().Kind == token.OptIdent && p.peekAt(1).Kind == token.RParen {
		tok := p.advance()
		condKind = ast.IfCondOptVar
		optVar = tok.Text
		bindName = strings.TrimSuffix(tok.Text, "?")
	} else if p.cur().Kind == token.Amp && p.peekAt(1).Kind == token.OptIdent && p.peekAt(2).Kind == token.RParen {
		p.advance() // `&`
		tok := p.advance()
		condKind = ast.IfCondOptVar
		mutUnwrap = true
		optVar = tok.Text
		bindName = strings.TrimSuffix(tok.Text, "?")
	} else {
		condExpr = p.parseExpr()
		if _, ok := p.match(token.KwAs); ok {
			condKind = ast.IfCondOptBind
			if u, isU := condExpr.(*ast.UnaryExpr); isU && u.Op == token.Amp {
				mutUnwrap = true
				condExpr = u.Operand
			}
			if p.cur().Kind == token.OptIdent {
				bad := p.advance()
				p.rep.Errorf(bad.Span, "unwrapped `as` binding %q must be a non-optional identifier (omit `?`)", bad.Text)
				bindName = strings.TrimSuffix(bad.Text, "?")
			} else {
				bindName = p.expect(token.Ident, "unwrapped identifier after `as`").Text
			}
		} else {
			condKind = ast.IfCondBool
		}
	}

	p.expect(token.RParen, "closing `if (...)` condition")
	thenBody := p.parseActionBody()
	endPos := thenBody.GetSpan().End

	var elseBody ast.Expr
	if p.cur().Kind == token.KwElse && !p.elseBelongsToMatch() {
		p.advance() // consume `else`
		elseBody = p.parseActionBody()
		endPos = elseBody.GetSpan().End
	}

	return &ast.IfExpr{
		CondKind:  condKind,
		MutUnwrap: mutUnwrap,
		OptVar:    optVar,
		BindName:  bindName,
		Cond:      condExpr,
		Then:      thenBody,
		Else:      elseBody,
		Span:      diag.Span{Start: kw.Span.Start, End: endPos},
	}
}

// elseBelongsToMatch implements Spec §10.3 / B3:
// When inside a `match` arm, an `else` followed by `:` (`else:`) or `<Pattern>:` (`else Status.Closed:`)
// belongs to the enclosing `match`, NOT an unbraced `if`.
func (p *Parser) elseBelongsToMatch() bool {
	if p.inMatchArm == 0 || p.cur().Kind != token.KwElse {
		return false
	}
	if p.peekAt(1).Kind == token.Colon {
		return true
	}
	// Speculatively check `<Pattern>:` right after `else`!
	savePos := p.pos
	realRep := p.rep
	tempRep := diag.NewReporter()
	p.rep = tempRep
	defer func() {
		p.pos = savePos
		p.rep = realRep
	}()
	p.advance() // `else`
	_ = p.parseMatchPatternList()
	if tempRep.HasErrors() {
		return false
	}
	return p.cur().Kind == token.Colon
}

func (p *Parser) parseForExpr(label string, startPos diag.Pos) *ast.ForExpr {
	kw := p.expect(token.KwFor, "in `for`")
	if label == "" {
		startPos = kw.Span.Start
	}
	p.expect(token.LParen, "after `for`")

	fe := &ast.ForExpr{Label: label}
	if p.looksLikeForInHeader() {
		// Parse `[&]var1 [, [&]var2] in ...`
		if _, ok := p.match(token.Amp); ok {
			fe.Var1Mut = true
		}
		fe.Var1, fe.Var1IsOpt = p.parseLoopVarName()
		if _, ok := p.match(token.Comma); ok {
			if fe.Var1Mut {
				p.rep.Errorf(kw.Span, "in two-variable `for (i, &item in ...)`, `&` goes on the second variable, not the index/key")
			}
			if _, ok2 := p.match(token.Amp); ok2 {
				fe.Var2Mut = true
			}
			fe.Var2, fe.Var2IsOpt = p.parseLoopVarName()
		}
		p.expect(token.KwIn, "in `for (... in ...)`")
		firstExpr := p.parseExpr()
		if p.cur().Kind == token.DotDot || p.cur().Kind == token.DotDotEq {
			fe.Kind = ast.ForRange
			fe.Inclusive = p.advance().Kind == token.DotDotEq
			fe.CondOrIter = firstExpr
			fe.RangeEnd = p.parseExpr()
		} else {
			fe.Kind = ast.ForIter
			fe.CondOrIter = firstExpr
		}
	} else {
		fe.Kind = ast.ForCond
		fe.CondOrIter = p.parseExpr()
	}

	p.expect(token.RParen, "closing `for (...)`")
	fe.Body = p.parseActionBody()
	endPos := fe.Body.GetSpan().End

	if p.cur().Kind == token.KwElse && !p.elseBelongsToMatch() {
		p.advance()
		fe.Else = p.parseActionBody()
		endPos = fe.Else.GetSpan().End
	}
	fe.Span = diag.Span{Start: startPos, End: endPos}
	return fe
}

func (p *Parser) parseLoopVarName() (string, bool) {
	if p.cur().Kind == token.OptIdent {
		tk := p.advance()
		return tk.Text, true
	}
	return p.expect(token.Ident, "loop variable name").Text, false
}

func (p *Parser) looksLikeForInHeader() bool {
	i := p.pos
	if p.toks[i].Kind == token.Amp {
		i++
	}
	if i >= len(p.toks) || (p.toks[i].Kind != token.Ident && p.toks[i].Kind != token.OptIdent) {
		return false
	}
	i++
	if i < len(p.toks) && p.toks[i].Kind == token.KwIn {
		return true
	}
	if i < len(p.toks) && p.toks[i].Kind == token.Comma {
		i++
		if i < len(p.toks) && p.toks[i].Kind == token.Amp {
			i++
		}
		if i < len(p.toks) && (p.toks[i].Kind == token.Ident || p.toks[i].Kind == token.OptIdent) {
			i++
			return i < len(p.toks) && p.toks[i].Kind == token.KwIn
		}
	}
	return false
}

func (p *Parser) parseMatchExpr() *ast.MatchExpr {
	kw := p.expect(token.KwMatch, "in `match`")
	p.expect(token.LParen, "after `match`")
	scrut := p.parseExpr()
	p.expect(token.RParen, "closing `match (...)`")

	var arms []ast.MatchArm
	for p.cur().Kind == token.KwWhen {
		wTok := p.advance()
		pats := p.parseMatchPatternList()
		p.expect(token.Colon, "after `when` pattern")
		p.inMatchArm++
		body := p.parseActionBody()
		p.inMatchArm--
		// Consume optional newline AutoSemi between match arms if any slipped through
		arms = append(arms, ast.MatchArm{
			Patterns: pats,
			IsElse:   false,
			Body:     body,
			Span:     diag.Span{Start: wTok.Span.Start, End: body.GetSpan().End},
		})
	}

	elseTok := p.expect(token.KwElse, "terminating `match` expression (`every match must end with else:` or `else Variant:`)")
	var elsePats []ast.Pattern
	if p.cur().Kind != token.Colon {
		elsePats = p.parseMatchPatternList()
	}
	p.expect(token.Colon, "after `else` in `match`")
	p.inMatchArm++
	elseBody := p.parseActionBody()
	p.inMatchArm--

	arms = append(arms, ast.MatchArm{
		Patterns: elsePats,
		IsElse:   true,
		Body:     elseBody,
		Span:     diag.Span{Start: elseTok.Span.Start, End: elseBody.GetSpan().End},
	})

	return &ast.MatchExpr{
		Scrutinee: scrut,
		Arms:      arms,
		Span:      diag.Span{Start: kw.Span.Start, End: elseBody.GetSpan().End},
	}
}

func (p *Parser) parseMatchPatternList() []ast.Pattern {
	var pats []ast.Pattern
	for {
		pat := p.parseSinglePattern()
		pats = append(pats, pat)
		if _, ok := p.match(token.Comma); !ok {
			break
		}
	}
	return pats
}

func (p *Parser) parseSinglePattern() ast.Pattern {
	first := p.parsePatternAtom()
	if p.cur().Kind == token.DotDot || p.cur().Kind == token.DotDotEq {
		incl := p.advance().Kind == token.DotDotEq
		high := p.parsePatternAtom()
		return &ast.RangePattern{
			Low:       first,
			High:      high,
			Inclusive: incl,
			Span:      diag.Span{Start: first.GetSpan().Start, End: high.GetSpan().End},
		}
	}
	return &ast.ExprPattern{
		Expr: first,
		Span: first.GetSpan(),
	}
}

func (p *Parser) parsePatternAtom() ast.Expr {
	if p.cur().Kind == token.Minus {
		m := p.advance()
		operand := p.parsePrimaryExpr()
		return &ast.UnaryExpr{
			Op:      token.Minus,
			Operand: operand,
			Span:    diag.Span{Start: m.Span.Start, End: operand.GetSpan().End},
		}
	}
	base := p.parsePrimaryExpr()
	for p.cur().Kind == token.Dot {
		p.advance()
		mem := p.expect(token.Ident, "enum variant in pattern")
		base = &ast.MemberExpr{
			Base:   base,
			Member: mem.Text,
			Span:   diag.Span{Start: base.GetSpan().Start, End: mem.Span.End},
		}
	}
	return base
}
