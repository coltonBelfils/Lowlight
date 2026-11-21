package lsp

import (
	"path/filepath"
	"sort"
	"strings"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/lexer"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

const (
	semTokNamespace = iota
	semTokType
	semTokStruct
	semTokEnum
	semTokInterface
	semTokTypeParameter
	semTokParameter
	semTokVariable
	semTokProperty
	semTokEnumMember
	semTokFunction
	semTokMethod
	semTokKeyword
	semTokComment
	semTokString
	semTokNumber
	semTokOperator
)

const (
	semModDeclaration = 1 << iota
	semModDefinition
	semModReadonly
	semModStatic
	semModDocumentation
	semModModification
	semModDefaultLibrary
)

var semanticTokensLegend = SemanticTokensLegend{
	TokenTypes: []string{
		"namespace",
		"type",
		"struct",
		"enum",
		"interface",
		"typeParameter",
		"parameter",
		"variable",
		"property",
		"enumMember",
		"function",
		"method",
		"keyword",
		"comment",
		"string",
		"number",
		"operator",
	},
	TokenModifiers: []string{
		"declaration",
		"definition",
		"readonly",
		"static",
		"documentation",
		"modification",
		"defaultLibrary",
	},
}

type rawSemTok struct {
	startOff  int
	line      int
	startChar int
	length    int
	tokType   uint32
	tokMods   uint32
}

type semTokCollector struct {
	src        string
	filePath   string
	prog       *sema.CheckedProgram
	mod        *sema.Module
	toks       []token.Token
	lineStarts []int
	emitted    map[int]rawSemTok // startOffset -> token
	tpScopes   []map[string]bool
	paramSyms  map[*sema.Symbol]bool
	docSpans   []diag.Span
}

func (s *Server) computeSemanticTokens(filePath, src string) *SemanticTokens {
	prog := s.getOrCheckProgram(filePath, src)
	if prog != nil {
		s.mu.RLock()
		cached := s.semTokCache[prog]
		s.mu.RUnlock()
		if cached != nil {
			return cached
		}
	}
	mod := findModuleForPath(prog, filePath)

	rep := diag.NewReporter()
	allToks, comments := lexer.LexWithComments(filePath, src, rep)
	var cleanToks []token.Token
	for _, tk := range allToks {
		if tk.Kind == token.EOF || tk.AutoSemi {
			continue
		}
		cleanToks = append(cleanToks, tk)
	}

	c := &semTokCollector{
		src:        src,
		filePath:   filePath,
		prog:       prog,
		mod:        mod,
		toks:       cleanToks,
		lineStarts: computeLineStarts(src),
		emitted:    make(map[int]rawSemTok),
		paramSyms:  make(map[*sema.Symbol]bool),
	}

	// Collect doc comment & parameter symbol metadata once per program.
	if mod != nil && mod.File != nil {
		c.docSpans = collectDocSpans(mod.File)
		for _, d := range mod.File.Decls {
			c.collectDeclParamMeta(d)
		}
		if c.prog != nil {
			for _, li := range c.prog.Lambdas {
				if li != nil {
					for _, ps := range li.ParamSyms {
						if ps != nil {
							c.paramSyms[ps] = true
						}
					}
				}
			}
		}
		for _, d := range mod.File.Decls {
			c.visitDecl(d)
		}
	}

	// Emit comment tokens (split multi-line comments line by line as required by LSP 3.17).
	for _, cm := range comments {
		mods := uint32(0)
		if c.isInsideAnyDocSpan(cm.Span) {
			mods |= semModDocumentation
		}
		c.emitMultilineRange(cm.Span.Start.Offset, cm.Span.End.Offset, semTokComment, mods)
	}

	// Sort emitted tokens by (line, startChar) and encode LSP delta quintuples.
	list := make([]rawSemTok, 0, len(c.emitted))
	for _, rt := range c.emitted {
		if rt.length > 0 {
			list = append(list, rt)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].line != list[j].line {
			return list[i].line < list[j].line
		}
		return list[i].startChar < list[j].startChar
	})

	data := make([]uint32, 0, len(list)*5)
	prevLine := 0
	prevChar := 0
	for _, rt := range list {
		deltaLine := rt.line - prevLine
		deltaStart := rt.startChar
		if deltaLine == 0 {
			deltaStart = rt.startChar - prevChar
		}
		if deltaLine < 0 || deltaStart < 0 {
			continue
		}
		data = append(data, uint32(deltaLine), uint32(deltaStart), uint32(rt.length), rt.tokType, rt.tokMods)
		prevLine = rt.line
		prevChar = rt.startChar
	}

	res := &SemanticTokens{Data: data}
	if prog != nil {
		s.mu.Lock()
		if len(s.semTokCache) > 64 {
			s.semTokCache = make(map[*sema.CheckedProgram]*SemanticTokens)
		}
		s.semTokCache[prog] = res
		s.mu.Unlock()
	}
	return res
}

func computeLineStarts(src string) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (c *semTokCollector) offsetToLineChar(off int) (int, int) {
	if off < 0 {
		off = 0
	}
	if off > len(c.src) {
		off = len(c.src)
	}
	line := sort.Search(len(c.lineStarts), func(i int) bool {
		return c.lineStarts[i] > off
	}) - 1
	if line < 0 {
		line = 0
	}
	lineStart := c.lineStarts[line]
	col := utf16Len(c.src[lineStart:off])
	return line, col
}

func collectDocSpans(f *ast.File) []diag.Span {
	if f == nil {
		return nil
	}
	var spans []diag.Span
	add := func(sp diag.Span) {
		if sp.Start.Line > 0 {
			spans = append(spans, sp)
		}
	}
	add(f.DocSpan)
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.StructDecl:
			add(v.DocSpan)
			for _, f := range v.Fields {
				add(f.DocSpan)
			}
		case *ast.EnumDecl:
			add(v.DocSpan)
			for _, ev := range v.Variants {
				add(ev.DocSpan)
			}
		case *ast.InterfaceDecl:
			add(v.DocSpan)
			for _, m := range v.Methods {
				add(m.DocSpan)
			}
		case *ast.DistinctTypeDecl:
			add(v.DocSpan)
		case *ast.MethodDecl:
			add(v.DocSpan)
		case *ast.VarDecl:
			add(v.DocSpan)
		case *ast.ExternFnDecl:
			add(v.DocSpan)
		}
	}
	return spans
}

func (c *semTokCollector) isInsideAnyDocSpan(cmSpan diag.Span) bool {
	for _, docSp := range c.docSpans {
		if cmSpan.Start.Offset >= docSp.Start.Offset && cmSpan.End.Offset <= docSp.End.Offset {
			return true
		}
	}
	return false
}

func (c *semTokCollector) collectDeclParamMeta(d ast.Decl) {
	if c.prog == nil || c.mod == nil {
		return
	}
	switch decl := d.(type) {
	case *ast.VarDecl:
		if _, ok := decl.Value.(*ast.FuncLit); ok {
			if fi := findFuncInstanceForDecl(c.prog, c.mod, decl.Span); fi != nil {
				for _, ps := range fi.ParamSyms {
					if ps != nil {
						c.paramSyms[ps] = true
					}
				}
			}
		}
	case *ast.MethodDecl:
		if fi := findFuncInstanceForDecl(c.prog, c.mod, decl.Span); fi != nil {
			for _, ps := range fi.ParamSyms {
				if ps != nil {
					c.paramSyms[ps] = true
				}
			}
		}
	}
}

func (c *semTokCollector) emitToken(tk token.Token, tokType, mods uint32) {
	if tk.Span.Start.Offset < 0 || tk.Span.End.Offset <= tk.Span.Start.Offset || tk.Span.End.Offset > len(c.src) {
		return
	}
	text := c.src[tk.Span.Start.Offset:tk.Span.End.Offset]
	if strings.ContainsRune(text, '\n') {
		return
	}
	line, char := c.offsetToLineChar(tk.Span.Start.Offset)
	length := utf16Len(text)
	if length <= 0 {
		return
	}
	if _, exists := c.emitted[tk.Span.Start.Offset]; exists {
		// Merge modification flag if re-emitted on write target
		cur := c.emitted[tk.Span.Start.Offset]
		cur.tokMods |= mods
		c.emitted[tk.Span.Start.Offset] = cur
		return
	}
	c.emitted[tk.Span.Start.Offset] = rawSemTok{
		startOff:  tk.Span.Start.Offset,
		line:      line,
		startChar: char,
		length:    length,
		tokType:   tokType,
		tokMods:   mods,
	}
}

func (c *semTokCollector) emitMultilineRange(startOff, endOff int, tokType, mods uint32) {
	if startOff < 0 {
		startOff = 0
	}
	if endOff > len(c.src) {
		endOff = len(c.src)
	}
	if startOff >= endOff {
		return
	}
	pos := startOff
	for pos < endOff {
		nl := strings.IndexByte(c.src[pos:endOff], '\n')
		segEnd := endOff
		if nl >= 0 {
			segEnd = pos + nl
		}
		if segEnd > pos && c.src[segEnd-1] == '\r' {
			segEnd--
		}
		if segEnd > pos {
			line, char := c.offsetToLineChar(pos)
			length := utf16Len(c.src[pos:segEnd])
			if length > 0 {
				if _, exists := c.emitted[pos]; !exists {
					c.emitted[pos] = rawSemTok{
						startOff:  pos,
						line:      line,
						startChar: char,
						length:    length,
						tokType:   tokType,
						tokMods:   mods,
					}
				}
			}
		}
		if nl < 0 {
			break
		}
		pos = pos + nl + 1
	}
}

func (c *semTokCollector) findTokensInSpan(sp diag.Span) []token.Token {
	if sp.Start.Offset < 0 || sp.End.Offset <= sp.Start.Offset {
		return nil
	}
	lo := sort.Search(len(c.toks), func(i int) bool {
		return c.toks[i].Span.Start.Offset >= sp.Start.Offset
	})
	var res []token.Token
	for i := lo; i < len(c.toks); i++ {
		tk := c.toks[i]
		if tk.Span.Start.Offset >= sp.End.Offset {
			break
		}
		res = append(res, tk)
	}
	return res
}

func (c *semTokCollector) emitFirstIdentInSpan(sp diag.Span, name string, tokType, mods uint32) {
	if name == "" {
		return
	}
	for _, tk := range c.findTokensInSpan(sp) {
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			c.emitToken(tk, tokType, mods)
			return
		}
	}
}

func (c *semTokCollector) emitHeaderIdentInSpan(sp diag.Span, name string, tokType, mods uint32) {
	if name == "" {
		return
	}
	for _, tk := range c.findTokensInSpan(sp) {
		if tk.Kind == token.Assign || tk.Kind == token.LBrace {
			return
		}
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			c.emitToken(tk, tokType, mods)
			return
		}
	}
}

func (c *semTokCollector) emitLastIdentInSpan(sp diag.Span, name string, tokType, mods uint32) {
	if name == "" {
		return
	}
	toks := c.findTokensInSpan(sp)
	for i := len(toks) - 1; i >= 0; i-- {
		tk := toks[i]
		if (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == name {
			c.emitToken(tk, tokType, mods)
			return
		}
	}
}

func (c *semTokCollector) pushTP(tps []ast.TypeParam) {
	if len(tps) == 0 {
		return
	}
	m := make(map[string]bool, len(tps))
	for _, tp := range tps {
		if tp.Name != "" {
			m[tp.Name] = true
		}
	}
	c.tpScopes = append(c.tpScopes, m)
}

func (c *semTokCollector) pushTPNames(names []string) {
	if len(names) == 0 {
		return
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			m[n] = true
		}
	}
	c.tpScopes = append(c.tpScopes, m)
}

func (c *semTokCollector) popTP(has bool) {
	if has && len(c.tpScopes) > 0 {
		c.tpScopes = c.tpScopes[:len(c.tpScopes)-1]
	}
}

func (c *semTokCollector) isTypeParam(name string) bool {
	for i := len(c.tpScopes) - 1; i >= 0; i-- {
		if c.tpScopes[i][name] {
			return true
		}
	}
	return false
}

func (c *semTokCollector) isStdMod(m *sema.Module) bool {
	if m == nil || c.prog == nil || c.prog.StdDir == "" {
		return false
	}
	stdAbs, err := filepath.Abs(c.prog.StdDir)
	if err != nil {
		stdAbs = c.prog.StdDir
	}
	mAbs, err2 := filepath.Abs(m.Path)
	if err2 != nil {
		mAbs = m.Path
	}
	return strings.HasPrefix(filepath.ToSlash(mAbs), filepath.ToSlash(stdAbs)+"/")
}

func (c *semTokCollector) classifyTypeName(modAlias, name string) (uint32, uint32) {
	if modAlias == "" && c.isTypeParam(name) {
		return semTokTypeParameter, 0
	}
	if modAlias == "" && (name == "Self" || LookupBuiltinTypeDoc(name) != nil) {
		return semTokType, semModDefaultLibrary
	}
	if c.mod != nil {
		if nom := lookupNominalType(c.mod, modAlias, name); nom != nil {
			mods := uint32(0)
			if c.prog != nil {
				switch v := nom.(type) {
				case *types.Struct:
					if c.isStdMod(c.prog.StructMods[v]) {
						mods |= semModDefaultLibrary
					}
					return semTokStruct, mods
				case *types.Enum:
					if c.isStdMod(c.prog.EnumMods[v]) {
						mods |= semModDefaultLibrary
					}
					return semTokEnum, mods
				case *types.Interface:
					if c.isStdMod(c.prog.InterfaceMods[v]) {
						mods |= semModDefaultLibrary
					}
					return semTokInterface, mods
				case *types.Distinct:
					if c.isStdMod(c.prog.DistinctMods[v]) {
						mods |= semModDefaultLibrary
					}
					return semTokType, mods
				}
			}
		}
		if gst := lookupGenStruct(c.mod, modAlias, name); gst != nil {
			mods := uint32(0)
			if c.isStdMod(gst.Mod) {
				mods |= semModDefaultLibrary
			}
			return semTokStruct, mods
		}
		if gen := lookupGenEnum(c.mod, modAlias, name); gen != nil {
			mods := uint32(0)
			if c.isStdMod(gen.Mod) {
				mods |= semModDefaultLibrary
			}
			return semTokEnum, mods
		}
		if gi := lookupGenInterface(c.mod, modAlias, name); gi != nil {
			mods := uint32(0)
			if c.isStdMod(gi.Mod) {
				mods |= semModDefaultLibrary
			}
			return semTokInterface, mods
		}
	}
	return semTokType, 0
}

func (c *semTokCollector) visitTypeExpr(te ast.TypeExpr) {
	if te == nil {
		return
	}
	switch v := te.(type) {
	case *ast.NamedType:
		if v.Module != "" {
			c.emitFirstIdentInSpan(v.Span, v.Module, semTokNamespace, 0)
		}
		tt, mods := c.classifyTypeName(v.Module, v.Name)
		c.emitFirstIdentInSpan(v.Span, v.Name, tt, mods)
		for _, ta := range v.TypeArgs {
			c.visitTypeExpr(ta)
		}
	case *ast.OptionalType:
		c.visitTypeExpr(v.Elem)
	case *ast.MutBorrowType:
		c.visitTypeExpr(v.Elem)
	case *ast.OwnType:
		c.visitTypeExpr(v.Elem)
	case *ast.VectorType:
		c.visitTypeExpr(v.Elem)
	case *ast.StackArrayType:
		c.visitTypeExpr(v.Elem)
	case *ast.TupleType:
		for _, el := range v.Elems {
			c.visitTypeExpr(el)
		}
	case *ast.SetType:
		c.visitTypeExpr(v.Elem)
	case *ast.MapType:
		c.visitTypeExpr(v.Key)
		c.visitTypeExpr(v.Value)
	case *ast.FuncType:
		for _, p := range v.Params {
			c.visitTypeExpr(p.Type)
		}
		c.visitTypeExpr(v.Ret)
	}
}

func (c *semTokCollector) visitDecl(d ast.Decl) {
	switch decl := d.(type) {
	case *ast.ImportDecl:
		if decl.Alias != "" {
			c.emitLastIdentInSpan(decl.Span, decl.Alias, semTokNamespace, semModDeclaration)
		}

	case *ast.StructDecl:
		c.emitFirstIdentInSpan(decl.Span, decl.Name, semTokStruct, semModDeclaration|semModDefinition)
		c.pushTP(decl.TypeParams)
		for _, tp := range decl.TypeParams {
			c.emitFirstIdentInSpan(tp.Span, tp.Name, semTokTypeParameter, semModDeclaration)
			for _, b := range tp.Bounds {
				bt, bmods := c.classifyTypeName("", b)
				c.emitFirstIdentInSpan(tp.Span, b, bt, bmods)
			}
		}
		for _, f := range decl.Fields {
			c.emitHeaderIdentInSpan(f.Span, f.Name, semTokProperty, semModDeclaration)
			c.visitTypeExpr(f.Type)
			if f.Default != nil {
				c.visitExpr(c.prog.TopBodyMaps, f.Default, false)
			}
		}
		c.popTP(len(decl.TypeParams) > 0)

	case *ast.EnumDecl:
		c.emitFirstIdentInSpan(decl.Span, decl.Name, semTokEnum, semModDeclaration|semModDefinition)
		c.pushTP(decl.TypeParams)
		for _, tp := range decl.TypeParams {
			c.emitFirstIdentInSpan(tp.Span, tp.Name, semTokTypeParameter, semModDeclaration)
			for _, b := range tp.Bounds {
				bt, bmods := c.classifyTypeName("", b)
				c.emitFirstIdentInSpan(tp.Span, b, bt, bmods)
			}
		}
		for _, ev := range decl.Variants {
			c.emitHeaderIdentInSpan(ev.Span, ev.Name, semTokEnumMember, semModDeclaration|semModReadonly)
			c.visitTypeExpr(ev.PayloadType)
		}
		c.popTP(len(decl.TypeParams) > 0)

	case *ast.InterfaceDecl:
		c.emitFirstIdentInSpan(decl.Span, decl.Name, semTokInterface, semModDeclaration|semModDefinition)
		c.pushTP(decl.TypeParams)
		for _, tp := range decl.TypeParams {
			c.emitFirstIdentInSpan(tp.Span, tp.Name, semTokTypeParameter, semModDeclaration)
			for _, b := range tp.Bounds {
				bt, bmods := c.classifyTypeName("", b)
				c.emitFirstIdentInSpan(tp.Span, b, bt, bmods)
			}
		}
		for _, m := range decl.Methods {
			c.emitHeaderIdentInSpan(m.Span, m.Name, semTokMethod, semModDeclaration)
			for _, p := range m.Params {
				c.emitHeaderIdentInSpan(p.Span, p.Name, semTokParameter, paramModifiers(p.Type))
				c.visitTypeExpr(p.Type)
			}
			c.visitTypeExpr(m.Ret)
		}
		c.popTP(len(decl.TypeParams) > 0)

	case *ast.DistinctTypeDecl:
		c.emitFirstIdentInSpan(decl.Span, decl.Name, semTokType, semModDeclaration|semModDefinition)
		c.visitTypeExpr(decl.Base)

	case *ast.ExternFnDecl:
		name := decl.Name
		if decl.Alias != "" {
			name = decl.Alias
		}
		c.emitHeaderIdentInSpan(decl.Span, name, semTokFunction, semModDeclaration)
		for _, p := range decl.Params {
			c.emitHeaderIdentInSpan(p.Span, p.Name, semTokParameter, paramModifiers(p.Type))
			c.visitTypeExpr(p.Type)
		}
		c.visitTypeExpr(decl.Ret)

	case *ast.MethodDecl:
		rtType, rtMods := c.classifyTypeName("", decl.RecvType)
		c.emitHeaderIdentInSpan(decl.Span, decl.RecvType, rtType, rtMods)
		mods := uint32(semModDeclaration | semModDefinition)
		if decl.RecvMode == ast.ReceiverMode(-1) {
			mods |= semModStatic
		}
		// Find method name right after `.` in `bind Type.method =`
		c.emitMethodDeclName(decl, mods)
		c.pushTPNames(decl.RecvTypeParams)
		c.pushTP(decl.TypeParams)
		for _, tp := range decl.TypeParams {
			c.emitFirstIdentInSpan(tp.Span, tp.Name, semTokTypeParameter, semModDeclaration)
			for _, b := range tp.Bounds {
				bt, bmods := c.classifyTypeName("", b)
				c.emitFirstIdentInSpan(tp.Span, b, bt, bmods)
			}
		}
		for _, p := range decl.Params {
			c.emitHeaderIdentInSpan(p.Span, p.Name, semTokParameter, paramModifiers(p.Type))
			c.visitTypeExpr(p.Type)
		}
		c.visitTypeExpr(decl.Ret)
		var bm *sema.BodyMaps
		if fi := findFuncInstanceForDecl(c.prog, c.mod, decl.Span); fi != nil {
			bm = fi.BodyMaps
		}
		c.visitExpr(bm, decl.Body, false)
		c.popTP(len(decl.TypeParams) > 0)
		c.popTP(len(decl.RecvTypeParams) > 0)

	case *ast.VarDecl:
		if fl, isFn := decl.Value.(*ast.FuncLit); isFn {
			c.emitHeaderIdentInSpan(decl.Span, decl.Name, semTokFunction, semModDeclaration|semModDefinition|semModReadonly)
			c.pushTP(decl.TypeParams)
			for _, tp := range decl.TypeParams {
				c.emitFirstIdentInSpan(tp.Span, tp.Name, semTokTypeParameter, semModDeclaration)
				for _, b := range tp.Bounds {
					bt, bmods := c.classifyTypeName("", b)
					c.emitFirstIdentInSpan(tp.Span, b, bt, bmods)
				}
			}
			for _, p := range fl.Params {
				c.emitHeaderIdentInSpan(p.Span, p.Name, semTokParameter, paramModifiers(p.Type))
				c.visitTypeExpr(p.Type)
			}
			c.visitTypeExpr(fl.Ret)
			var bm *sema.BodyMaps
			if fi := findFuncInstanceForDecl(c.prog, c.mod, decl.Span); fi != nil {
				bm = fi.BodyMaps
			}
			c.visitExpr(bm, fl.Body, false)
			c.popTP(len(decl.TypeParams) > 0)
		} else {
			mods := uint32(semModDeclaration)
			if decl.IsBind {
				mods |= semModReadonly
			}
			c.emitHeaderIdentInSpan(decl.Span, decl.Name, semTokVariable, mods)
			c.visitTypeExpr(decl.Type)
			var bm *sema.BodyMaps
			if c.prog != nil {
				bm = c.prog.TopBodyMaps
			}
			c.visitExpr(bm, decl.Value, false)
		}
	}
}

func (c *semTokCollector) emitMethodDeclName(decl *ast.MethodDecl, mods uint32) {
	seenDot := false
	for _, tk := range c.findTokensInSpan(decl.Span) {
		if tk.Kind == token.Assign || tk.Kind == token.LBrace {
			return
		}
		if tk.Kind == token.Dot {
			seenDot = true
			continue
		}
		if seenDot && (tk.Kind == token.Ident || tk.Kind == token.OptIdent) && tk.Text == decl.Name {
			c.emitToken(tk, semTokMethod, mods)
			return
		}
	}
}

func (c *semTokCollector) visitStmt(bm *sema.BodyMaps, st ast.Stmt) {
	if st == nil {
		return
	}
	switch s := st.(type) {
	case *ast.VarStmt:
		mods := uint32(semModDeclaration)
		if s.IsBind {
			mods |= semModReadonly
		}
		if s.IsUnpack {
			for _, uv := range s.UnpackVars {
				c.emitHeaderIdentInSpan(uv.Span, uv.Name, semTokVariable, mods)
				c.visitTypeExpr(uv.Type)
			}
		} else {
			c.emitHeaderIdentInSpan(s.Span, s.Name, semTokVariable, mods)
			c.visitTypeExpr(s.Type)
		}
		c.visitExpr(bm, s.Value, false)
		if s.FallbackUnwind != nil {
			c.visitExpr(bm, s.FallbackUnwind.Value, false)
		}
	case *ast.AssignStmt:
		if s.IsUnpack {
			for _, tgt := range s.UnpackTargets {
				c.visitExpr(bm, tgt, true)
			}
		} else {
			c.visitExpr(bm, s.Target, true)
		}
		c.visitExpr(bm, s.Value, false)
	case *ast.UnwindStmt:
		c.visitExpr(bm, s.Value, false)
	case *ast.ExprStmt:
		c.visitExpr(bm, s.Expr, false)
	}
}

func (c *semTokCollector) visitExpr(bm *sema.BodyMaps, e ast.Expr, isWrite bool) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.BlockExpr:
		for _, st := range v.Stmts {
			c.visitStmt(bm, st)
		}
		c.visitExpr(bm, v.Tail, false)

	case *ast.StmtBodyExpr:
		c.visitStmt(bm, v.Stmt)

	case *ast.IfExpr:
		if v.CondKind == ast.IfCondOptVar {
			c.emitFirstIdentInSpan(v.Span, v.OptVar, semTokVariable, semModReadonly)
		}
		c.visitExpr(bm, v.Cond, false)
		if v.CondKind == ast.IfCondOptBind && v.BindName != "" {
			c.emitFirstIdentInSpan(v.Span, v.BindName, semTokVariable, semModDeclaration|semModReadonly)
		}
		c.visitTypeExpr(v.EvalType)
		c.visitExpr(bm, v.Then, false)
		c.visitExpr(bm, v.Else, false)

	case *ast.ForExpr:
		if v.Var1 != "" {
			c.emitFirstIdentInSpan(v.Span, v.Var1, semTokVariable, semModDeclaration|semModReadonly)
		}
		if v.Var2 != "" {
			c.emitFirstIdentInSpan(v.Span, v.Var2, semTokVariable, semModDeclaration|semModReadonly)
		}
		c.visitExpr(bm, v.CondOrIter, false)
		c.visitExpr(bm, v.RangeEnd, false)
		c.visitTypeExpr(v.EvalType)
		c.visitExpr(bm, v.Body, false)
		c.visitExpr(bm, v.Else, false)

	case *ast.WhileExpr:
		c.visitExpr(bm, v.CondOrIter, false)
		c.visitExpr(bm, v.RangeEnd, false)
		c.visitTypeExpr(v.EvalType)
		c.visitExpr(bm, v.Body, false)
		c.visitExpr(bm, v.Else, false)

	case *ast.MatchExpr:
		for _, scr := range v.Scrutinees {
			c.visitExpr(bm, scr, false)
		}
		for _, arm := range v.Arms {
			if arm.Synthetic {
				continue
			}
			for _, pat := range arm.AllPatterns() {
				switch p := pat.(type) {
				case *ast.ExprPattern:
					c.visitExpr(bm, p.Expr, false)
				case *ast.ContainsPattern:
					c.visitExpr(bm, p.Container, false)
				case *ast.MethodPattern:
					if bm != nil {
						if mpInfo := bm.MethodPatterns[p]; mpInfo != nil && mpInfo.Call != nil {
							c.visitExpr(bm, mpInfo.Call, false)
							continue
						}
					}
					for _, ta := range p.TypeArgs {
						c.visitTypeExpr(ta)
					}
					for _, a := range p.Args {
						c.visitExpr(bm, a, false)
					}
					c.emitLastIdentInSpan(p.MethodSpan, p.Method, semTokMethod, 0)
				case *ast.VariantPattern:
					c.visitExpr(bm, p.Target, false)
					for _, b := range p.Bindings {
						c.emitHeaderIdentInSpan(b.Span, b.BindName, semTokVariable, semModDeclaration|semModReadonly)
					}
				}
			}
			c.visitExpr(bm, arm.Body, false)
		}

	case *ast.FuncLit:
		lbm := bm
		if c.prog != nil {
			for _, li := range c.prog.Lambdas {
				if li != nil && li.Lit == v {
					lbm = li.BodyMaps
					break
				}
			}
		}
		for _, p := range v.Params {
			c.emitHeaderIdentInSpan(p.Span, p.Name, semTokParameter, paramModifiers(p.Type))
			c.visitTypeExpr(p.Type)
		}
		c.visitTypeExpr(v.Ret)
		c.visitExpr(lbm, v.Body, false)

	case *ast.CallExpr:
		for _, ta := range v.TypeArgs {
			c.visitTypeExpr(ta)
		}
		for _, a := range v.Args {
			c.visitExpr(bm, a, false)
		}
		if inf := exprInfoFromBM(c.prog, bm, v); inf != nil {
			switch inf.CallKind {
			case sema.CallBuiltinFunc, sema.CallIntrinsic:
				c.emitFirstIdentInSpan(v.Callee.GetSpan(), inf.BuiltinName, semTokFunction, semModDefaultLibrary)
			case sema.CallBuiltinMethod:
				if mem, ok := v.Callee.(*ast.MemberExpr); ok {
					c.visitExpr(bm, mem.Base, false)
					c.emitLastIdentInSpan(mem.Span, mem.Member, semTokMethod, semModDefaultLibrary)
					return
				}
			case sema.CallInterfaceMethod:
				if mem, ok := v.Callee.(*ast.MemberExpr); ok {
					c.visitExpr(bm, mem.Base, false)
					c.emitLastIdentInSpan(mem.Span, mem.Member, semTokMethod, 0)
					return
				}
			case sema.CallDirectFunc:
				var mods uint32
				if inf.TargetFunc != nil && c.isStdMod(inf.TargetFunc.Mod) {
					mods |= semModDefaultLibrary
				}
				if inf.TargetFunc != nil && inf.TargetFunc.IsStaticAssoc {
					mods |= semModStatic
				}
				if mem, ok := v.Callee.(*ast.MemberExpr); ok {
					if inf.TargetFunc != nil && inf.TargetFunc.IsStaticAssoc {
						// Type.method(...) or mod.fn(...)
						c.emitMemberBaseAsTypeOrNs(bm, mem.Base)
					} else {
						c.visitExpr(bm, mem.Base, false)
					}
					tokType := semTokMethod
					if _, isModAlias := c.modAliasOfBase(mem.Base); isModAlias {
						tokType = semTokFunction
					}
					c.emitLastIdentInSpan(mem.Span, mem.Member, uint32(tokType), mods)
					return
				}
				if id, ok := v.Callee.(*ast.IdentExpr); ok {
					c.emitFirstIdentInSpan(id.Span, id.Name, semTokFunction, mods)
					return
				}
			case sema.CallExternC:
				if id, ok := v.Callee.(*ast.IdentExpr); ok {
					c.emitFirstIdentInSpan(id.Span, id.Name, semTokFunction, 0)
					return
				}
			case sema.CallEnumVariant:
				if mem, ok := v.Callee.(*ast.MemberExpr); ok {
					c.emitMemberBaseAsTypeOrNs(bm, mem.Base)
					c.emitLastIdentInSpan(mem.Span, mem.Member, semTokEnumMember, semModReadonly)
					return
				}
			}
		}
		c.visitExpr(bm, v.Callee, false)

	case *ast.MemberExpr:
		inf := exprInfoFromBM(c.prog, bm, v)
		if inf != nil && inf.EnumVariant != nil {
			c.emitMemberBaseAsTypeOrNs(bm, v.Base)
			c.emitLastIdentInSpan(v.Span, v.Member, semTokEnumMember, semModReadonly)
			return
		}
		if _, isAlias := c.modAliasOfBase(v.Base); isAlias {
			c.emitFirstIdentInSpan(v.Base.GetSpan(), c.modAliasName(v.Base), semTokNamespace, 0)
			tokType := semTokVariable
			mods := uint32(semModReadonly)
			if inf != nil && inf.Sym != nil {
				if inf.Sym.FuncInst != nil || inf.Sym.ExternFn != nil {
					tokType = semTokFunction
				}
				if inf.Sym.FuncInst != nil && c.isStdMod(inf.Sym.FuncInst.Mod) {
					mods |= semModDefaultLibrary
				}
			}
			c.emitLastIdentInSpan(v.Span, v.Member, uint32(tokType), mods)
			return
		}
		c.visitExpr(bm, v.Base, false)
		mods := uint32(0)
		if isWrite {
			mods |= semModModification
		}
		c.emitLastIdentInSpan(v.Span, v.Member, semTokProperty, mods)

	case *ast.StructLit:
		if v.Module != "" {
			c.emitFirstIdentInSpan(v.Span, v.Module, semTokNamespace, 0)
		}
		tt, tmods := c.classifyTypeName(v.Module, v.TypeName)
		c.emitFirstIdentInSpan(v.Span, v.TypeName, tt, tmods)
		for _, ta := range v.TypeArgs {
			c.visitTypeExpr(ta)
		}
		for _, f := range v.Fields {
			c.emitHeaderIdentInSpan(f.Span, f.Name, semTokProperty, 0)
			c.visitExpr(bm, f.Value, false)
		}

	case *ast.IdentExpr:
		inf := exprInfoFromBM(c.prog, bm, v)
		mods := uint32(0)
		if isWrite {
			mods |= semModModification
		}
		if inf != nil && inf.Sym != nil {
			if c.paramSyms[inf.Sym] {
				if inf.Sym.BindKind == sema.BindReadBorrow {
					mods |= semModReadonly
				}
				c.emitFirstIdentInSpan(v.Span, v.Name, semTokParameter, mods)
				return
			}
			if inf.Sym.FuncInst != nil {
				if c.isStdMod(inf.Sym.FuncInst.Mod) {
					mods |= semModDefaultLibrary
				}
				c.emitFirstIdentInSpan(v.Span, v.Name, semTokFunction, mods)
				return
			}
			if inf.Sym.ExternFn != nil {
				c.emitFirstIdentInSpan(v.Span, v.Name, semTokFunction, mods)
				return
			}
			if inf.Sym.IsBind || inf.Sym.BindKind == sema.BindReadBorrow {
				mods |= semModReadonly
			}
			c.emitFirstIdentInSpan(v.Span, v.Name, semTokVariable, mods)
			return
		}
		if LookupBuiltinFunc(v.Name) != nil {
			c.emitFirstIdentInSpan(v.Span, v.Name, semTokFunction, semModDefaultLibrary)
			return
		}
		if LookupBuiltinTypeDoc(v.Name) != nil {
			c.emitFirstIdentInSpan(v.Span, v.Name, semTokType, semModDefaultLibrary)
			return
		}
		if c.mod != nil {
			if _, isAlias := c.mod.Aliased[v.Name]; isAlias {
				c.emitFirstIdentInSpan(v.Span, v.Name, semTokNamespace, 0)
				return
			}
		}

	case *ast.BinaryExpr:
		c.visitExpr(bm, v.Left, false)
		c.visitExpr(bm, v.Right, false)
	case *ast.UnaryExpr:
		c.visitExpr(bm, v.Operand, false)
	case *ast.UnsafeExpr:
		c.visitExpr(bm, v.Expr, false)
	case *ast.IndexExpr:
		c.visitExpr(bm, v.Base, false)
		c.visitExpr(bm, v.Index, false)
	case *ast.SliceExpr:
		c.visitExpr(bm, v.Base, false)
		c.visitExpr(bm, v.Low, false)
		c.visitExpr(bm, v.High, false)
	case *ast.RangeExpr:
		c.visitExpr(bm, v.Low, false)
		c.visitExpr(bm, v.High, false)
	case *ast.TupleLit:
		for _, el := range v.Elems {
			c.visitExpr(bm, el, false)
		}
	case *ast.ArrayLit:
		for _, el := range v.Elems {
			c.visitExpr(bm, el, false)
		}
	case *ast.VecLit:
		for _, el := range v.Elems {
			c.visitExpr(bm, el, false)
		}
	case *ast.SetLit:
		for _, el := range v.Elems {
			c.visitExpr(bm, el, false)
		}
	case *ast.MapLit:
		for _, ent := range v.Entries {
			c.visitExpr(bm, ent.Key, false)
			c.visitExpr(bm, ent.Value, false)
		}
	case *ast.InterpStringExpr:
		for _, sub := range v.Exprs {
			c.visitExpr(bm, sub, false)
		}
	}
}

func (c *semTokCollector) modAliasName(e ast.Expr) string {
	if id, ok := e.(*ast.IdentExpr); ok {
		return id.Name
	}
	return ""
}

func (c *semTokCollector) modAliasOfBase(e ast.Expr) (*sema.Module, bool) {
	if c.mod == nil {
		return nil, false
	}
	if id, ok := e.(*ast.IdentExpr); ok {
		m, ok2 := c.mod.Aliased[id.Name]
		return m, ok2
	}
	return nil, false
}

func paramModifiers(te ast.TypeExpr) uint32 {
	mods := uint32(semModDeclaration)
	if _, isMut := te.(*ast.MutBorrowType); !isMut {
		if _, isOwn := te.(*ast.OwnType); !isOwn {
			mods |= semModReadonly
		}
	}
	return mods
}

func (c *semTokCollector) emitMemberBaseAsTypeOrNs(bm *sema.BodyMaps, base ast.Expr) {
	switch b := base.(type) {
	case *ast.IdentExpr:
		if _, isMod := c.modAliasOfBase(b); isMod {
			c.emitFirstIdentInSpan(b.Span, b.Name, semTokNamespace, 0)
			return
		}
		if inf := exprInfoFromBM(c.prog, bm, b); inf != nil && inf.Sym != nil {
			c.visitExpr(bm, b, false)
			return
		}
		tt, tmods := c.classifyTypeName("", b.Name)
		c.emitFirstIdentInSpan(b.Span, b.Name, tt, tmods)
	case *ast.MemberExpr:
		// `mod.Enum`
		if _, isMod := c.modAliasOfBase(b.Base); isMod {
			c.emitFirstIdentInSpan(b.Base.GetSpan(), c.modAliasName(b.Base), semTokNamespace, 0)
			tt, tmods := c.classifyTypeName(c.modAliasName(b.Base), b.Member)
			c.emitLastIdentInSpan(b.Span, b.Member, tt, tmods)
			return
		}
		c.visitExpr(bm, b, false)
	case *ast.IndexExpr:
		c.emitMemberBaseAsTypeOrNs(bm, b.Base)
	default:
		c.visitExpr(bm, base, false)
	}
}
