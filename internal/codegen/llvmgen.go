package codegen

import (
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"

	"lowlight/internal/ir"
	"lowlight/internal/sema"
	"lowlight/internal/token"
	"lowlight/internal/types"
)

// EmitLLVM generates a complete, valid, standalone LLVM IR (.ll) translation unit from `prog`.
func EmitLLVM(prog *ir.Program) string {
	g := newLLVMEmitter(prog)
	g.emitAll()
	return g.sb.String()
}

type stringLitEntry struct {
	id     int
	val    string
	symbol string
	length int
}

type helperDecl struct {
	fn         string
	retTy      string
	paramTypes string
}

type llvmEmitter struct {
	prog         *ir.Program
	unitMode     UnitMode
	defineFuncs  map[string]bool
	sb           strings.Builder
	strings      map[string]*stringLitEntry
	stringList   []*stringLitEntry
	nextStrID    int
	nextRegID    int
	nextBlockID  int
	isTerminated bool
	currentBlock string
	localSlots   map[*ir.Local]string
	movedSlots   map[string]string
	entryAllocas []string
	declaredFns  map[string]bool
	declaredTyps map[string]bool
	funcSet      map[string]bool
	helperDecls  []helperDecl
	structMap    map[string]*types.Struct
	lambdaEnvMap map[string]*sema.LambdaInfo
}

func newLLVMEmitter(prog *ir.Program) *llvmEmitter {
	funcSet := make(map[string]bool)
	for _, fn := range prog.Funcs {
		funcSet[fn.CName] = true
	}
	for _, st := range prog.StaticThunks {
		funcSet[st.ThunkCName] = true
	}
	for _, ef := range prog.ExternFuncs {
		funcSet[ef.Name] = true
	}
	structMap := make(map[string]*types.Struct)
	for _, st := range prog.Structs {
		structMap[st.CName] = st
	}
	lambdaEnvMap := make(map[string]*sema.LambdaInfo)
	if prog.Checked != nil {
		for _, li := range prog.Checked.Lambdas {
			lambdaEnvMap[li.EnvCName] = li
		}
	}
	return &llvmEmitter{
		prog:         prog,
		strings:      make(map[string]*stringLitEntry),
		localSlots:   make(map[*ir.Local]string),
		movedSlots:   make(map[string]string),
		declaredFns:  make(map[string]bool),
		declaredTyps: make(map[string]bool),
		funcSet:      funcSet,
		structMap:    structMap,
		lambdaEnvMap: lambdaEnvMap,
	}
}

func (g *llvmEmitter) raw(s string) {
	g.sb.WriteString(s)
	g.sb.WriteByte('\n')
}

func (g *llvmEmitter) indentRaw(indent int, s string) {
	for i := 0; i < indent; i++ {
		g.sb.WriteString("    ")
	}
	g.sb.WriteString(s)
	g.sb.WriteByte('\n')
}

func (g *llvmEmitter) line(format string, args ...any) {
	if len(args) == 0 {
		g.sb.WriteString(strings.ReplaceAll(format, "%%", "%"))
		g.sb.WriteByte('\n')
		return
	}
	g.sb.WriteString(fmt.Sprintf(format, args...))
	g.sb.WriteByte('\n')
}

func (g *llvmEmitter) indentLine(indent int, format string, args ...any) {
	for i := 0; i < indent; i++ {
		g.sb.WriteString("    ")
	}
	g.line(format, args...)
}

func (g *llvmEmitter) newReg() string {
	r := fmt.Sprintf("%%t.%d", g.nextRegID)
	g.nextRegID++
	return r
}

func (g *llvmEmitter) allocaEntry(tyStr string, align int) string {
	r := g.newReg()
	if align <= 0 {
		align = 8
	}
	g.entryAllocas = append(g.entryAllocas, fmt.Sprintf("    %s = alloca %s, align %d", r, tyStr, align))
	return r
}

func (g *llvmEmitter) newBlockLabel(prefix string) string {
	l := fmt.Sprintf("%s.%d", prefix, g.nextBlockID)
	g.nextBlockID++
	return l
}

func (g *llvmEmitter) emitLabel(name string) {
	if !g.isTerminated {
		g.indentLine(1, "br label %%%s", name)
	}
	g.line("%s:", name)
	g.currentBlock = name
	g.isTerminated = false
}

// -----------------------------------------------------------------------------
// Top-Level LLVM IR Generation
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitAll() {
	// 1. Target DataLayout and Triple
	g.emitHeader()
	g.emitExternHeaders()

	// 2. Struct and Composite Type Definitions
	g.emitTypeDefs()

	// 3. Collect String Literals from the Program
	g.collectAllStrings()

	// 4. External and Native Runtime Definitions
	g.emitRuntimeAndDeclarations()

	// 5. Top-Level Global Constants Storage
	g.emitGlobals()

	// 6. Lowered MIR Function and Method Bodies
	g.line("; Lowered Lowlight Functions")
	for _, fn := range g.prog.Funcs {
		if g.unitMode == UnitStandalone || g.defineFuncs[fn.CName] {
			g.emitFunction(fn)
		} else {
			g.emitFunctionDecl(fn)
		}
	}

	// 7. Static Function Closure Thunks
	g.emitStaticThunks()

	// 8. Interface VTable Method Wrappers and Instances
	g.emitVTables()

	// 9. Main Entry Point / Test Harness Runner
	if g.unitMode != UnitPackage && (g.prog.MainFunc != nil || g.prog.TestMode) {
		g.emitMain()
	}

	// 10. Synthesized Composite and Collection Runtime Helpers
	g.emitSynthesizedHelpersLLVM()

	// 11. Any Remaining External Declarations
	if len(g.helperDecls) > 0 {
		for _, cd := range g.helperDecls {
			if g.funcSet[cd.fn] {
				continue
			}
			g.line("declare %s @%s(%s)", cd.retTy, cd.fn, cd.paramTypes)
		}
		g.line("")
	}

	// 12. Emit String Literals Pool (at the end so all dynamically interned strings are included)
	g.emitStringPool()

	// 13. Emit Extern Header Metadata (when `extern "<header>"` declarations exist)
	g.emitExternHeadersMetadata()
}

func escapeLLVMMetaString(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '"' || b == '\\' || b < 0x20 || b >= 0x7f {
			fmt.Fprintf(&sb, "\\%02X", b)
		} else {
			sb.WriteByte(b)
		}
	}
	return sb.String()
}

func (g *llvmEmitter) emitExternHeaders() {
	if len(g.prog.ExternHeaders) == 0 {
		return
	}
	g.line("; User Extern Headers")
	for _, hdr := range g.prog.ExternHeaders {
		if strings.HasPrefix(hdr, "./") || strings.HasPrefix(hdr, "../") || strings.HasPrefix(hdr, "/") {
			g.line("; extern %q", hdr)
		} else {
			g.line("; extern <%s>", hdr)
		}
	}
	g.line("")
}

func (g *llvmEmitter) emitExternHeadersMetadata() {
	if len(g.prog.ExternHeaders) == 0 {
		return
	}
	g.line("; Extern Header Metadata")
	refs := make([]string, len(g.prog.ExternHeaders))
	for i := range g.prog.ExternHeaders {
		refs[i] = fmt.Sprintf("!%d", i)
	}
	g.line("!lowlight.extern_headers = !{%s}", strings.Join(refs, ", "))
	for i, hdr := range g.prog.ExternHeaders {
		g.line("!%d = !{!\"%s\"}", i, escapeLLVMMetaString(hdr))
	}
	g.line("")
}

func (g *llvmEmitter) emitHeader() {
	g.line("; Lowlight LLVM IR Module")
	triple := g.prog.TargetTriple
	if triple == "" {
		if runtime.GOARCH == "arm64" {
			if runtime.GOOS == "darwin" {
				triple = "arm64-apple-macosx15.0.0"
			} else {
				triple = "aarch64-unknown-linux-gnu"
			}
		} else {
			if runtime.GOOS == "darwin" {
				triple = "x86_64-apple-macosx15.0.0"
			} else {
				triple = "x86_64-unknown-linux-gnu"
			}
		}
	}

	datalayout := "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-f80:128-n8:16:32:64-S128"
	if strings.HasPrefix(triple, "arm64-apple") || strings.HasPrefix(triple, "aarch64-apple") {
		datalayout = "e-m:o-i64:64-i128:128-n32:64-S128"
	} else if strings.HasPrefix(triple, "aarch64") || strings.HasPrefix(triple, "arm64") {
		datalayout = "e-m:e-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128"
	} else if strings.Contains(triple, "-apple-") {
		datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-f80:128-n8:16:32:64-S128"
	}

	g.line("target datalayout = %q", datalayout)
	g.line("target triple = %q", triple)
	g.line("")
}

// -----------------------------------------------------------------------------
// Type Mapping & Type Definitions
// -----------------------------------------------------------------------------

func (g *llvmEmitter) llvmType(t types.Type) string {
	if t == nil {
		return "void"
	}
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimVoid, types.PrimNever:
			return "void"
		case types.PrimBool:
			return "i1"
		case types.PrimI8, types.PrimU8:
			return "i8"
		case types.PrimI16, types.PrimU16:
			return "i16"
		case types.PrimI32, types.PrimU32:
			return "i32"
		case types.PrimI64, types.PrimU64:
			return "i64"
		case types.PrimF32:
			return "float"
		case types.PrimF64:
			return "double"
		case types.PrimChar:
			return "i32"
		case types.PrimString:
			return "%struct.ll_string"
		case types.PrimArena:
			return "%struct.ll_arena"
		default:
			return "i64"
		}
	case *types.Distinct:
		return g.llvmType(v.Base)
	case *types.Ptr, *types.Ref:
		return "ptr"
	case *types.Enum:
		if !v.IsTagged {
			return g.llvmType(v.Underlying)
		}
		return "%struct." + v.CName
	case *types.Struct:
		if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) == 1 {
			return "%struct.ll_vec_" + v.TypeArgs[0].Mangled()
		}
		return "%struct." + v.CName
	case *types.Vector:
		return "%struct." + CType(v)
	case *types.Optional:
		if v.IsBoxed() {
			return "ptr"
		}
		return "%struct." + CType(v)
	case *types.StackArray:
		return "%struct." + CType(v)
	case *types.Tuple:
		return "%struct." + CType(v)
	case *types.Func:
		if !v.Closure {
			return "ptr" // an `fn` value is its entry function's address
		}
		return "%struct.ll_closure"
	case *types.Interface:
		return "%struct." + v.CName
	default:
		return "i64"
	}
}

func (g *llvmEmitter) emitTypeDefs() {
	g.line("; Core Type Definitions")
	g.raw("%struct.ll_string = type { ptr, i64, i64 }")
	g.raw("%struct.ll_closure = type { ptr, ptr, ptr, ptr }")
	g.raw("%struct.ll_arena = type { ptr, ptr, i64 }")
	g.line("")

	g.line("; Monomorphized Type Definitions")
	emittedOptStructs := make(map[string]bool)
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Enum:
			if v.IsTagged {
				// Compute max payload size in 64-bit words (minimum 2 words = 16 bytes for pointer/int pairs)
				maxPayloadWords := 2
				for _, vr := range v.Variants {
					if vr.Kind == types.VariantTuple {
						w := (g.typeSize(vr.Payload) + 7) / 8
						if w > maxPayloadWords {
							maxPayloadWords = w
						}
					}
				}
				g.line("%%struct.%s = type { i32, [%d x i64] }", v.CName, maxPayloadWords)
			}
		case *types.Struct:
			if strings.HasPrefix(v.CName, "ll_lambda_env_") {
				continue
			}
			if (v.Name == "List" || strings.HasPrefix(v.Name, "List[")) && len(v.TypeArgs) == 1 {
				continue
			}
			var fieldTypes []string
			if len(v.Fields) == 0 {
				fieldTypes = append(fieldTypes, "i8")
			} else {
				for _, f := range v.Fields {
					fieldTypes = append(fieldTypes, g.llvmType(f.Type))
				}
			}
			packedPrefix := ""
			packedSuffix := ""
			if v.Packed {
				packedPrefix = "<{"
				packedSuffix = "}>"
			} else {
				packedPrefix = "{"
				packedSuffix = "}"
			}
			g.line("%%struct.%s = type %s %s %s", v.CName, packedPrefix, strings.Join(fieldTypes, ", "), packedSuffix)
		case *types.Optional:
			if !v.IsBoxed() {
				cn := CType(v)
				if !emittedOptStructs[cn] {
					emittedOptStructs[cn] = true
					g.line("%%struct.%s = type { i1, %s }", cn, g.llvmType(v.Elem))
				}
			}
		case *types.StackArray:
			cn := CType(v)
			g.line("%%struct.%s = type { [%d x %s] }", cn, v.Size, g.llvmType(v.Elem))
			optElem := types.NewOptional(v.Elem)
			if !optElem.IsBoxed() {
				optCn := CType(optElem)
				if !emittedOptStructs[optCn] {
					emittedOptStructs[optCn] = true
					g.line("%%struct.%s = type { i1, %s }", optCn, g.llvmType(v.Elem))
				}
			}
		case *types.Tuple:
			cn := CType(v)
			var fieldTypes []string
			if len(v.Elems) == 0 {
				fieldTypes = append(fieldTypes, "i8")
			} else {
				for _, el := range v.Elems {
					fieldTypes = append(fieldTypes, g.llvmType(el))
				}
			}
			g.line("%%struct.%s = type { %s }", cn, strings.Join(fieldTypes, ", "))
		case *types.Vector:
			cn := CType(v)
			g.line("%%struct.%s = type { ptr, i64, i64 }", cn)
			optElem := types.NewOptional(v.Elem)
			if !optElem.IsBoxed() {
				optCn := CType(optElem)
				if !emittedOptStructs[optCn] {
					emittedOptStructs[optCn] = true
					g.line("%%struct.%s = type { i1, %s }", optCn, g.llvmType(v.Elem))
				}
			}
		case *types.Interface:
			g.line("%%struct.%s = type { ptr, ptr, i1 }", v.CName)
			var vtFields []string
			vtFields = append(vtFields, "ptr", "ptr") // drop_box, clone_box
			for range v.Methods {
				vtFields = append(vtFields, "ptr")
			}
			g.line("%%struct.ll_vt_%s = type { %s }", v.CName, strings.Join(vtFields, ", "))
		}
	}

	if len(g.prog.Checked.Lambdas) > 0 {
		g.line("; Lambda Capture Environments")
		for _, li := range g.prog.Checked.Lambdas {
			if len(li.Captures) == 0 {
				continue
			}
			var fieldTypes []string
			for _, c := range li.Captures {
				fieldTypes = append(fieldTypes, g.llvmType(c.Type))
			}
			g.line("%%struct.%s = type { %s }", li.EnvCName, strings.Join(fieldTypes, ", "))
		}
	}
	g.line("")
}

func (g *llvmEmitter) typeAlign(t types.Type) int {
	if t == nil {
		return 1
	}
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimBool, types.PrimI8, types.PrimU8:
			return 1
		case types.PrimI16, types.PrimU16:
			return 2
		case types.PrimI32, types.PrimU32, types.PrimF32, types.PrimChar:
			return 4
		default:
			return 8
		}
	case *types.Ptr, *types.Ref:
		return 8
	case *types.Distinct:
		return g.typeAlign(v.Base)
	case *types.Enum:
		if !v.IsTagged {
			if v.Underlying != nil {
				return g.typeAlign(v.Underlying)
			}
			return 8
		}
		return 8
	case *types.Struct:
		if v.Packed {
			return 1
		}
		if len(v.Fields) == 0 {
			if realSt, ok := g.structMap[v.CName]; ok && len(realSt.Fields) > 0 {
				v = realSt
			} else if li, ok := g.lambdaEnvMap[v.CName]; ok && len(li.Captures) > 0 {
				maxA := 1
				for _, c := range li.Captures {
					a := g.typeAlign(c.Type)
					if a > maxA {
						maxA = a
					}
				}
				return maxA
			}
		}
		maxA := 1
		for _, f := range v.Fields {
			a := g.typeAlign(f.Type)
			if a > maxA {
				maxA = a
			}
		}
		return maxA
	case *types.Optional:
		if v.IsBoxed() {
			return 8
		}
		a := g.typeAlign(v.Elem)
		if a < 1 {
			a = 1
		}
		return a
	case *types.StackArray:
		return g.typeAlign(v.Elem)
	case *types.Tuple:
		maxA := 1
		for _, el := range v.Elems {
			a := g.typeAlign(el)
			if a > maxA {
				maxA = a
			}
		}
		return maxA
	default:
		return 8
	}
}

func (g *llvmEmitter) typeSize(t types.Type) int {
	if t == nil {
		return 0
	}
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimVoid, types.PrimNever:
			return 0
		case types.PrimBool, types.PrimI8, types.PrimU8:
			return 1
		case types.PrimI16, types.PrimU16:
			return 2
		case types.PrimI32, types.PrimU32, types.PrimF32, types.PrimChar:
			return 4
		case types.PrimI64, types.PrimU64, types.PrimF64:
			return 8
		case types.PrimString, types.PrimArena:
			return 24
		default:
			return 8
		}
	case *types.Ptr, *types.Ref:
		return 8
	case *types.Distinct:
		return g.typeSize(v.Base)
	case *types.Enum:
		if !v.IsTagged {
			if v.Underlying != nil {
				return g.typeSize(v.Underlying)
			}
			return 8
		}
		maxPayloadWords := 2
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple {
				w := (g.typeSize(vr.Payload) + 7) / 8
				if w > maxPayloadWords {
					maxPayloadWords = w
				}
			}
		}
		return 8 + maxPayloadWords*8
	case *types.Struct:
		if len(v.Fields) == 0 {
			if realSt, ok := g.structMap[v.CName]; ok && len(realSt.Fields) > 0 {
				v = realSt
			} else if li, ok := g.lambdaEnvMap[v.CName]; ok && len(li.Captures) > 0 {
				offset := 0
				maxAlign := 1
				for _, c := range li.Captures {
					fAlign := g.typeAlign(c.Type)
					if fAlign > maxAlign {
						maxAlign = fAlign
					}
					offset = (offset + fAlign - 1) & -fAlign
					offset += g.typeSize(c.Type)
				}
				totalSize := (offset + maxAlign - 1) & -maxAlign
				if totalSize == 0 {
					return 1
				}
				return totalSize
			}
		}
		if v.Packed {
			tot := 0
			for _, f := range v.Fields {
				tot += g.typeSize(f.Type)
			}
			if tot == 0 {
				return 1
			}
			return tot
		}
		offset := 0
		maxAlign := 1
		for _, f := range v.Fields {
			fAlign := g.typeAlign(f.Type)
			if fAlign > maxAlign {
				maxAlign = fAlign
			}
			offset = (offset + fAlign - 1) & -fAlign
			offset += g.typeSize(f.Type)
		}
		totalSize := (offset + maxAlign - 1) & -maxAlign
		if totalSize == 0 {
			return 1
		}
		return totalSize
	case *types.Vector:
		return 24
	case *types.Optional:
		if v.IsBoxed() {
			return 8
		}
		elemAlign := g.typeAlign(v.Elem)
		if elemAlign < 1 {
			elemAlign = 1
		}
		offset := elemAlign
		total := offset + g.typeSize(v.Elem)
		return (total + elemAlign - 1) & -elemAlign
	case *types.StackArray:
		return int(v.Size) * g.typeSize(v.Elem)
	case *types.Tuple:
		if len(v.Elems) == 0 {
			return 1
		}
		offset := 0
		maxAlign := 1
		for _, el := range v.Elems {
			fAlign := g.typeAlign(el)
			if fAlign > maxAlign {
				maxAlign = fAlign
			}
			offset = (offset + fAlign - 1) & -fAlign
			offset += g.typeSize(el)
		}
		return (offset + maxAlign - 1) & -maxAlign
	case *types.Func:
		if !v.Closure {
			return 8
		}
		return 32
	case *types.Interface:
		return 24
	default:
		return 8
	}
}

// -----------------------------------------------------------------------------
// String Literals Pool
// -----------------------------------------------------------------------------

func (g *llvmEmitter) internString(s string) *stringLitEntry {
	if entry, ok := g.strings[s]; ok {
		return entry
	}
	id := g.nextStrID
	g.nextStrID++
	sym := fmt.Sprintf("@.str.%d", id)
	_, byteLen := llvmEscapeString(s)
	entry := &stringLitEntry{
		id:     id,
		val:    s,
		symbol: sym,
		length: byteLen,
	}
	g.strings[s] = entry
	g.stringList = append(g.stringList, entry)
	return entry
}

func llvmEscapeString(s string) (escaped string, byteLen int) {
	var sb strings.Builder
	rawBytes := []byte(s)
	byteLen = len(rawBytes) + 1 // +1 for \00 null terminator
	for _, b := range rawBytes {
		if b >= 32 && b < 127 && b != '\\' && b != '"' {
			sb.WriteByte(b)
		} else {
			sb.WriteString(fmt.Sprintf("\\%02X", b))
		}
	}
	sb.WriteString("\\00")
	return sb.String(), byteLen
}

func (g *llvmEmitter) collectAllStrings() {
	// Pre-intern standard strings
	g.internString("")
	g.internString("%s")
	g.internString("No tests found.\n")

	// Collect string literals from all expressions across functions and globals emitted in this unit
	if g.unitMode != UnitPackage {
		for _, gl := range g.prog.Globals {
			g.collectExprStrings(gl.InitExpr)
			for _, instr := range gl.InitInstr {
				g.collectInstrStrings(instr)
			}
		}
	}
	for _, fn := range g.prog.Funcs {
		if g.unitMode != UnitStandalone && !g.defineFuncs[fn.CName] {
			continue
		}
		if fn.TestDesc != "" {
			g.internString(fn.TestDesc)
		}
		for _, instr := range fn.Body {
			g.collectInstrStrings(instr)
		}
	}
}

func (g *llvmEmitter) collectInstrStrings(instr ir.Instr) {
	if instr == nil {
		return
	}
	switch v := instr.(type) {
	case *ir.InstrAssign:
		g.collectExprStrings(v.Dst)
		g.collectExprStrings(v.Expr)
	case *ir.InstrCompoundAssign:
		g.collectExprStrings(v.Dst)
		g.collectExprStrings(v.Expr)
	case *ir.InstrExpr:
		g.collectExprStrings(v.Expr)
	case *ir.InstrIf:
		g.collectExprStrings(v.Cond)
		for _, in := range v.Then {
			g.collectInstrStrings(in)
		}
		for _, in := range v.Else {
			g.collectInstrStrings(in)
		}
	case *ir.InstrWhile:
		g.collectExprStrings(v.Cond)
		for _, in := range v.Body {
			g.collectInstrStrings(in)
		}
	case *ir.InstrBlock:
		for _, in := range v.Body {
			g.collectInstrStrings(in)
		}
	case *ir.InstrReturn:
		g.collectExprStrings(v.Expr)
	}
}

func (g *llvmEmitter) collectExprStrings(e ir.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ir.ExprLitString:
		g.internString(v.Val)
	case *ir.ExprLitCString:
		g.internString(v.Val)
	case *ir.ExprBinary:
		if v.File != "" {
			g.internString(v.File)
		}
		g.collectExprStrings(v.Left)
		g.collectExprStrings(v.Right)
	case *ir.ExprUnary:
		g.collectExprStrings(v.Operand)
	case *ir.ExprCast:
		g.collectExprStrings(v.Val)
	case *ir.ExprField:
		g.collectExprStrings(v.Base)
	case *ir.ExprIndex:
		g.collectExprStrings(v.Base)
		g.collectExprStrings(v.Index)
	case *ir.ExprDeref:
		g.collectExprStrings(v.Ptr)
	case *ir.ExprAddrOf:
		g.collectExprStrings(v.Place)
	case *ir.ExprCall:
		g.collectExprStrings(v.Callee)
		for _, a := range v.Args {
			g.collectExprStrings(a)
		}
	case *ir.ExprConstructStruct:
		for _, f := range v.Fields {
			g.collectExprStrings(f.Value)
		}
	case *ir.ExprConstructEnum:
		g.collectExprStrings(v.Payload)
	case *ir.ExprConstructArray:
		for _, el := range v.Elems {
			g.collectExprStrings(el)
		}
	case *ir.ExprConstructTuple:
		for _, el := range v.Elems {
			g.collectExprStrings(el)
		}
	case *ir.ExprConstructVector:
		for _, el := range v.Elems {
			g.collectExprStrings(el)
		}
	case *ir.ExprConstructClosure:
		g.collectExprStrings(v.EnvPtr)
	case *ir.ExprFnToClosure:
		g.collectExprStrings(v.Fn)
	case *ir.ExprWrapOptional:
		g.collectExprStrings(v.Value)
	case *ir.ExprOptionalHasValue:
		g.collectExprStrings(v.Opt)
	case *ir.ExprOptionalValue:
		g.collectExprStrings(v.Opt)
	case *ir.ExprOptionalValuePtr:
		g.collectExprStrings(v.OptPtr)
	case *ir.ExprSliceView:
		g.collectExprStrings(v.DataPtr)
		g.collectExprStrings(v.Len)
	case *ir.ExprBoxInterface:
		g.collectExprStrings(v.Value)
	case *ir.ExprAlloc:
		g.collectExprStrings(v.Count)
	case *ir.ExprFree:
		g.collectExprStrings(v.Ptr)
	case *ir.ExprRealloc:
		g.collectExprStrings(v.Ptr)
		g.collectExprStrings(v.Count)
	case *ir.ExprCopyBytes:
		g.collectExprStrings(v.Dst)
		g.collectExprStrings(v.Src)
		g.collectExprStrings(v.Count)
	case *ir.ExprToString:
		g.collectExprStrings(v.Val)
	case *ir.ExprDrop:
		g.collectExprStrings(v.Ptr)
	case *ir.ExprCopy:
		g.collectExprStrings(v.Val)
	case *ir.ExprEq:
		g.collectExprStrings(v.Left)
		g.collectExprStrings(v.Right)
	case *ir.ExprCmp:
		g.collectExprStrings(v.Left)
		g.collectExprStrings(v.Right)
	case *ir.ExprHash:
		g.collectExprStrings(v.Val)
	case *ir.ExprVecPush:
		g.collectExprStrings(v.VecPtr)
		g.collectExprStrings(v.Item)
	}
}

func (g *llvmEmitter) emitStringPool() {
	g.line("; Global String Constants")
	for _, entry := range g.stringList {
		esc, byteLen := llvmEscapeString(entry.val)
		g.line("%s = private unnamed_addr constant [%d x i8] c\"%s\", align 1", entry.symbol, byteLen, esc)
	}
	g.line("")
}

// -----------------------------------------------------------------------------
// Declarations & Globals
// -----------------------------------------------------------------------------

func (g *llvmEmitter) declareBuiltin(name, decl string) {
	if g.declaredFns[name] {
		return
	}
	g.line("%s", decl)
	g.declaredFns[name] = true
}

func (g *llvmEmitter) emitGlobals() {
	if len(g.prog.Globals) == 0 {
		return
	}
	g.line("; Top-Level Constants")
	for _, gl := range g.prog.Globals {
		switch g.unitMode {
		case UnitPackage:
			g.line("@%s = external global %s, align 8", gl.CName, g.llvmType(gl.Type))
		case UnitLinkMain:
			g.line("@%s = global %s zeroinitializer, align 8", gl.CName, g.llvmType(gl.Type))
		default:
			g.line("@%s = internal global %s zeroinitializer, align 8", gl.CName, g.llvmType(gl.Type))
		}
	}
	g.line("")
}

func (g *llvmEmitter) emitFunctionDecl(fn *ir.Function) {
	var paramTypes []string
	if fn.IsLambda {
		paramTypes = append(paramTypes, "ptr")
	}
	for _, p := range fn.Params {
		pTy := g.llvmType(p.Type)
		if p.IsMutPtr {
			pTy = "ptr"
		}
		paramTypes = append(paramTypes, pTy)
	}
	retTy := g.llvmType(fn.RetType)
	g.line("declare %s @%s(%s)", retTy, fn.CName, strings.Join(paramTypes, ", "))
	g.declaredFns[fn.CName] = true
}

// -----------------------------------------------------------------------------
// Functions & Instructions
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitFunction(fn *ir.Function) {
	g.localSlots = make(map[*ir.Local]string)
	g.movedSlots = make(map[string]string)
	g.nextRegID = 0
	g.isTerminated = false
	g.entryAllocas = nil

	var paramDecls []string
	if fn.IsLambda {
		paramDecls = append(paramDecls, "ptr %_ll_env.param")
	}
	for _, p := range fn.Params {
		pTy := g.llvmType(p.Type)
		if p.IsMutPtr {
			pTy = "ptr"
		}
		paramDecls = append(paramDecls, fmt.Sprintf("%s %%%s.param", pTy, p.CName))
	}
	retTy := g.llvmType(fn.RetType)

	var headerSB strings.Builder
	headerSB.WriteString(fmt.Sprintf("define %s @%s(%s) {\n", retTy, fn.CName, strings.Join(paramDecls, ", ")))
	headerSB.WriteString("entry:\n")

	var paramInitSB strings.Builder
	if fn.IsLambda {
		headerSB.WriteString("    %_ll_env.slot = alloca ptr, align 8\n")
		paramInitSB.WriteString("    store ptr %_ll_env.param, ptr %_ll_env.slot, align 8\n")
	}

	// 1. Allocate stack slots for all parameters
	for _, p := range fn.Params {
		slot := fmt.Sprintf("%%%s.slot", p.CName)
		g.localSlots[p] = slot
		pTy := g.llvmType(p.Type)
		if p.IsMutPtr {
			pTy = "ptr"
			headerSB.WriteString(fmt.Sprintf("    %s = alloca %s, align 8\n", slot, pTy))
			paramInitSB.WriteString(fmt.Sprintf("    store %s %%%s.param, ptr %s, align 8\n", pTy, p.CName, slot))
		} else {
			prim := types.UnderlyingPrimitive(p.Type)
			if prim != nil && prim.Kind == types.PrimBool {
				zReg := g.newReg()
				headerSB.WriteString(fmt.Sprintf("    %s = alloca i8, align 1\n", slot))
				paramInitSB.WriteString(fmt.Sprintf("    %s = zext i1 %%%s.param to i8\n", zReg, p.CName))
				paramInitSB.WriteString(fmt.Sprintf("    store i8 %s, ptr %s, align 1\n", zReg, slot))
			} else {
				align := g.typeAlign(p.Type)
				headerSB.WriteString(fmt.Sprintf("    %s = alloca %s, align %d\n", slot, pTy, align))
				paramInitSB.WriteString(fmt.Sprintf("    store %s %%%s.param, ptr %s, align %d\n", pTy, p.CName, slot, align))
			}
		}
		if p.MovedFlag != "" {
			flagSlot := fmt.Sprintf("%%%s.slot", p.MovedFlag)
			g.movedSlots[p.MovedFlag] = flagSlot
			if p.MovedLocal != nil {
				g.localSlots[p.MovedLocal] = flagSlot
			}
			headerSB.WriteString(fmt.Sprintf("    %s = alloca i1, align 1\n", flagSlot))
			paramInitSB.WriteString(fmt.Sprintf("    store i1 false, ptr %s, align 1\n", flagSlot))
		}
	}

	// 2. Allocate stack slots for all local variables
	for _, loc := range fn.Locals {
		slot := fmt.Sprintf("%%%s.slot", loc.CName)
		g.localSlots[loc] = slot
		locTy := g.llvmType(loc.Type)
		if loc.IsMutPtr {
			locTy = "ptr"
		}
		headerSB.WriteString(fmt.Sprintf("    %s = alloca %s, align 8\n", slot, locTy))
		if !loc.NoZeroInit {
			paramInitSB.WriteString(fmt.Sprintf("    store %s zeroinitializer, ptr %s, align 8\n", locTy, slot))
		}

		if loc.MovedFlag != "" {
			flagSlot := fmt.Sprintf("%%%s.slot", loc.MovedFlag)
			g.movedSlots[loc.MovedFlag] = flagSlot
			if loc.MovedLocal != nil {
				g.localSlots[loc.MovedLocal] = flagSlot
			}
			headerSB.WriteString(fmt.Sprintf("    %s = alloca i1, align 1\n", flagSlot))
			paramInitSB.WriteString(fmt.Sprintf("    store i1 true, ptr %s, align 1\n", flagSlot))
		}
	}

	savedSB := g.sb
	g.sb = strings.Builder{}
	g.currentBlock = "entry"

	// 3. Emit instruction sequence
	for _, instr := range fn.Body {
		g.emitInstr(instr)
	}

	// 4. Ensure block termination
	if !g.isTerminated {
		if fn.RetType == nil || fn.RetType.Equals(types.TypeVoid) {
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "ret %s zeroinitializer", retTy)
		}
		g.isTerminated = true
	}

	bodyStr := g.sb.String()
	g.sb = savedSB

	g.sb.WriteString(headerSB.String())
	for _, alloc := range g.entryAllocas {
		g.sb.WriteString(alloc)
		g.sb.WriteByte('\n')
	}
	g.sb.WriteString(paramInitSB.String())
	g.sb.WriteString(bodyStr)
	g.line("}")
	g.line("")

	if (strings.HasSuffix(fn.CName, "_to_string") || strings.Contains(fn.CName, "_to_string") || strings.HasSuffix(fn.Name, ".to_string")) && len(fn.Params) == 1 && fn.RetType != nil && fn.RetType.Equals(types.TypeString) {
		pTy := g.llvmType(fn.Params[0].Type)
		align := g.typeAlign(fn.Params[0].Type)
		g.line("define void @%s_p(ptr %%res, ptr %%self) {", fn.CName)
		g.line("entry:")
		g.indentLine(1, "%%val = load %s, ptr %%self, align %d", pTy, align)
		g.indentLine(1, "%%ret = call %%struct.ll_string @%s(%s %%val)", fn.CName, pTy)
		g.indentRaw(1, "store %struct.ll_string %ret, ptr %res, align 8")
		g.indentRaw(1, "ret void")
		g.line("}")
		g.line("")
	}
}

func (g *llvmEmitter) emitInstr(instr ir.Instr) {
	if g.isTerminated {
		// Instructions after a terminator are unreachable unless preceded by a label
		if _, isLabel := instr.(*ir.InstrLabel); !isLabel {
			return
		}
	}

	switch v := instr.(type) {
	case *ir.InstrAssign:
		if loc, ok := v.Dst.(*ir.ExprLocal); ok && loc.Local.IsMutPtr {
			isAddr := false
			if _, isAddrOf := v.Expr.(*ir.ExprAddrOf); isAddrOf {
				isAddr = true
			} else if u, isUnary := v.Expr.(*ir.ExprUnary); isUnary && u.Op == token.Amp {
				isAddr = true
			} else if v.Expr.Type() != nil {
				if _, isPtr := v.Expr.Type().(*types.Ptr); isPtr {
					isAddr = true
				} else if _, isRef := v.Expr.Type().(*types.Ref); isRef {
					isAddr = true
				}
			}
			if isAddr {
				val := g.emitExpr(v.Expr)
				slot := g.localSlots[loc.Local]
				if slot == "" {
					slot = fmt.Sprintf("%%%s.slot", loc.Local.CName)
				}
				g.indentLine(1, "store ptr %s, ptr %s, align 8", val, slot)
				return
			}
		}
		val := g.emitExpr(v.Expr)
		addr := g.emitLValueAddr(v.Dst)
		dstTy := v.Dst.Type()
		prim := types.UnderlyingPrimitive(dstTy)
		if prim != nil && prim.Kind == types.PrimBool {
			zReg := g.newReg()
			g.indentLine(1, "%s = zext i1 %s to i8", zReg, val)
			g.indentLine(1, "store i8 %s, ptr %s, align 1", zReg, addr)
		} else {
			align := g.typeAlign(dstTy)
			g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(dstTy), val, addr, align)
		}

	case *ir.InstrCompoundAssign:
		addr := g.emitLValueAddr(v.Dst)
		currVal := g.emitLoad(addr, v.Dst.Type())
		rhsVal := g.emitExpr(v.Expr)
		resVal := g.emitBinaryOp(v.Op, currVal, rhsVal, v.Dst.Type(), "", 0, v.Expr.Type())
		align := g.typeAlign(v.Dst.Type())
		g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(v.Dst.Type()), resVal, addr, align)

	case *ir.InstrExpr:
		g.emitExpr(v.Expr)

	case *ir.InstrZero:
		var slot string
		if v.Var != nil {
			slot = g.localSlots[v.Var]
		}
		if slot != "" {
			sz := g.typeSize(v.Type)
			if sz <= 8 {
				g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.Type), slot)
			} else {
				g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %s, i8 0, i64 %d, i1 false)", slot, sz)
			}
		}
		if v.MovedFlag != "" {
			if fSlot, ok := g.movedSlots[v.MovedFlag]; ok {
				g.indentLine(1, "store i1 true, ptr %s, align 1", fSlot)
			}
		}

	case *ir.InstrDrop:
		if v.Type != nil && v.Type.IsPlain() {
			return
		}
		if v.MovedFlag != "" {
			if fSlot, ok := g.movedSlots[v.MovedFlag]; ok {
				movedReg := g.newReg()
				g.indentLine(1, "%s = load i1, ptr %s, align 1", movedReg, fSlot)
				dropBlock := g.newBlockLabel("drop.act")
				contBlock := g.newBlockLabel("drop.cont")
				// If NOT moved (!moved, i.e. moved == false), perform drop
				g.indentLine(1, "br i1 %s, label %%%s, label %%%s", movedReg, contBlock, dropBlock)

				g.emitLabel(dropBlock)
				g.indentLine(1, "store i1 true, ptr %s, align 1", fSlot)
				if v.Var != nil {
					slot := g.localSlots[v.Var]
					g.emitTypeDrop(slot, v.Type)
				}
				g.indentLine(1, "br label %%%s", contBlock)

				g.emitLabel(contBlock)
				return
			}
		}
		if v.Var != nil {
			slot := g.localSlots[v.Var]
			g.emitTypeDrop(slot, v.Type)
		}

	case *ir.InstrIf:
		condVal := g.emitExpr(v.Cond)
		thenBlock := g.newBlockLabel("then")
		elseBlock := g.newBlockLabel("else")
		contBlock := g.newBlockLabel("if.cont")

		g.indentLine(1, "br i1 %s, label %%%s, label %%%s", condVal, thenBlock, elseBlock)
		g.isTerminated = true

		// Then branch
		g.emitLabel(thenBlock)
		for _, in := range v.Then {
			g.emitInstr(in)
		}
		if !g.isTerminated {
			g.indentLine(1, "br label %%%s", contBlock)
			g.isTerminated = true
		}

		// Else branch
		g.emitLabel(elseBlock)
		for _, in := range v.Else {
			g.emitInstr(in)
		}
		if !g.isTerminated {
			g.indentLine(1, "br label %%%s", contBlock)
			g.isTerminated = true
		}

		g.emitLabel(contBlock)

	case *ir.InstrWhile:
		condBlock := g.newBlockLabel("while.cond")
		bodyBlock := g.newBlockLabel("while.body")
		endBlock := g.newBlockLabel("while.end")

		if !g.isTerminated {
			g.indentLine(1, "br label %%%s", condBlock)
			g.isTerminated = true
		}

		g.emitLabel(condBlock)
		condVal := g.emitExpr(v.Cond)
		g.indentLine(1, "br i1 %s, label %%%s, label %%%s", condVal, bodyBlock, endBlock)
		g.isTerminated = true

		g.emitLabel(bodyBlock)
		for _, in := range v.Body {
			g.emitInstr(in)
		}
		if !g.isTerminated {
			g.indentLine(1, "br label %%%s", condBlock)
			g.isTerminated = true
		}

		g.emitLabel(endBlock)

	case *ir.InstrBlock:
		for _, in := range v.Body {
			g.emitInstr(in)
		}

	case *ir.InstrLabel:
		g.emitLabel(v.Name)

	case *ir.InstrGoto:
		g.indentLine(1, "br label %%%s", v.Label)
		g.isTerminated = true

	case *ir.InstrReturn:
		if v.Expr != nil {
			val := g.emitExpr(v.Expr)
			g.indentLine(1, "ret %s %s", g.llvmType(v.Expr.Type()), val)
		} else {
			g.indentLine(1, "ret void")
		}
		g.isTerminated = true
	}
}

func (g *llvmEmitter) emitTypeDrop(ptrSlot string, t types.Type) {
	if t == nil || t.IsPlain() {
		return
	}
	switch v := t.(type) {
	case *types.Primitive:
		if v.Kind == types.PrimString {
			g.indentLine(1, "call void @ll_str_free_p(ptr %s)", ptrSlot)
		} else if v.Kind == types.PrimArena {
			g.declareHelper("ll_drop__Arena", "void", "ptr")
			g.indentLine(1, "call void @ll_drop__Arena(ptr %s)", ptrSlot)
		}
	case *types.Vector, *types.Struct, *types.Distinct, *types.Interface, *types.StackArray, *types.Optional, *types.Func, *types.Tuple:
		if f, isFn := v.(*types.Func); isFn && !f.Closure {
			return // an `fn` owns nothing
		}
		fn := ir.DropFnName(v)
		g.declareHelper(fn, "void", "ptr")
		g.indentLine(1, "call void @%s(ptr %s)", fn, ptrSlot)
	case *types.Enum:
		if v.IsTagged {
			fn := ir.DropFnName(v)
			g.declareHelper(fn, "void", "ptr")
			g.indentLine(1, "call void @%s(ptr %s)", fn, ptrSlot)
		}
	}
}

func (g *llvmEmitter) emitCopyVal(destAddr, srcAddr string, t types.Type) {
	if t == nil || t.IsPlain() {
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", r, g.llvmType(t), srcAddr, g.typeAlign(t))
		g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(t), r, destAddr, g.typeAlign(t))
		return
	}
	if t.Mangled() == "string" {
		g.declareHelper("ll_copy_p__string", "void", "ptr, ptr")
		g.indentLine(1, "call void @ll_copy_p__string(ptr %s, ptr %s)", destAddr, srcAddr)
		return
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "copy"); custom != nil {
		arg := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", arg, g.llvmType(t), srcAddr, g.typeAlign(t))
		copied := g.newReg()
		g.indentLine(1, "%s = call %s @%s(%s %s)", copied, g.llvmType(t), custom.CName, g.llvmType(t), arg)
		g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(t), copied, destAddr, g.typeAlign(t))
		return
	}
	if opt, ok := t.(*types.Optional); ok {
		if opt.IsBoxed() {
			fn := "ll_copy_p__" + opt.Mangled()
			g.declareHelper(fn, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, destAddr, srcAddr)
			return
		}
		pHasSrc := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", pHasSrc, g.llvmType(opt), srcAddr)
		hasVal := g.newReg()
		g.indentLine(1, "%s = load i1, ptr %s, align 1", hasVal, pHasSrc)
		pHasDest := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", pHasDest, g.llvmType(opt), destAddr)
		g.indentLine(1, "store i1 %s, ptr %s, align 1", hasVal, pHasDest)
		lSome := g.newBlockLabel("opt.copy.some")
		lNone := g.newBlockLabel("opt.copy.none")
		lCont := g.newBlockLabel("opt.copy.cont")
		g.indentLine(1, "br i1 %s, label %%%s, label %%%s", hasVal, lSome, lNone)
		g.isTerminated = true
		g.emitLabel(lSome)
		pValSrc := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pValSrc, g.llvmType(opt), srcAddr)
		pValDest := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pValDest, g.llvmType(opt), destAddr)
		g.emitCopyVal(pValDest, pValSrc, opt.Elem)
		g.indentLine(1, "br label %%%s", lCont)
		g.isTerminated = true
		g.emitLabel(lNone)
		g.indentLine(1, "br label %%%s", lCont)
		g.isTerminated = true
		g.emitLabel(lCont)
		return
	}
	if st, ok := t.(*types.Struct); ok {
		for i, f := range st.Fields {
			srcField := g.newReg()
			destField := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", srcField, g.llvmType(st), srcAddr, i)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", destField, g.llvmType(st), destAddr, i)
			g.emitCopyVal(destField, srcField, f.Type)
		}
		return
	}
	if tup, ok := t.(*types.Tuple); ok {
		for i, el := range tup.Elems {
			srcField := g.newReg()
			destField := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", srcField, g.llvmType(tup), srcAddr, i)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", destField, g.llvmType(tup), destAddr, i)
			g.emitCopyVal(destField, srcField, el)
		}
		return
	}
	if d, ok := t.(*types.Distinct); ok {
		g.emitCopyVal(destAddr, srcAddr, d.Base)
		return
	}
	fn := "ll_copy_p__" + t.Mangled()
	g.declareHelper(fn, "void", "ptr, ptr")
	g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, destAddr, srcAddr)
}

func (g *llvmEmitter) declareHelper(fn, retTy, paramTypes string) {
	if g.funcSet[fn] || g.declaredFns[fn] {
		return
	}
	g.declaredFns[fn] = true
	g.helperDecls = append(g.helperDecls, helperDecl{
		fn:         fn,
		retTy:      retTy,
		paramTypes: paramTypes,
	})
}

// -----------------------------------------------------------------------------
// Expressions & LValues
// -----------------------------------------------------------------------------

func (g *llvmEmitter) resolveFieldIdx(baseTy types.Type, fieldName string, explicitIdx int) int {
	if p, ok := baseTy.(*types.Ptr); ok {
		baseTy = p.Elem
	} else if r, ok := baseTy.(*types.Ref); ok {
		baseTy = r.Elem
	}
	switch t := baseTy.(type) {
	case *types.Struct:
		for i, f := range t.Fields {
			if f.Name == fieldName || f.CName == fieldName {
				return i
			}
		}
		if realSt, ok := g.structMap[t.CName]; ok {
			for i, f := range realSt.Fields {
				if f.Name == fieldName || f.CName == fieldName {
					return i
				}
			}
		}
		if li, ok := g.lambdaEnvMap[t.CName]; ok {
			for i, c := range li.Captures {
				if c.CName == fieldName || c.OuterSym.Name == fieldName || c.OuterSym.CName == fieldName {
					return i
				}
			}
		}
		for _, progSt := range g.prog.Structs {
			if progSt.CName == t.CName {
				for i, f := range progSt.Fields {
					if f.Name == fieldName || f.CName == fieldName {
						return i
					}
				}
			}
		}
	case *types.StackArray:
		if fieldName == "data" {
			return 0
		}
	case *types.Tuple:
		if explicitIdx >= 0 {
			return explicitIdx
		}
		if strings.HasPrefix(fieldName, "_") {
			if n, err := strconv.Atoi(fieldName[1:]); err == nil {
				return n
			}
		}
		if n, err := strconv.Atoi(fieldName); err == nil {
			return n
		}
	case *types.Vector:
		switch fieldName {
		case "data":
			return 0
		case "len":
			return 1
		case "cap":
			return 2
		}
	case *types.Primitive:
		if t.Kind == types.PrimString {
			switch fieldName {
			case "data":
				return 0
			case "len":
				return 1
			case "cap":
				return 2
			}
		}
	case *types.Optional:
		switch fieldName {
		case "has_value":
			return 0
		case "value":
			return 1
		}
	case *types.Interface:
		switch fieldName {
		case "data":
			return 0
		case "vtable":
			return 1
		case "owned":
			return 2
		}
	case *types.Func:
		switch fieldName {
		case "fn":
			return 0
		case "env":
			return 1
		case "copy_env":
			return 2
		case "drop_env":
			return 3
		}
	case *types.Enum:
		if fieldName == "tag" {
			return 0
		}
		return 1
	}
	return explicitIdx
}

func (g *llvmEmitter) resolveVariantFieldOffset(v *ir.ExprField) (int, bool) {
	parent, ok := v.Base.(*ir.ExprField)
	if !ok {
		return 0, false
	}
	grandparent, ok := parent.Base.(*ir.ExprField)
	if !ok || grandparent.FieldName != "data" {
		return 0, false
	}
	enTy := grandparent.Base.Type()
	if enTy == nil {
		return 0, false
	}
	if p, isPtr := enTy.(*types.Ptr); isPtr {
		enTy = p.Elem
	} else if r, isRef := enTy.(*types.Ref); isRef {
		enTy = r.Elem
	}
	en, ok := enTy.(*types.Enum)
	if !ok {
		return 0, false
	}
	for _, vr := range en.Variants {
		if (vr.CName == parent.FieldName || vr.Name == parent.FieldName) && vr.Kind == types.VariantTuple {
			return 0, true
		}
	}
	return 0, false
}

func (g *llvmEmitter) emitAsPtr(e ir.Expr) string {
	if _, isPtr := e.Type().(*types.Ptr); isPtr {
		return g.emitExpr(e)
	}
	return g.emitLValueAddr(e)
}

func (g *llvmEmitter) emitLValueAddr(e ir.Expr) string {
	switch v := e.(type) {
	case *ir.ExprLocal:
		slot := g.localSlots[v.Local]
		if slot == "" && v.Local != nil {
			slot = fmt.Sprintf("%%%s.slot", v.Local.CName)
		}
		if v.Local.IsMutPtr {
			ptrReg := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", ptrReg, slot)
			return ptrReg
		}
		return slot

	case *ir.ExprGlobal:
		sym := v.Global.CName
		if sym == "" {
			sym = v.Global.Name
		}
		return "@" + sym

	case *ir.ExprField:
		if byteOffset, ok := g.resolveVariantFieldOffset(v); ok {
			baseAddr := g.emitLValueAddr(v.Base)
			fReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds i8, ptr %s, i64 %d", fReg, baseAddr, byteOffset)
			return fReg
		}
		baseTy := v.Base.Type()
		var baseAddr string
		if p, ok := baseTy.(*types.Ptr); ok {
			baseTy = p.Elem
			baseAddr = g.emitExpr(v.Base)
		} else if r, ok := baseTy.(*types.Ref); ok {
			baseTy = r.Elem
			baseAddr = g.emitExpr(v.Base)
		} else if v.IsPtr {
			baseAddr = g.emitExpr(v.Base)
			// A boxed `?Struct` (self-referential optional) is lowered as a nullable pointer to
			// the struct, so field GEPs must index the pointee struct, not the `ptr` optional.
			if opt, isOpt := baseTy.(*types.Optional); isOpt && opt.IsBoxed() {
				baseTy = opt.Elem
			}
		} else {
			baseAddr = g.emitLValueAddr(v.Base)
		}
		if baseTy == nil || baseTy.Equals(types.TypeVoid) {
			idx := v.FieldIdx
			if idx <= 0 {
				return baseAddr
			}
			fReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds i64, ptr %s, i64 %d", fReg, baseAddr, idx)
			return fReg
		}
		fieldIdx := g.resolveFieldIdx(baseTy, v.FieldName, v.FieldIdx)
		fReg := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
			fReg, g.llvmType(baseTy), baseAddr, fieldIdx)
		return fReg

	case *ir.ExprIndex:
		baseTy := v.Base.Type()
		if arrTy, isArr := baseTy.(*types.StackArray); isArr {
			baseAddr := g.emitLValueAddr(v.Base)
			idxVal := g.emitExpr(v.Index)
			elReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0, i64 %s",
				elReg, g.llvmType(arrTy), baseAddr, idxVal)
			return elReg
		}
		if vecTy, isVec := baseTy.(*types.Vector); isVec {
			var baseAddr string
			if _, isPtr := baseTy.(*types.Ptr); isPtr {
				baseAddr = g.emitExpr(v.Base)
			} else {
				baseAddr = g.emitLValueAddr(v.Base)
			}
			dataPtrReg := g.newReg()
			dataReg := g.newReg()
			idxVal := g.emitExpr(v.Index)
			elReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0",
				dataPtrReg, g.llvmType(vecTy), baseAddr)
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", dataReg, dataPtrReg)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s",
				elReg, g.llvmType(vecTy.Elem), dataReg, idxVal)
			return elReg
		}
		if p, isPtr := baseTy.(*types.Ptr); isPtr {
			baseVal := g.emitExpr(v.Base)
			idxVal := g.emitExpr(v.Index)
			elReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s",
				elReg, g.llvmType(p.Elem), baseVal, idxVal)
			return elReg
		}
		if r, isRef := baseTy.(*types.Ref); isRef {
			baseVal := g.emitExpr(v.Base)
			idxVal := g.emitExpr(v.Index)
			elReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s",
				elReg, g.llvmType(r.Elem), baseVal, idxVal)
			return elReg
		}
		baseAddr := g.emitLValueAddr(v.Base)
		return baseAddr

	case *ir.ExprUnary:
		if v.Op == token.Amp {
			return g.emitLValueAddr(v.Operand)
		}
		val := g.emitExpr(e)
		tmpSlot := g.allocaEntry(g.llvmType(e.Type()), 8)
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(e.Type()), val, tmpSlot)
		return tmpSlot

	case *ir.ExprDeref:
		return g.emitExpr(v.Ptr)

	default:
		// For non-memory places, allocate a temporary stack slot
		val := g.emitExpr(e)
		tmpSlot := g.allocaEntry(g.llvmType(e.Type()), 8)
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(e.Type()), val, tmpSlot)
		return tmpSlot
	}
}

func (g *llvmEmitter) emitLoad(addr string, t types.Type) string {
	r := g.newReg()
	g.indentLine(1, "%s = load %s, ptr %s, align %d", r, g.llvmType(t), addr, g.typeAlign(t))
	return r
}

func (g *llvmEmitter) emitExpr(e ir.Expr) string {
	if e == nil {
		return "void"
	}
	switch v := e.(type) {
	case *ir.ExprLitInt:
		if v.Negated {
			return fmt.Sprintf("-%d", v.Val)
		}
		return fmt.Sprintf("%d", v.Val)

	case *ir.ExprLitFloat:
		if math.IsNaN(v.Val) {
			return "0x7FF8000000000000"
		} else if math.IsInf(v.Val, 1) {
			return "0x7FF0000000000000"
		} else if math.IsInf(v.Val, -1) {
			return "0xFFF0000000000000"
		}
		s := strconv.FormatFloat(v.Val, 'g', -1, 64)
		if !strings.Contains(s, ".") {
			if idx := strings.IndexAny(s, "eE"); idx != -1 {
				s = s[:idx] + ".0" + s[idx:]
			} else {
				s += ".0"
			}
		}
		return s

	case *ir.ExprLitBool:
		if v.Val {
			return "true"
		}
		return "false"

	case *ir.ExprLitChar:
		return fmt.Sprintf("%d", int32(v.Val))

	case *ir.ExprLitString:
		entry := g.internString(v.Val)
		sSlot := g.allocaEntry("%struct.ll_string", 8)
		p0 := g.newReg()
		p1 := g.newReg()
		p2 := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 0", p0, sSlot)
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 1", p1, sSlot)
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 2", p2, sSlot)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", entry.symbol, p0)
		g.indentLine(1, "store i64 %d, ptr %s, align 8", len([]byte(v.Val)), p1)
		g.indentLine(1, "store i64 0, ptr %s, align 8", p2)
		r := g.newReg()
		g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, sSlot)
		return r

	case *ir.ExprLitCString:
		entry := g.internString(v.Val)
		return entry.symbol

	case *ir.ExprLitNone:
		if v.OptType != nil && v.OptType.IsBoxed() {
			return "null"
		}
		optSlot := g.allocaEntry(g.llvmType(v.Type()), 8)
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.Type()), optSlot)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.Type()), optSlot)
		return r

	case *ir.ExprLitZero:
		tyStr := g.llvmType(v.Typ)
		if tyStr == "ptr" {
			return "null"
		}
		if tyStr == "i1" {
			return "false"
		}
		if strings.HasPrefix(tyStr, "i") || tyStr == "float" || tyStr == "double" {
			return "0"
		}
		zSlot := g.allocaEntry(tyStr, 8)
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", tyStr, zSlot)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, tyStr, zSlot)
		return r

	case *ir.ExprLocal:
		slot := g.localSlots[v.Local]
		if slot == "" && v.Local != nil {
			slot = fmt.Sprintf("%%%s.slot", v.Local.CName)
		}
		if v.Local.IsMutPtr {
			ptrReg := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", ptrReg, slot)
			return ptrReg
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", r, g.llvmType(v.Local.Type), slot, g.typeAlign(v.Local.Type))
		return r

	case *ir.ExprGlobal:
		sym := v.Global.CName
		if sym == "" {
			sym = v.Global.Name
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr @%s, align 8", r, g.llvmType(v.Global.Type), sym)
		return r

	case *ir.ExprFuncRef:
		return "@" + v.Name

	case *ir.ExprField:
		fReg := g.emitLValueAddr(v)
		fTy := v.Typ
		if v.Base.Type() != nil {
			baseTy := v.Base.Type()
			if p, ok := baseTy.(*types.Ptr); ok {
				baseTy = p.Elem
			} else if r, ok := baseTy.(*types.Ref); ok {
				baseTy = r.Elem
			}
			if _, isArr := baseTy.(*types.StackArray); isArr && v.FieldName == "data" {
				return fReg
			}
			if _, isEn := baseTy.(*types.Enum); isEn && v.FieldName == "tag" {
				f32 := g.newReg()
				g.indentLine(1, "%s = load i32, ptr %s, align 4", f32, fReg)
				r := g.newReg()
				g.indentLine(1, "%s = zext i32 %s to i64", r, f32)
				return r
			}
			if _, isFunc := v.Base.Type().(*types.Func); isFunc {
				fTy = &types.Ptr{Elem: types.TypeVoid}
			}
			if _, isIfc := v.Base.Type().(*types.Interface); isIfc {
				if v.FieldName == "data" || v.FieldName == "vtable" {
					fTy = &types.Ptr{Elem: types.TypeVoid}
				}
			}
		}
		return g.emitLoad(fReg, fTy)

	case *ir.ExprIndex:
		baseTy := v.Base.Type()
		if strTy, isStr := baseTy.(*types.Primitive); isStr && strTy.Kind == types.PrimString {
			baseAddr := g.emitLValueAddr(v.Base)
			dataPtrReg := g.newReg()
			dataReg := g.newReg()
			idxVal := g.emitExpr(v.Index)
			elReg := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 0",
				dataPtrReg, baseAddr)
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", dataReg, dataPtrReg)
			g.indentLine(1, "%s = getelementptr inbounds i8, ptr %s, i64 %s",
				elReg, dataReg, idxVal)
			r := g.newReg()
			g.indentLine(1, "%s = load i8, ptr %s, align 1", r, elReg)
			return r
		}
		elAddr := g.emitLValueAddr(v)
		return g.emitLoad(elAddr, v.Typ)

	case *ir.ExprDeref:
		ptrVal := g.emitExpr(v.Ptr)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", r, g.llvmType(v.Typ), ptrVal, g.typeAlign(v.Typ))
		return r

	case *ir.ExprAddrOf:
		return g.emitLValueAddr(v.Place)

	case *ir.ExprBinary:
		if v.Left.Type() != nil && v.Left.Type().Equals(types.TypeString) {
			leftAddr := g.emitLValueAddr(v.Left)
			rightAddr := g.emitLValueAddr(v.Right)
			switch v.Op {
			case token.Eq:
				r := g.newReg()
				g.indentLine(1, "%s = call i1 @ll_str_eq_p(ptr %s, ptr %s)", r, leftAddr, rightAddr)
				return r
			case token.Neq:
				eq := g.newReg()
				g.indentLine(1, "%s = call i1 @ll_str_eq_p(ptr %s, ptr %s)", eq, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = xor i1 %s, true", r, eq)
				return r
			case token.Lt:
				cmp := g.newReg()
				g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = icmp slt i64 %s, 0", r, cmp)
				return r
			case token.Lte:
				cmp := g.newReg()
				g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = icmp sle i64 %s, 0", r, cmp)
				return r
			case token.Gt:
				cmp := g.newReg()
				g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = icmp sgt i64 %s, 0", r, cmp)
				return r
			case token.Gte:
				cmp := g.newReg()
				g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = icmp sge i64 %s, 0", r, cmp)
				return r
			}
		}
		if v.Left.Type() != nil && !g.isScalar(v.Left.Type()) && !g.isFloat(v.Left.Type()) && (v.Op == token.Eq || v.Op == token.Neq) {
			leftAddr := g.emitLValueAddr(v.Left)
			rightAddr := g.emitLValueAddr(v.Right)
			fn := "ll_eq_p__" + v.Left.Type().Mangled()
			g.declareHelper(fn, "i1", "ptr, ptr")
			eqReg := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", eqReg, fn, leftAddr, rightAddr)
			if v.Op == token.Eq {
				return eqReg
			}
			r := g.newReg()
			g.indentLine(1, "%s = xor i1 %s, true", r, eqReg)
			return r
		}
		leftVal := g.emitExpr(v.Left)
		rightVal := g.emitExpr(v.Right)
		return g.emitBinaryOp(v.Op, leftVal, rightVal, v.Left.Type(), v.File, v.Line, v.Right.Type())

	case *ir.ExprUnary:
		if v.Op == token.Amp {
			return g.emitLValueAddr(v.Operand)
		}
		val := g.emitExpr(v.Operand)
		return g.emitUnaryOp(v.Op, val, v.Operand.Type())

	case *ir.ExprCast:
		from := v.From
		if from == nil && v.Val != nil {
			from = v.Val.Type()
		}
		val := g.emitExpr(v.Val)
		return g.emitCastOp(val, from, v.To)

	case *ir.ExprCall:
		return g.emitCall(v)

	case *ir.ExprConstructStruct:
		sSlot := g.allocaEntry(g.llvmType(v.StructType), 8)
		for _, f := range v.Fields {
			fVal := g.emitExpr(f.Value)
			fIdx := f.Index
			if f.Name != "" {
				fIdx = g.resolveFieldIdx(v.StructType, f.Name, f.Index)
			}
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
				fp, g.llvmType(v.StructType), sSlot, fIdx)
			g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(f.Value.Type()), fVal, fp)
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.StructType), sSlot)
		return r

	case *ir.ExprConstructEnum:
		if !v.EnumType.IsTagged {
			return fmt.Sprintf("%d", v.Variant.Value)
		}
		eSlot := g.allocaEntry(g.llvmType(v.EnumType), 8)
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.EnumType), eSlot)
		tagPtr := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0",
			tagPtr, g.llvmType(v.EnumType), eSlot)
		g.indentLine(1, "store i32 %d, ptr %s, align 4", v.Variant.Value, tagPtr)
		if v.Payload != nil {
			pVal := g.emitExpr(v.Payload)
			dataPtr := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1",
				dataPtr, g.llvmType(v.EnumType), eSlot)
			g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(v.Payload.Type()), pVal, dataPtr)
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.EnumType), eSlot)
		return r

	case *ir.ExprConstructArray:
		arrSlot := g.allocaEntry(g.llvmType(v.ArrayType), 8)
		for i, el := range v.Elems {
			elVal := g.emitExpr(el)
			ep := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0, i64 %d",
				ep, g.llvmType(v.ArrayType), arrSlot, i)
			g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(el.Type()), elVal, ep)
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.ArrayType), arrSlot)
		return r

	case *ir.ExprConstructTuple:
		tupSlot := g.allocaEntry(g.llvmType(v.TupleType), 8)
		if len(v.Elems) == 0 {
			g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.TupleType), tupSlot)
		} else {
			for i, el := range v.Elems {
				elVal := g.emitExpr(el)
				ep := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
					ep, g.llvmType(v.TupleType), tupSlot, i)
				g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(el.Type()), elVal, ep, g.typeAlign(el.Type()))
			}
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.TupleType), tupSlot)
		return r

	case *ir.ExprConstructVector:
		vecSlot := g.allocaEntry(g.llvmType(v.VecType), 8)
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.VecType), vecSlot)
		if len(v.Elems) > 0 {
			resFn := "ll_vec_reserve__" + v.VecType.Mangled()
			g.declareHelper(resFn, "void", "ptr, i64")
			g.indentLine(1, "call void @%s(ptr %s, i64 %d)", resFn, vecSlot, len(v.Elems))
			pushFn := "ll_vec_push_p__" + v.VecType.Mangled()
			g.declareHelper(pushFn, "void", "ptr, ptr")
			for _, el := range v.Elems {
				elAddr := g.emitLValueAddr(el)
				g.indentLine(1, "call void @%s(ptr %s, ptr %s)", pushFn, vecSlot, elAddr)
			}
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.VecType), vecSlot)
		return r

	case *ir.ExprConstructClosure:
		entryFn := v.EntryFn
		if strings.HasSuffix(entryFn, "_entry") {
			base := strings.TrimSuffix(entryFn, "_entry")
			if g.funcSet[base] {
				entryFn = base
			}
		}
		if entryFn != "" && !g.funcSet[entryFn] {
			g.declareHelper(entryFn, "void", "...")
		}
		if v.CopyFn != "" && !g.funcSet[v.CopyFn] {
			g.declareHelper(v.CopyFn, "ptr", "ptr")
		}
		if v.DropFn != "" && !g.funcSet[v.DropFn] {
			g.declareHelper(v.DropFn, "void", "ptr")
		}
		if !v.FuncType.Closure {
			return "@" + entryFn
		}

		closSlot := g.allocaEntry("%struct.ll_closure", 8)
		p0 := g.newReg()
		p1 := g.newReg()
		p2 := g.newReg()
		p3 := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_closure, ptr %s, i32 0, i32 0", p0, closSlot)
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_closure, ptr %s, i32 0, i32 1", p1, closSlot)
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_closure, ptr %s, i32 0, i32 2", p2, closSlot)
		g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_closure, ptr %s, i32 0, i32 3", p3, closSlot)

		entryVal := "@" + entryFn
		envVal := "null"
		if v.EnvPtr != nil {
			envVal = g.emitExpr(v.EnvPtr)
		}
		copyVal := "null"
		if v.CopyFn != "" {
			copyVal = "@" + v.CopyFn
		}
		dropVal := "null"
		if v.DropFn != "" {
			dropVal = "@" + v.DropFn
		}
		g.indentLine(1, "store ptr %s, ptr %s, align 8", entryVal, p0)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", envVal, p1)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", copyVal, p2)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", dropVal, p3)
		r := g.newReg()
		g.indentLine(1, "%s = load %%struct.ll_closure, ptr %s, align 8", r, closSlot)
		return r

	case *ir.ExprFnToClosure:
		fnVal := g.emitExpr(v.Fn)
		r0 := g.newReg()
		g.indentLine(1, "%s = insertvalue %%struct.ll_closure zeroinitializer, ptr %s, 0", r0, fnVal)
		return r0

	case *ir.ExprWrapOptional:
		if v.OptType.IsBoxed() {
			st := v.OptType.Elem.(*types.Struct)
			sz := g.typeSize(st)
			allocReg := g.newReg()
			g.indentLine(1, "%s = call ptr @ll_alloc(i64 %d)", allocReg, sz)
			val := g.emitExpr(v.Value)
			g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(st), val, allocReg)
			return allocReg
		}
		optSlot := g.allocaEntry(g.llvmType(v.OptType), 8)
		pHas := g.newReg()
		pVal := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", pHas, g.llvmType(v.OptType), optSlot)
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pVal, g.llvmType(v.OptType), optSlot)
		g.indentLine(1, "store i1 true, ptr %s, align 1", pHas)
		val := g.emitExpr(v.Value)
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(v.Value.Type()), val, pVal)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.OptType), optSlot)
		return r

	case *ir.ExprOptionalHasValue:
		if optTy, ok := v.Opt.Type().(*types.Optional); ok && optTy.IsBoxed() {
			val := g.emitExpr(v.Opt)
			r := g.newReg()
			g.indentLine(1, "%s = icmp ne ptr %s, null", r, val)
			return r
		}
		optAddr := g.emitLValueAddr(v.Opt)
		pHas := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", pHas, g.llvmType(v.Opt.Type()), optAddr)
		r := g.newReg()
		g.indentLine(1, "%s = load i1, ptr %s, align 1", r, pHas)
		return r

	case *ir.ExprOptionalValue:
		if v.IsBoxed {
			ptrVal := g.emitExpr(v.Opt)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.ElemType), ptrVal)
			return r
		}
		optAddr := g.emitLValueAddr(v.Opt)
		pVal := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pVal, g.llvmType(v.Opt.Type()), optAddr)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.ElemType), pVal)
		return r

	case *ir.ExprOptionalValuePtr:
		optPtrVal := g.emitExpr(v.OptPtr)
		if v.IsBoxed {
			r := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", r, optPtrVal)
			return r
		}
		optTy := types.NewOptional(v.ElemType)
		pVal := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pVal, g.llvmType(optTy), optPtrVal)
		return pVal

	case *ir.ExprSliceView:
		dataVal := g.emitExpr(v.DataPtr)
		lenVal := g.emitExpr(v.Len)
		vecSlot := g.allocaEntry(g.llvmType(v.VecType), 8)
		p0 := g.newReg()
		p1 := g.newReg()
		p2 := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", p0, g.llvmType(v.VecType), vecSlot)
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", p1, g.llvmType(v.VecType), vecSlot)
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 2", p2, g.llvmType(v.VecType), vecSlot)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", dataVal, p0)
		g.indentLine(1, "store i64 %s, ptr %s, align 8", lenVal, p1)
		g.indentLine(1, "store i64 0, ptr %s, align 8", p2)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.VecType), vecSlot)
		return r

	case *ir.ExprBoxInterface:
		ifcSlot := g.allocaEntry(g.llvmType(v.IfcType), 8)
		p0 := g.newReg()
		p1 := g.newReg()
		p2 := g.newReg()
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", p0, g.llvmType(v.IfcType), ifcSlot)
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", p1, g.llvmType(v.IfcType), ifcSlot)
		g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 2", p2, g.llvmType(v.IfcType), ifcSlot)

		if v.ConcreteType != nil && v.ConcreteType.Equals(v.IfcType) {
			valAddr := g.emitLValueAddr(v.Value)
			dSlot := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", dSlot, g.llvmType(v.IfcType), valAddr)
			dVal := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", dVal, dSlot)
			vtSlot := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", vtSlot, g.llvmType(v.IfcType), valAddr)
			vtVal := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", vtVal, vtSlot)
			g.indentLine(1, "store ptr %s, ptr %s, align 8", dVal, p0)
			g.indentLine(1, "store ptr %s, ptr %s, align 8", vtVal, p1)
			g.indentLine(1, "store i1 false, ptr %s, align 1", p2)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.IfcType), ifcSlot)
			return r
		}

		var dataPtr string
		if _, isPtr := v.Value.Type().(*types.Ptr); isPtr {
			dataPtr = g.emitExpr(v.Value)
		} else if _, isRef := v.Value.Type().(*types.Ref); isRef {
			dataPtr = g.emitExpr(v.Value)
		} else {
			dataPtr = g.emitLValueAddr(v.Value)
		}
		vtSym := "@" + ir.VTableInstanceName(v.ConcreteType, v.IfcType)
		ownedVal := "false"
		if v.Owned {
			ownedVal = "true"
		}
		g.indentLine(1, "store ptr %s, ptr %s, align 8", dataPtr, p0)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", vtSym, p1)
		g.indentLine(1, "store i1 %s, ptr %s, align 1", ownedVal, p2)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.IfcType), ifcSlot)
		return r

	case *ir.ExprAlloc:
		sz := g.typeSize(v.AllocType)
		totBytes := fmt.Sprintf("%d", sz)
		if v.Count != nil {
			cntVal := g.emitExpr(v.Count)
			mulReg := g.newReg()
			g.indentLine(1, "%s = mul i64 %s, %d", mulReg, cntVal, sz)
			totBytes = mulReg
		}
		fnName := "ll_alloc"
		if v.Uninit {
			fnName = "ll_alloc_uninit"
		}
		r := g.newReg()
		g.indentLine(1, "%s = call ptr @%s(i64 %s)", r, fnName, totBytes)
		return r

	case *ir.ExprArenaAlloc:
		arenaPtr := g.emitExpr(v.Arena)
		sz := g.typeSize(v.AllocType)
		dtorVal := "null"
		if v.DtorFn != "" {
			g.declareHelper(v.DtorFn, "void", "ptr")
			dtorVal = "@" + v.DtorFn
		}
		g.declareHelper("ll_arena_alloc", "ptr", "ptr, i64, i64, ptr")
		r := g.newReg()
		g.indentLine(1, "%s = call ptr @ll_arena_alloc(ptr %s, i64 %d, i64 8, ptr %s)", r, arenaPtr, sz, dtorVal)
		return r

	case *ir.ExprFree:
		pVal := g.emitExpr(v.Ptr)
		g.indentLine(1, "call void @ll_free(ptr %s)", pVal)
		return "void"

	case *ir.ExprRealloc:
		pVal := g.emitExpr(v.Ptr)
		sz := g.typeSize(v.AllocType)
		totBytes := fmt.Sprintf("%d", sz)
		if v.Count != nil {
			cntVal := g.emitExpr(v.Count)
			mulReg := g.newReg()
			g.indentLine(1, "%s = mul i64 %s, %d", mulReg, cntVal, sz)
			totBytes = mulReg
		}
		r := g.newReg()
		g.indentLine(1, "%s = call ptr @ll_realloc(ptr %s, i64 %s)", r, pVal, totBytes)
		return r

	case *ir.ExprCopyBytes:
		dstVal := g.emitExpr(v.Dst)
		srcVal := g.emitExpr(v.Src)
		cntVal := g.emitExpr(v.Count)
		g.indentLine(1, "call void @ll_copy_bytes(ptr %s, ptr %s, i64 %s)", dstVal, srcVal, cntVal)
		return "void"

	case *ir.ExprSizeOf:
		return fmt.Sprintf("%d", g.typeSize(v.QueryType))

	case *ir.ExprAlignOf:
		return fmt.Sprintf("%d", g.typeAlign(v.QueryType))

	case *ir.ExprToString:
		if _, isPtr := v.ValType.(*types.Ptr); isPtr {
			val := g.emitExpr(v.Val)
			resSlot := g.allocaEntry("%struct.ll_string", 8)
			g.indentLine(1, "call void @ll_tostr_ptr_p(ptr %s, ptr %s)", resSlot, val)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if _, isRef := v.ValType.(*types.Ref); isRef {
			val := g.emitExpr(v.Val)
			resSlot := g.allocaEntry("%struct.ll_string", 8)
			g.indentLine(1, "call void @ll_tostr_ptr_p(ptr %s, ptr %s)", resSlot, val)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		srcAddr := g.emitLValueAddr(v.Val)
		fn := "ll_tostr_p__" + v.ValType.Mangled()
		g.declareHelper(fn, "void", "ptr, ptr")
		resSlot := g.allocaEntry("%struct.ll_string", 8)
		g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, resSlot, srcAddr)
		r := g.newReg()
		g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
		return r

	case *ir.ExprDrop:
		pVal := g.emitExpr(v.Ptr)
		g.emitTypeDrop(pVal, v.ValType)
		return "void"

	case *ir.ExprCopy:
		if v.ValType.IsPlain() {
			return g.emitExpr(v.Val)
		}
		valAddr := g.emitLValueAddr(v.Val)
		resSlot := g.allocaEntry(g.llvmType(v.ValType), g.typeAlign(v.ValType))
		g.emitCopyVal(resSlot, valAddr, v.ValType)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", r, g.llvmType(v.ValType), resSlot, g.typeAlign(v.ValType))
		return r

	case *ir.ExprEq:
		if g.isFloat(v.ValType) {
			lVal := g.emitExpr(v.Left)
			rVal := g.emitExpr(v.Right)
			r := g.newReg()
			g.indentLine(1, "%s = fcmp oeq %s %s, %s", r, g.llvmType(v.ValType), lVal, rVal)
			return r
		}
		if g.isScalar(v.ValType) {
			lVal := g.emitExpr(v.Left)
			rVal := g.emitExpr(v.Right)
			r := g.newReg()
			g.indentLine(1, "%s = icmp eq %s %s, %s", r, g.llvmType(v.ValType), lVal, rVal)
			return r
		}
		leftAddr := g.emitLValueAddr(v.Left)
		rightAddr := g.emitLValueAddr(v.Right)
		fn := "ll_eq_p__" + v.ValType.Mangled()
		g.declareHelper(fn, "i1", "ptr, ptr")
		r := g.newReg()
		g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, fn, leftAddr, rightAddr)
		return r

	case *ir.ExprCmp:
		leftAddr := g.emitLValueAddr(v.Left)
		rightAddr := g.emitLValueAddr(v.Right)
		if v.ValType != nil && v.ValType.Equals(types.TypeString) {
			r := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", r, leftAddr, rightAddr)
			return r
		}
		fn := "ll_cmp_p__" + v.ValType.Mangled()
		g.declareHelper(fn, "i64", "ptr, ptr")
		r := g.newReg()
		g.indentLine(1, "%s = call i64 @%s(ptr %s, ptr %s)", r, fn, leftAddr, rightAddr)
		return r

	case *ir.ExprHash:
		if v.ValType != nil && v.ValType.Equals(types.TypeString) {
			addr := g.emitLValueAddr(v.Val)
			r := g.newReg()
			g.declareHelper("ll_str_hash_p", "i64", "ptr")
			g.indentLine(1, "%s = call i64 @ll_str_hash_p(ptr %s)", r, addr)
			return r
		}
		if _, isPtr := v.ValType.(*types.Ptr); isPtr {
			val := g.emitExpr(v.Val)
			i := g.newReg()
			r := g.newReg()
			g.indentLine(1, "%s = ptrtoint ptr %s to i64", i, val)
			g.indentLine(1, "%s = call i64 @ll_hash_mix64(i64 %s)", r, i)
			return r
		}
		if _, isRef := v.ValType.(*types.Ref); isRef {
			val := g.emitExpr(v.Val)
			i := g.newReg()
			r := g.newReg()
			g.indentLine(1, "%s = ptrtoint ptr %s to i64", i, val)
			g.indentLine(1, "%s = call i64 @ll_hash_mix64(i64 %s)", r, i)
			return r
		}
		val := g.emitExpr(v.Val)
		fn := ir.HashFnName(v.ValType)
		g.declareHelper(fn, "i64", g.llvmType(v.ValType))
		r := g.newReg()
		g.indentLine(1, "%s = call i64 @%s(%s %s)", r, fn, g.llvmType(v.ValType), val)
		return r

	case *ir.ExprVecPush:
		vecAddr := g.emitExpr(v.VecPtr)
		itemAddr := g.emitLValueAddr(v.Item)
		fn := "ll_vec_push_p__" + v.VecType.Mangled()
		g.declareHelper(fn, "void", "ptr, ptr")
		g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, vecAddr, itemAddr)
		return "void"

	default:
		return "0"
	}
}

// -----------------------------------------------------------------------------
// Operation Emitters (Binary, Unary, Cast, Call)
// -----------------------------------------------------------------------------

func (g *llvmEmitter) isIntegerType(t types.Type) bool {
	if prim := types.UnderlyingPrimitive(t); prim != nil {
		return prim.IsInteger()
	}
	return false
}

func (g *llvmEmitter) isFloat(t types.Type) bool {
	if prim := types.UnderlyingPrimitive(t); prim != nil {
		return prim.Kind == types.PrimF32 || prim.Kind == types.PrimF64
	}
	return false
}

func (g *llvmEmitter) isUnsigned(t types.Type) bool {
	if prim := types.UnderlyingPrimitive(t); prim != nil {
		return prim.IsUnsignedInt()
	}
	return false
}

func (g *llvmEmitter) isScalar(t types.Type) bool {
	if t == nil {
		return false
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid && v.Kind != types.PrimString && v.Kind != types.PrimArena && !g.isFloat(t)
	case *types.Ptr, *types.Ref:
		return true
	case *types.Enum:
		return !v.IsTagged
	case *types.Distinct:
		return g.isScalar(v.Base)
	default:
		return false
	}
}

func (g *llvmEmitter) emitBinaryOp(op token.Kind, lVal, rVal string, t types.Type, file string, line int, rType types.Type) string {
	if t != nil && t.Equals(types.TypeString) {
		slotL := g.allocaEntry("%struct.ll_string", 8)
		g.indentLine(1, "store %%struct.ll_string %s, ptr %s, align 8", lVal, slotL)

		slotR := g.allocaEntry("%struct.ll_string", 8)
		g.indentLine(1, "store %%struct.ll_string %s, ptr %s, align 8", rVal, slotR)

		switch op {
		case token.Eq:
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_str_eq_p(ptr %s, ptr %s)", r, slotL, slotR)
			return r
		case token.Neq:
			eq := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_str_eq_p(ptr %s, ptr %s)", eq, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = xor i1 %s, true", r, eq)
			return r
		case token.Lt:
			cmp := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = icmp slt i64 %s, 0", r, cmp)
			return r
		case token.Lte:
			cmp := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = icmp sle i64 %s, 0", r, cmp)
			return r
		case token.Gt:
			cmp := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = icmp sgt i64 %s, 0", r, cmp)
			return r
		case token.Gte:
			cmp := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", cmp, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = icmp sge i64 %s, 0", r, cmp)
			return r
		}
	}

	isFloat := g.isFloat(t)
	isSigned := !g.isUnsigned(t)

	r := g.newReg()
	tyStr := g.llvmType(t)

	if tyStr == "ptr" {
		elemTy := "i8"
		if p, ok := t.(*types.Ptr); ok && !p.Elem.Equals(types.TypeVoid) {
			elemTy = g.llvmType(p.Elem)
		} else if rf, ok := t.(*types.Ref); ok {
			elemTy = g.llvmType(rf.Elem)
		}
		switch op {
		case token.Plus:
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s", r, elemTy, lVal, rVal)
			return r
		case token.Minus:
			neg := g.newReg()
			g.indentLine(1, "%s = sub i64 0, %s", neg, rVal)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s", r, elemTy, lVal, neg)
			return r
		}
	}

	switch op {
	case token.Plus:
		if isFloat {
			g.indentLine(1, "%s = fadd %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = add %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Minus:
		if isFloat {
			g.indentLine(1, "%s = fsub %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = sub %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Star:
		if isFloat {
			g.indentLine(1, "%s = fmul %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = mul %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Slash:
		if isFloat {
			g.indentLine(1, "%s = fdiv %s %s, %s", r, tyStr, lVal, rVal)
			return r
		}
		fileVal := "null"
		if file != "" {
			fileVal = g.internString(file).symbol
		}
		prim := types.UnderlyingPrimitive(t)
		bits := 64
		if prim != nil {
			bits = prim.BitWidth()
		}
		if rType == nil {
			rType = t
		}
		rTyStr := g.llvmType(rType)
		if prim != nil && prim.IsUnsignedInt() {
			g.declareHelper("ll_div_u64", "i64", "i64, i64, ptr, i32")
			lVal64 := lVal
			if tyStr != "i64" {
				lVal64 = g.newReg()
				g.indentLine(1, "%s = zext %s %s to i64", lVal64, tyStr, lVal)
			}
			rVal64 := rVal
			if rTyStr != "i64" {
				rVal64 = g.newReg()
				g.indentLine(1, "%s = zext %s %s to i64", rVal64, rTyStr, rVal)
			}
			callReg := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_div_u64(i64 %s, i64 %s, ptr %s, i32 %d)", callReg, lVal64, rVal64, fileVal, line)
			if tyStr != "i64" {
				g.indentLine(1, "%s = trunc i64 %s to %s", r, callReg, tyStr)
				return r
			}
			return callReg
		}
		fn := fmt.Sprintf("ll_div_i%d", bits)
		g.declareHelper(fn, tyStr, fmt.Sprintf("%s, %s, ptr, i32", tyStr, tyStr))
		rValTy := rVal
		if rTyStr != tyStr {
			rValTy = g.newReg()
			if bits > g.typeSize(rType)*8 {
				g.indentLine(1, "%s = sext %s %s to %s", rValTy, rTyStr, rVal, tyStr)
			} else {
				g.indentLine(1, "%s = trunc %s %s to %s", rValTy, rTyStr, rVal, tyStr)
			}
		}
		g.indentLine(1, "%s = call %s @%s(%s %s, %s %s, ptr %s, i32 %d)", r, tyStr, fn, tyStr, lVal, tyStr, rValTy, fileVal, line)
		return r

	case token.Percent:
		fileVal := "null"
		if file != "" {
			fileVal = g.internString(file).symbol
		}
		prim := types.UnderlyingPrimitive(t)
		bits := 64
		if prim != nil {
			bits = prim.BitWidth()
		}
		if rType == nil {
			rType = t
		}
		rTyStr := g.llvmType(rType)
		if prim != nil && prim.IsUnsignedInt() {
			g.declareHelper("ll_mod_u64", "i64", "i64, i64, ptr, i32")
			lVal64 := lVal
			if tyStr != "i64" {
				lVal64 = g.newReg()
				g.indentLine(1, "%s = zext %s %s to i64", lVal64, tyStr, lVal)
			}
			rVal64 := rVal
			if rTyStr != "i64" {
				rVal64 = g.newReg()
				g.indentLine(1, "%s = zext %s %s to i64", rVal64, rTyStr, rVal)
			}
			callReg := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_mod_u64(i64 %s, i64 %s, ptr %s, i32 %d)", callReg, lVal64, rVal64, fileVal, line)
			if tyStr != "i64" {
				g.indentLine(1, "%s = trunc i64 %s to %s", r, callReg, tyStr)
				return r
			}
			return callReg
		}
		fn := fmt.Sprintf("ll_mod_i%d", bits)
		g.declareHelper(fn, tyStr, fmt.Sprintf("%s, %s, ptr, i32", tyStr, tyStr))
		rValTy := rVal
		if rTyStr != tyStr {
			rValTy = g.newReg()
			if bits > g.typeSize(rType)*8 {
				g.indentLine(1, "%s = sext %s %s to %s", rValTy, rTyStr, rVal, tyStr)
			} else {
				g.indentLine(1, "%s = trunc %s %s to %s", rValTy, rTyStr, rVal, tyStr)
			}
		}
		g.indentLine(1, "%s = call %s @%s(%s %s, %s %s, ptr %s, i32 %d)", r, tyStr, fn, tyStr, lVal, tyStr, rValTy, fileVal, line)
		return r

	case token.Amp:
		g.indentLine(1, "%s = and %s %s, %s", r, tyStr, lVal, rVal)
		return r

	case token.Pipe:
		g.indentLine(1, "%s = or %s %s, %s", r, tyStr, lVal, rVal)
		return r

	case token.Caret:
		g.indentLine(1, "%s = xor %s %s, %s", r, tyStr, lVal, rVal)
		return r

	case token.Shl:
		fileVal := "null"
		if file != "" {
			fileVal = g.internString(file).symbol
		}
		prim := types.UnderlyingPrimitive(t)
		bits := 64
		if prim != nil {
			bits = prim.BitWidth()
		}
		if rType == nil {
			rType = t
		}
		rTyStr := g.llvmType(rType)
		fn := fmt.Sprintf("ll_shl_u%d", bits)
		g.declareHelper(fn, tyStr, fmt.Sprintf("%s, i64, i32, ptr, i32", tyStr))
		rVal64 := rVal
		if rTyStr != "i64" {
			rVal64 = g.newReg()
			g.indentLine(1, "%s = sext %s %s to i64", rVal64, rTyStr, rVal)
		}
		g.indentLine(1, "%s = call %s @%s(%s %s, i64 %s, i32 1, ptr %s, i32 %d)", r, tyStr, fn, tyStr, lVal, rVal64, fileVal, line)
		return r

	case token.Shr:
		fileVal := "null"
		if file != "" {
			fileVal = g.internString(file).symbol
		}
		prim := types.UnderlyingPrimitive(t)
		bits := 64
		if prim != nil {
			bits = prim.BitWidth()
		}
		if rType == nil {
			rType = t
		}
		rTyStr := g.llvmType(rType)
		var fn string
		if prim != nil && prim.IsUnsignedInt() {
			fn = fmt.Sprintf("ll_shr_u%d", bits)
		} else {
			fn = fmt.Sprintf("ll_shr_i%d", bits)
		}
		g.declareHelper(fn, tyStr, fmt.Sprintf("%s, i64, i32, ptr, i32", tyStr))
		rVal64 := rVal
		if rTyStr != "i64" {
			rVal64 = g.newReg()
			g.indentLine(1, "%s = sext %s %s to i64", rVal64, rTyStr, rVal)
		}
		g.indentLine(1, "%s = call %s @%s(%s %s, i64 %s, i32 1, ptr %s, i32 %d)", r, tyStr, fn, tyStr, lVal, rVal64, fileVal, line)
		return r

	// Comparisons
	case token.Eq:
		if isFloat {
			g.indentLine(1, "%s = fcmp oeq %s %s, %s", r, tyStr, lVal, rVal)
		} else if g.isScalar(t) {
			g.indentLine(1, "%s = icmp eq %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			slotL := g.allocaEntry(tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, lVal, slotL, g.typeAlign(t))
			slotR := g.allocaEntry(tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, rVal, slotR, g.typeAlign(t))
			fn := "ll_eq_p__" + t.Mangled()
			g.declareHelper(fn, "i1", "ptr, ptr")
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, fn, slotL, slotR)
		}
		return r

	case token.Neq:
		if isFloat {
			g.indentLine(1, "%s = fcmp one %s %s, %s", r, tyStr, lVal, rVal)
		} else if g.isScalar(t) {
			g.indentLine(1, "%s = icmp ne %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			slotL := g.allocaEntry(tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, lVal, slotL, g.typeAlign(t))
			slotR := g.allocaEntry(tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, rVal, slotR, g.typeAlign(t))
			fn := "ll_eq_p__" + t.Mangled()
			g.declareHelper(fn, "i1", "ptr, ptr")
			eqReg := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", eqReg, fn, slotL, slotR)
			g.indentLine(1, "%s = xor i1 %s, true", r, eqReg)
		}
		return r

	case token.Lt:
		if isFloat {
			g.indentLine(1, "%s = fcmp olt %s %s, %s", r, tyStr, lVal, rVal)
		} else if isSigned {
			g.indentLine(1, "%s = icmp slt %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = icmp ult %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Lte:
		if isFloat {
			g.indentLine(1, "%s = fcmp ole %s %s, %s", r, tyStr, lVal, rVal)
		} else if isSigned {
			g.indentLine(1, "%s = icmp sle %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = icmp ule %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Gt:
		if isFloat {
			g.indentLine(1, "%s = fcmp ogt %s %s, %s", r, tyStr, lVal, rVal)
		} else if isSigned {
			g.indentLine(1, "%s = icmp sgt %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = icmp ugt %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Gte:
		if isFloat {
			g.indentLine(1, "%s = fcmp oge %s %s, %s", r, tyStr, lVal, rVal)
		} else if isSigned {
			g.indentLine(1, "%s = icmp sge %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = icmp uge %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.KwAnd:
		g.indentLine(1, "%s = and i1 %s, %s", r, lVal, rVal)
		return r

	case token.KwOr:
		g.indentLine(1, "%s = or i1 %s, %s", r, lVal, rVal)
		return r

	default:
		return lVal
	}
}

func (g *llvmEmitter) emitUnaryOp(op token.Kind, val string, t types.Type) string {
	isFloat := g.isFloat(t)
	r := g.newReg()
	tyStr := g.llvmType(t)

	switch op {
	case token.Minus:
		if isFloat {
			g.indentLine(1, "%s = fneg %s %s", r, tyStr, val)
		} else {
			g.indentLine(1, "%s = sub %s 0, %s", r, tyStr, val)
		}
		return r
	case token.KwNot:
		g.indentLine(1, "%s = xor i1 %s, true", r, val)
		return r
	case token.Tilde:
		g.indentLine(1, "%s = xor %s %s, -1", r, tyStr, val)
		return r
	default:
		return val
	}
}

func (g *llvmEmitter) emitCastOp(val string, from, to types.Type) string {
	fromTyStr := g.llvmType(from)
	toTyStr := g.llvmType(to)

	if fromTyStr == toTyStr {
		return val
	}
	if fromTyStr == "void" || toTyStr == "void" {
		return "0"
	}

	fromIsPtr := fromTyStr == "ptr"
	toIsPtr := toTyStr == "ptr"

	r := g.newReg()

	if fromIsPtr && toIsPtr {
		return val
	}
	if fromIsPtr && !toIsPtr {
		g.indentLine(1, "%s = ptrtoint ptr %s to %s", r, val, toTyStr)
		return r
	}
	if !fromIsPtr && toIsPtr {
		g.indentLine(1, "%s = inttoptr %s %s to ptr", r, fromTyStr, val)
		return r
	}

	fromSize := g.typeSize(from)
	toSize := g.typeSize(to)

	fromIsFloat := g.isFloat(from)
	toIsFloat := g.isFloat(to)

	if fromIsFloat && toIsFloat {
		if fromSize < toSize {
			g.indentLine(1, "%s = fpext %s %s to %s", r, fromTyStr, val, toTyStr)
		} else {
			g.indentLine(1, "%s = fptrunc %s %s to %s", r, fromTyStr, val, toTyStr)
		}
		return r
	}

	if fromIsFloat && !toIsFloat {
		if g.isUnsigned(to) {
			g.indentLine(1, "%s = fptoui %s %s to %s", r, fromTyStr, val, toTyStr)
		} else {
			g.indentLine(1, "%s = fptosi %s %s to %s", r, fromTyStr, val, toTyStr)
		}
		return r
	}

	if !fromIsFloat && toIsFloat {
		if g.isUnsigned(from) {
			g.indentLine(1, "%s = uitofp %s %s to %s", r, fromTyStr, val, toTyStr)
		} else {
			g.indentLine(1, "%s = sitofp %s %s to %s", r, fromTyStr, val, toTyStr)
		}
		return r
	}

	// Integer to Integer
	if fromSize == toSize {
		return val
	}
	if fromSize < toSize {
		if g.isUnsigned(from) {
			g.indentLine(1, "%s = zext %s %s to %s", r, fromTyStr, val, toTyStr)
		} else {
			g.indentLine(1, "%s = sext %s %s to %s", r, fromTyStr, val, toTyStr)
		}
		return r
	}
	g.indentLine(1, "%s = trunc %s %s to %s", r, fromTyStr, val, toTyStr)
	return r
}

func (g *llvmEmitter) exprLLVMType(e ir.Expr) string {
	if loc, ok := e.(*ir.ExprLocal); ok && loc.Local.IsMutPtr {
		return "ptr"
	}
	if ef, ok := e.(*ir.ExprField); ok && ef.Base.Type() != nil {
		if _, isIfc := ef.Base.Type().(*types.Interface); isIfc {
			if ef.FieldName == "data" || ef.FieldName == "vtable" {
				return "ptr"
			}
		}
	}
	return g.llvmType(e.Type())
}

func (g *llvmEmitter) emitCall(call *ir.ExprCall) string {
	// Special-case interface method call: lookup in vtable
	if fnField, ok := call.Callee.(*ir.ExprField); ok && strings.HasPrefix(fnField.FieldName, "m_") {
		var ifc *types.Interface
		var vtBase ir.Expr
		if vtField, isField := fnField.Base.(*ir.ExprField); isField {
			if ifcTy, isIfc := vtField.Base.Type().(*types.Interface); isIfc {
				ifc = ifcTy
				vtBase = vtField
			}
		}
		if ifc != nil {
			mName := strings.TrimPrefix(fnField.FieldName, "m_")
			mIdx := -1
			for i, m := range ifc.Methods {
				if m.Name == mName {
					mIdx = i
					break
				}
			}
			if mIdx >= 0 {
				vtPtr := g.emitExpr(vtBase)
				fnPtrSlot := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_vt_%s, ptr %s, i32 0, i32 %d",
					fnPtrSlot, ifc.CName, vtPtr, 2+mIdx)
				fnPtr := g.newReg()
				g.indentLine(1, "%s = load ptr, ptr %s, align 8", fnPtr, fnPtrSlot)

				var typedArgs []string
				for _, a := range call.Args {
					aVal := g.emitExpr(a)
					typedArgs = append(typedArgs, fmt.Sprintf("%s %s", g.exprLLVMType(a), aVal))
				}

				retTyStr := g.llvmType(call.Typ)
				if retTyStr == "void" {
					g.indentLine(1, "call void %s(%s)", fnPtr, strings.Join(typedArgs, ", "))
					return "void"
				}
				r := g.newReg()
				g.indentLine(1, "%s = call %s %s(%s)", r, retTyStr, fnPtr, strings.Join(typedArgs, ", "))
				return r
			}
		}
	}

	// Special-case print, println, eprint, eprintln: use pointer wrapper
	if fnRef, ok := call.Callee.(*ir.ExprFuncRef); ok {
		if fnRef.Name == "ll_print_str" && len(call.Args) == 3 {
			strAddr := g.emitLValueAddr(call.Args[0])
			isErrVal := g.emitExpr(call.Args[1])
			isLnVal := g.emitExpr(call.Args[2])
			isErrI32 := g.emitCastOp(isErrVal, types.TypeBool, types.TypeI32)
			isLnI32 := g.emitCastOp(isLnVal, types.TypeBool, types.TypeI32)
			g.indentLine(1, "call void @ll_print_str_p(ptr %s, i32 %s, i32 %s)", strAddr, isErrI32, isLnI32)
			return "void"
		}
		if fnRef.Name == "ll_panic_str" && len(call.Args) >= 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			fileAddr := "null"
			lineVal := "0"
			colVal := "0"
			if len(call.Args) >= 4 {
				fileAddr = g.emitLValueAddr(call.Args[1])
				lineVal = g.emitExpr(call.Args[2])
				colVal = g.emitExpr(call.Args[3])
			} else if len(call.Args) >= 3 {
				fileAddr = g.emitLValueAddr(call.Args[1])
				lineVal = g.emitExpr(call.Args[2])
			}
			g.indentLine(1, "call void @ll_panic_str_p(ptr %s, ptr %s, i64 %s, i64 %s)", strAddr, fileAddr, lineVal, colVal)
			g.indentLine(1, "unreachable")
			g.isTerminated = true
			return "void"
		}
		if fnRef.Name == "ll_arena_new" && len(call.Args) >= 1 {
			capVal := g.emitExpr(call.Args[0])
			arenaSlot := g.allocaEntry("%struct.ll_arena", 8)
			g.declareHelper("ll_arena_new_p", "void", "ptr, i64")
			g.indentLine(1, "call void @ll_arena_new_p(ptr %s, i64 %s)", arenaSlot, capVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_arena, ptr %s, align 8", r, arenaSlot)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_slice_view__") && len(call.Args) >= 3 {
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_slice_view__")
			wrapper := "ll_vec_slice_view_p__" + mangled
			vecAddr := g.emitLValueAddr(call.Args[0])
			loVal := g.emitExpr(call.Args[1])
			hiVal := g.emitExpr(call.Args[2])
			checkVal := "1"
			fileVal := "null"
			lineVal := "0"
			if len(call.Args) >= 6 {
				checkVal = g.emitExpr(call.Args[3])
				fileVal = g.emitExpr(call.Args[4])
				lineVal = g.emitExpr(call.Args[5])
			}
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			g.declareHelper(wrapper, "void", "ptr, ptr, i64, i64, i32, ptr, i64")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, i64 %s, i64 %s, i32 %s, ptr %s, i64 %s)",
				wrapper, resSlot, vecAddr, loVal, hiVal, checkVal, fileVal, lineVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_slice_owned__") && len(call.Args) >= 3 {
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_slice_owned__")
			wrapper := "ll_vec_slice_owned_p__" + mangled
			vecAddr := g.emitLValueAddr(call.Args[0])
			loVal := g.emitExpr(call.Args[1])
			hiVal := g.emitExpr(call.Args[2])
			checkVal := "1"
			fileVal := "null"
			lineVal := "0"
			if len(call.Args) >= 6 {
				checkVal = g.emitExpr(call.Args[3])
				fileVal = g.emitExpr(call.Args[4])
				lineVal = g.emitExpr(call.Args[5])
			}
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			g.declareHelper(wrapper, "void", "ptr, ptr, i64, i64, i32, ptr, i64")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, i64 %s, i64 %s, i32 %s, ptr %s, i64 %s)",
				wrapper, resSlot, vecAddr, loVal, hiVal, checkVal, fileVal, lineVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_assert" && len(call.Args) >= 4 {
			condVal := g.emitExpr(call.Args[0])
			msgAddr := g.emitLValueAddr(call.Args[1])
			fileAddr := g.emitLValueAddr(call.Args[2])
			lineVal := g.emitExpr(call.Args[3])
			colVal := "0"
			if len(call.Args) >= 5 {
				colVal = g.emitExpr(call.Args[4])
			}
			g.declareHelper("ll_assert_p", "void", "i1, ptr, ptr, i64, i64")
			g.indentLine(1, "call void @ll_assert_p(i1 %s, ptr %s, ptr %s, i64 %s, i64 %s)", condVal, msgAddr, fileAddr, lineVal, colVal)
			return "void"
		}
		if fnRef.Name == "ll_str_c_str" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			gep := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 0", gep, strAddr)
			rawPtr := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", rawPtr, gep)
			isNull := g.newReg()
			g.indentLine(1, "%s = icmp eq ptr %s, null", isNull, rawPtr)
			emptySym := g.internString("").symbol
			r := g.newReg()
			g.indentLine(1, "%s = select i1 %s, ptr %s, ptr %s", r, isNull, emptySym, rawPtr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_push__") && len(call.Args) == 2 {
			vecAddr := g.emitAsPtr(call.Args[0])
			itemAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_push__")
			wrapper := "ll_vec_push_p__" + mangled
			g.declareHelper(wrapper, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s)", wrapper, vecAddr, itemAddr)
			return "void"
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_get__") && len(call.Args) == 2 {
			vecAddr := g.emitAsPtr(call.Args[0])
			idxVal := g.emitExpr(call.Args[1])
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			optTy, _ := call.Typ.(*types.Optional)
			elemTy := optTy.Elem

			pLen := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1", pLen, vecAddr)
			lenVal := g.newReg()
			g.indentLine(1, "%s = load i64, ptr %s, align 8", lenVal, pLen)

			cond1 := g.newReg()
			g.indentLine(1, "%s = icmp sge i64 %s, 0", cond1, idxVal)
			cond2 := g.newReg()
			g.indentLine(1, "%s = icmp slt i64 %s, %s", cond2, idxVal, lenVal)
			inBounds := g.newReg()
			g.indentLine(1, "%s = and i1 %s, %s", inBounds, cond1, cond2)

			lIn := g.newBlockLabel("vec_get.in")
			lOut := g.newBlockLabel("vec_get.out")
			lCont := g.newBlockLabel("vec_get.cont")
			g.indentLine(1, "br i1 %s, label %%%s, label %%%s", inBounds, lIn, lOut)
			g.isTerminated = true

			// In bounds:
			g.emitLabel(lIn)
			if optTy.IsBoxed() {
				sz := g.typeSize(elemTy)
				allocReg := g.newReg()
				g.indentLine(1, "%s = call ptr @ll_alloc(i64 %d)", allocReg, sz)
				pData := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0", pData, vecAddr)
				dataVal := g.newReg()
				g.indentLine(1, "%s = load ptr, ptr %s, align 8", dataVal, pData)
				pElem := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s", pElem, g.llvmType(elemTy), dataVal, idxVal)
				g.emitCopyVal(allocReg, pElem, elemTy)
				g.indentLine(1, "store ptr %s, ptr %s, align 8", allocReg, resSlot)
			} else {
				pHas := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", pHas, g.llvmType(call.Typ), resSlot)
				g.indentLine(1, "store i1 true, ptr %s, align 1", pHas)

				pData := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0", pData, vecAddr)
				dataVal := g.newReg()
				g.indentLine(1, "%s = load ptr, ptr %s, align 8", dataVal, pData)
				pElem := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i64 %s", pElem, g.llvmType(elemTy), dataVal, idxVal)

				pDest := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", pDest, g.llvmType(call.Typ), resSlot)
				g.emitCopyVal(pDest, pElem, elemTy)
			}
			g.indentLine(1, "br label %%%s", lCont)
			g.isTerminated = true

			// Out of bounds:
			g.emitLabel(lOut)
			if optTy.IsBoxed() {
				g.indentLine(1, "store ptr null, ptr %s, align 8", resSlot)
			} else {
				g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(call.Typ), resSlot)
			}
			g.indentLine(1, "br label %%%s", lCont)
			g.isTerminated = true

			// Continuation:
			g.emitLabel(lCont)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_contains__") && len(call.Args) == 2 {
			vecAddr := g.emitAsPtr(call.Args[0])
			itemAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_contains__")
			wrapper := "ll_vec_contains_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, vecAddr, itemAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_arr_contains__") && len(call.Args) == 2 {
			arrAddr := g.emitLValueAddr(call.Args[0])
			itemAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_arr_contains__")
			wrapper := "ll_arr_contains_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, arrAddr, itemAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_pop__") && len(call.Args) == 1 {
			vecAddr := g.emitAsPtr(call.Args[0])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_pop__")
			wrapper := "ll_vec_pop_p__" + mangled
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			g.declareHelper(wrapper, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s)", wrapper, resSlot, vecAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_set_insert__") && len(call.Args) == 2 {
			setAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_set_insert__")
			wrapper := "ll_set_insert_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, setAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_set_has__") && len(call.Args) == 2 {
			setAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_set_has__")
			wrapper := "ll_set_has_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, setAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_set_remove__") && len(call.Args) == 2 {
			setAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_set_remove__")
			wrapper := "ll_set_remove_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, setAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_insert__") && len(call.Args) == 3 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			valAddr := g.emitLValueAddr(call.Args[2])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_insert__")
			wrapper := "ll_map_insert_p__" + mangled
			g.declareHelper(wrapper, "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, ptr %s)", wrapper, mapAddr, keyAddr, valAddr)
			return "void"
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_get__") && len(call.Args) == 2 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_get__")
			wrapper := "ll_map_get_p__" + mangled
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			g.declareHelper(wrapper, "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, ptr %s)", wrapper, resSlot, mapAddr, keyAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_has__") && len(call.Args) == 2 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_has__")
			wrapper := "ll_map_has_p__" + mangled
			g.declareHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, mapAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_remove__") && len(call.Args) == 2 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_remove__")
			wrapper := "ll_map_remove_p__" + mangled
			resSlot := g.allocaEntry(g.llvmType(call.Typ), 8)
			g.declareHelper(wrapper, "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, ptr %s)", wrapper, resSlot, mapAddr, keyAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
	}

	// Function call (static or indirect)
	calleeSymbol := ""
	if fnRef, ok := call.Callee.(*ir.ExprFuncRef); ok {
		calleeSymbol = "@" + fnRef.Name
		if !g.funcSet[fnRef.Name] && !g.declaredFns[fnRef.Name] {
			var argTys []string
			for _, a := range call.Args {
				argTys = append(argTys, g.exprLLVMType(a))
			}
			g.declareHelper(fnRef.Name, g.llvmType(call.Typ), strings.Join(argTys, ", "))
		}
	} else {
		calleeSymbol = g.emitExpr(call.Callee)
	}

	var typedArgs []string
	for _, a := range call.Args {
		aVal := g.emitExpr(a)
		typedArgs = append(typedArgs, fmt.Sprintf("%s %s", g.exprLLVMType(a), aVal))
	}

	retTyStr := g.llvmType(call.Typ)
	if retTyStr == "void" {
		g.indentLine(1, "call void %s(%s)", calleeSymbol, strings.Join(typedArgs, ", "))
		return "void"
	}
	r := g.newReg()
	g.indentLine(1, "%s = call %s %s(%s)", r, retTyStr, calleeSymbol, strings.Join(typedArgs, ", "))
	return r
}

func isFuncRef(e ir.Expr) bool {
	_, ok := e.(*ir.ExprFuncRef)
	return ok
}

func (g *llvmEmitter) emitStaticThunks() {
	if len(g.prog.StaticThunks) == 0 {
		return
	}
	g.line("; Static Function Closure Thunks")
	for _, st := range g.prog.StaticThunks {
		var paramDecls []string
		var paramTypes []string
		var callArgs []string
		paramDecls = append(paramDecls, "ptr %_ll_env.param")
		paramTypes = append(paramTypes, "ptr")
		for idx, p := range st.FuncType.Params {
			pn := fmt.Sprintf("%%_p%d.param", idx)
			pTy := g.llvmType(p.Type)
			if p.Mode == types.ModeMut {
				pTy = "ptr"
			}
			paramDecls = append(paramDecls, fmt.Sprintf("%s %s", pTy, pn))
			paramTypes = append(paramTypes, pTy)
			callArgs = append(callArgs, fmt.Sprintf("%s %s", pTy, pn))
		}
		retTy := g.llvmType(st.FuncType.Ret)
		if g.unitMode == UnitPackage {
			g.line("declare %s @%s(%s)", retTy, st.ThunkCName, strings.Join(paramTypes, ", "))
			g.declaredFns[st.ThunkCName] = true
			continue
		}
		g.line("define %s @%s(%s) {", retTy, st.ThunkCName, strings.Join(paramDecls, ", "))
		g.line("entry:")
		if st.FuncType.Ret.Equals(types.TypeVoid) {
			g.indentLine(1, "call void @%s(%s)", st.TargetFunc.CName, strings.Join(callArgs, ", "))
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "%%t.0 = call %s @%s(%s)", retTy, st.TargetFunc.CName, strings.Join(callArgs, ", "))
			g.indentLine(1, "ret %s %%t.0", retTy)
		}
		g.line("}")
		g.line("")
	}
}

func (g *llvmEmitter) emitVTables() {
	if len(g.prog.VTables) == 0 {
		return
	}
	g.line("; Interface VTable Method Wrappers and Instances")
	for _, vt := range g.prog.VTables {
		cTy := g.llvmType(vt.Concrete)
		vtInst := ir.VTableInstanceName(vt.Concrete, vt.Iface)
		vtTypeName := ir.VTableTypeName(vt.Iface)
		if g.unitMode == UnitPackage {
			g.line("@%s = external constant %%struct.%s, align 8", vtInst, vtTypeName)
			continue
		}

		// 1. drop_box
		dropName := vtInst + "__drop"
		g.line("define void @%s(ptr %%_data.param) {", dropName)
		g.line("entry:")
		g.indentRaw(1, "%isNull = icmp eq ptr %_data.param, null")
		g.indentRaw(1, "br i1 %isNull, label %done, label %doDrop")
		g.line("doDrop:")
		if !vt.Concrete.IsPlain() {
			dropFn := ir.DropFnName(vt.Concrete)
			g.declareHelper(dropFn, "void", "ptr")
			g.indentLine(1, "call void @%s(ptr %%_data.param)", dropFn)
		}
		g.indentRaw(1, "call void @ll_free(ptr %_data.param)")
		g.indentRaw(1, "br label %done")
		g.line("done:")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line("")

		// 2. clone_box
		cloneName := vtInst + "__clone"
		g.line("define ptr @%s(ptr %%_data.param) {", cloneName)
		g.line("entry:")
		if CanCopyType(g.prog, vt.Concrete) {
			sz := g.typeSize(vt.Concrete)
			g.indentLine(1, "%%nb = call ptr @ll_alloc(i64 %d)", sz)
			copyP := "ll_copy_p__" + vt.Concrete.Mangled()
			g.declareHelper(copyP, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %%nb, ptr %%_data.param)", copyP)
			g.indentRaw(1, "ret ptr %nb")
		} else {
			g.indentLine(1, "call void @ll_panic_cstr(ptr null, ptr null, i32 0)")
			g.indentLine(1, "unreachable")
		}
		g.line("}")
		g.line("")

		// 3. Method wrappers
		var methodNames []string
		for idx, im := range vt.Iface.Methods {
			wName := fmt.Sprintf("%s__m_%s", vtInst, im.Name)
			methodNames = append(methodNames, "@"+wName)

			var paramDecls []string
			var callArgs []string
			paramDecls = append(paramDecls, "ptr %_self.param")
			for pi, p := range im.Params {
				pn := fmt.Sprintf("%%_p%d.param", pi)
				pTy := g.llvmType(p.Type)
				if p.Mode == types.ModeMut {
					pTy = "ptr"
				}
				paramDecls = append(paramDecls, fmt.Sprintf("%s %s", pTy, pn))
				callArgs = append(callArgs, fmt.Sprintf("%s %s", pTy, pn))
			}

			retTy := g.llvmType(im.Ret)
			g.line("define %s @%s(%s) {", retTy, wName, strings.Join(paramDecls, ", "))
			g.line("entry:")

			emitPtrContains := func(fn string, itemTy types.Type) {
				p0Ty := g.llvmType(itemTy)
				p0Al := g.typeAlign(itemTy)
				g.indentLine(1, "%%_p0.addr = alloca %s, align %d", p0Ty, p0Al)
				g.indentLine(1, "store %s %%_p0.param, ptr %%_p0.addr, align %d", p0Ty, p0Al)
				g.declareHelper(fn, "i1", "ptr, ptr")
				g.indentLine(1, "%%res = call i1 @%s(ptr %%_self.param, ptr %%_p0.addr)", fn)
				g.indentLine(1, "ret i1 %%res")
				g.line("}")
				g.line("")
			}

			if vec, isVec := vt.Concrete.(*types.Vector); isVec {
				if im.Name == "contains" {
					emitPtrContains("ll_vec_contains_p__"+vec.Mangled(), vec.Elem)
					continue
				}
			}
			if arr, isArr := vt.Concrete.(*types.StackArray); isArr {
				if im.Name == "len" {
					g.indentLine(1, "ret i64 %d", arr.Size)
					g.line("}")
					g.line("")
					continue
				}
				if im.Name == "get" {
					al := g.typeAlign(arr)
					g.indentLine(1, "%%selfVal = load %s, ptr %%_self.param, align %d", cTy, al)
					g.indentLine(1, "%%res = call %s @ll_arr_get__%s(%s %%selfVal, i64 %%_p0.param)", retTy, arr.Mangled(), cTy)
					g.indentLine(1, "ret %s %%res", retTy)
					g.line("}")
					g.line("")
					continue
				}
				if im.Name == "contains" {
					emitPtrContains("ll_arr_contains_p__"+arr.Mangled(), arr.Elem)
					continue
				}
			}
			if setTy, isSet := vt.Concrete.(*types.Set); isSet {
				if im.Name == "contains" {
					emitPtrContains("ll_set_has_p__"+setTy.Mangled(), setTy.Elem)
					continue
				}
			}
			if p, isP := vt.Concrete.(*types.Primitive); isP && p.Kind == types.PrimString {
				if im.Name == "len" {
					g.indentRaw(1, "%lenPtr = getelementptr inbounds %struct.ll_string, ptr %_self.param, i32 0, i32 1")
					g.indentRaw(1, "%lenVal = load i64, ptr %lenPtr, align 8")
					g.indentRaw(1, "ret i64 %lenVal")
					g.line("}")
					g.line("")
					continue
				}
				if im.Name == "get" {
					g.indentRaw(1, "%lenPtr = getelementptr inbounds %struct.ll_string, ptr %_self.param, i32 0, i32 1")
					g.indentRaw(1, "%lenVal = load i64, ptr %lenPtr, align 8")
					g.indentRaw(1, "%neg = icmp slt i64 %_p0.param, 0")
					g.indentRaw(1, "%oob = icmp sge i64 %_p0.param, %lenVal")
					g.indentRaw(1, "%bad = or i1 %neg, %oob")
					g.indentRaw(1, "br i1 %bad, label %out_none, label %out_some")
					g.line("out_none:")
					g.indentLine(1, "ret %s zeroinitializer", retTy)
					g.line("out_some:")
					g.indentRaw(1, "%dataPtrPtr = getelementptr inbounds %struct.ll_string, ptr %_self.param, i32 0, i32 0")
					g.indentRaw(1, "%dataPtr = load ptr, ptr %dataPtrPtr, align 8")
					g.indentRaw(1, "%bytePtr = getelementptr inbounds i8, ptr %dataPtr, i64 %_p0.param")
					g.indentRaw(1, "%byteVal = load i8, ptr %bytePtr, align 1")
					g.indentLine(1, "%%res1 = insertvalue %s zeroinitializer, i1 1, 0", retTy)
					g.indentLine(1, "%%res2 = insertvalue %s %%res1, i8 %%byteVal, 1", retTy)
					g.indentLine(1, "ret %s %%res2", retTy)
					g.line("}")
					g.line("")
					continue
				}
			}
			if mp, isMp := vt.Concrete.(*types.Map); isMp {
				if im.Name == "len" {
					g.indentLine(1, "%%lenPtr = getelementptr inbounds %s, ptr %%_self.param, i32 0, i32 5", cTy)
					g.indentRaw(1, "%lenVal = load i64, ptr %lenPtr, align 8")
					g.indentRaw(1, "ret i64 %lenVal")
					g.line("}")
					g.line("")
					continue
				}
				if im.Name == "get" {
					g.indentLine(1, "%%selfVal = load %s, ptr %%_self.param, align 8", cTy)
					keyTy := g.llvmType(mp.Key)
					g.indentLine(1, "%%res = call %s @ll_map_get__%s(%s %%selfVal, %s %%_p0.param)", retTy, mp.Mangled(), cTy, keyTy)
					g.indentLine(1, "ret %s %%res", retTy)
					g.line("}")
					g.line("")
					continue
				}
				if im.Name == "has" || im.Name == "contains" {
					emitPtrContains("ll_map_has_p__"+mp.Mangled(), mp.Key)
					continue
				}
			}

			fi := vt.Methods[idx]
			targetFn := ""
			if fi != nil {
				targetFn = fi.CName
			}
			if targetFn != "" {
				var recvArg string
				if im.RecvMode == types.ModeMut {
					recvArg = "ptr %_self.param"
				} else {
					al := g.typeAlign(vt.Concrete)
					g.indentLine(1, "%%selfVal = load %s, ptr %%_self.param, align %d", cTy, al)
					recvArg = fmt.Sprintf("%s %%selfVal", cTy)
				}
				allArgs := append([]string{recvArg}, callArgs...)
				if im.Ret.Equals(types.TypeVoid) {
					g.indentLine(1, "call void @%s(%s)", targetFn, strings.Join(allArgs, ", "))
					g.indentLine(1, "ret void")
				} else {
					g.indentLine(1, "%%res = call %s @%s(%s)", retTy, targetFn, strings.Join(allArgs, ", "))
					g.indentLine(1, "ret %s %%res", retTy)
				}
			} else if im.Name == "to_string" {
				g.indentRaw(1, "%_tostr_res = alloca %struct.ll_string, align 8")
				toStrP := "ll_tostr_p__" + vt.Concrete.Mangled()
				g.declareHelper(toStrP, "void", "ptr, ptr")
				g.indentLine(1, "call void @%s(ptr %%_tostr_res, ptr %%_self.param)", toStrP)
				g.indentRaw(1, "%res = load %struct.ll_string, ptr %_tostr_res, align 8")
				g.indentRaw(1, "ret %struct.ll_string %res")
			} else {
				if retTy == "void" {
					g.indentLine(1, "ret void")
				} else {
					g.indentLine(1, "ret %s zeroinitializer", retTy)
				}
			}
			g.line("}")
			g.line("")
		}

		// 4. VTable instance
		var vtElems []string
		vtElems = append(vtElems, "ptr @"+dropName, "ptr @"+cloneName)
		for _, mn := range methodNames {
			vtElems = append(vtElems, "ptr "+mn)
		}
		if g.unitMode == UnitLinkMain {
			g.line("@%s = constant %%struct.%s { %s }, align 8",
				vtInst, vtTypeName, strings.Join(vtElems, ", "))
		} else {
			g.line("@%s = internal constant %%struct.%s { %s }, align 8",
				vtInst, vtTypeName, strings.Join(vtElems, ", "))
		}
		g.line("")
	}
}

// -----------------------------------------------------------------------------
// Entry Point (@main)
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitMain() {
	g.localSlots = make(map[*ir.Local]string)
	g.movedSlots = make(map[string]string)
	g.nextRegID = 0
	g.isTerminated = false
	g.entryAllocas = nil

	var headerSB strings.Builder
	headerSB.WriteString("; Program Entry Point\n")
	headerSB.WriteString("define i32 @main(i32 %argc, ptr %argv) {\n")
	headerSB.WriteString("entry:\n")

	var initSB strings.Builder
	initSB.WriteString("    store i32 %argc, ptr @ll_rt_argc, align 4\n")
	initSB.WriteString("    store ptr %argv, ptr @ll_rt_argv, align 8\n")

	// 2. Initialize top-level `bind` constants in declaration order. Each global's initializer
	// is lowered separately, so its temporaries' names can repeat across globals; prefix them
	// with the global's index to keep the slots in `main` distinct.
	for gi, gl := range g.prog.Globals {
		for _, loc := range gl.Locals {
			slot := fmt.Sprintf("%%g%d.%s.slot", gi, loc.CName)
			g.localSlots[loc] = slot
			headerSB.WriteString(fmt.Sprintf("    %s = alloca %s, align 8\n", slot, g.llvmType(loc.Type)))
			initSB.WriteString(fmt.Sprintf("    store %s zeroinitializer, ptr %s, align 8\n", g.llvmType(loc.Type), slot))
			if loc.MovedFlag != "" {
				flagSlot := fmt.Sprintf("%%g%d.%s.slot", gi, loc.MovedFlag)
				g.movedSlots[loc.MovedFlag] = flagSlot
				if loc.MovedLocal != nil {
					g.localSlots[loc.MovedLocal] = flagSlot
				}
				headerSB.WriteString(fmt.Sprintf("    %s = alloca i1, align 1\n", flagSlot))
				initSB.WriteString(fmt.Sprintf("    store i1 true, ptr %s, align 1\n", flagSlot))
			}
		}
	}

	savedSB := g.sb
	g.sb = strings.Builder{}
	g.currentBlock = "entry"

	for gi, gl := range g.prog.Globals {
		// Moved-flag slots are looked up by name; point the names at this global's slots.
		for _, loc := range gl.Locals {
			if loc.MovedFlag != "" {
				g.movedSlots[loc.MovedFlag] = fmt.Sprintf("%%g%d.%s.slot", gi, loc.MovedFlag)
			}
		}
		for _, in := range gl.InitInstr {
			g.emitInstr(in)
		}
		if gl.InitExpr != nil {
			initVal := g.emitExpr(gl.InitExpr)
			g.indentLine(1, "store %s %s, ptr @%s, align 8", g.llvmType(gl.Type), initVal, gl.CName)
		}
	}

	if g.prog.TestMode {
		// Test execution mode
		if g.prog.TestMainFunc != nil {
			if g.prog.TestMainFunc.RetType == nil || g.prog.TestMainFunc.RetType.Equals(types.TypeVoid) {
				g.indentLine(1, "call void @%s()", g.prog.TestMainFunc.CName)
				g.emitMainCleanup("0")
			} else {
				rc := g.newReg()
				g.indentLine(1, "%s = call %s @%s()", rc, g.llvmType(g.prog.TestMainFunc.RetType), g.prog.TestMainFunc.CName)
				rcI32 := g.emitCastOp(rc, g.prog.TestMainFunc.RetType, types.TypeI32)
				g.emitMainCleanup(rcI32)
			}
		} else if len(g.prog.TestFuncs) == 0 {
			noTestsEntry := g.internString("No tests found.\n")
			g.indentLine(1, "call i32 (ptr, ...) @printf(ptr %s)", noTestsEntry.symbol)
			g.emitMainCleanup("0")
		} else {
			// Run each test function via test runner
			passSlot := g.allocaEntry("i32", 4)
			failSlot := g.allocaEntry("i32", 4)
			g.indentLine(1, "store i32 0, ptr %s, align 4", passSlot)
			g.indentLine(1, "store i32 0, ptr %s, align 4", failSlot)

			for _, fn := range g.prog.TestFuncs {
				desc := fn.TestDesc
				if desc == "" {
					desc = fn.Name
				}
				descEntry := g.internString(desc)
				resReg := g.newReg()
				g.indentLine(1, "%s = call i32 @ll_run_test_case(ptr @%s, ptr %s)", resReg, fn.CName, descEntry.symbol)
				isFail := g.newReg()
				g.indentLine(1, "%s = icmp ne i32 %s, 0", isFail, resReg)

				passBlock := g.newBlockLabel("test.pass")
				failBlock := g.newBlockLabel("test.fail")
				mergeBlock := g.newBlockLabel("test.merge")
				g.indentLine(1, "br i1 %s, label %%%s, label %%%s", isFail, failBlock, passBlock)

				g.emitLabel(failBlock)
				currF := g.newReg()
				nextF := g.newReg()
				g.indentLine(1, "%s = load i32, ptr %s, align 4", currF, failSlot)
				g.indentLine(1, "%s = add i32 %s, 1", nextF, currF)
				g.indentLine(1, "store i32 %s, ptr %s, align 4", nextF, failSlot)
				g.indentLine(1, "br label %%%s", mergeBlock)

				g.emitLabel(passBlock)
				currP := g.newReg()
				nextP := g.newReg()
				g.indentLine(1, "%s = load i32, ptr %s, align 4", currP, passSlot)
				g.indentLine(1, "%s = add i32 %s, 1", nextP, currP)
				g.indentLine(1, "store i32 %s, ptr %s, align 4", nextP, passSlot)
				g.indentLine(1, "br label %%%s", mergeBlock)

				g.emitLabel(mergeBlock)
			}

			finP := g.newReg()
			finF := g.newReg()
			g.indentLine(1, "%s = load i32, ptr %s, align 4", finP, passSlot)
			g.indentLine(1, "%s = load i32, ptr %s, align 4", finF, failSlot)
			g.indentLine(1, "call void @ll_report_test_summary(i32 %s, i32 %s, i32 %d)", finP, finF, len(g.prog.TestFuncs))

			hasFailed := g.newReg()
			rc := g.newReg()
			g.indentLine(1, "%s = icmp sgt i32 %s, 0", hasFailed, finF)
			g.indentLine(1, "%s = select i1 %s, i32 1, i32 0", rc, hasFailed)
			g.emitMainCleanup(rc)
		}
	} else {
		// Normal execution mode
		if g.prog.MainFunc != nil {
			if g.prog.MainFunc.RetType == nil || g.prog.MainFunc.RetType.Equals(types.TypeVoid) {
				g.indentLine(1, "call void @%s()", g.prog.MainFunc.CName)
				g.emitMainCleanup("0")
			} else {
				rc := g.newReg()
				g.indentLine(1, "%s = call %s @%s()", rc, g.llvmType(g.prog.MainFunc.RetType), g.prog.MainFunc.CName)
				rcI32 := g.emitCastOp(rc, g.prog.MainFunc.RetType, types.TypeI32)
				g.emitMainCleanup(rcI32)
			}
		} else {
			g.emitMainCleanup("0")
		}
	}

	mainBodyStr := g.sb.String()
	g.sb = savedSB

	g.sb.WriteString(headerSB.String())
	for _, alloc := range g.entryAllocas {
		g.sb.WriteString(alloc)
		g.sb.WriteByte('\n')
	}
	g.sb.WriteString(initSB.String())
	g.sb.WriteString(mainBodyStr)
	g.line("}")
	g.line("")
}

func (g *llvmEmitter) emitMainCleanup(rcVal string) {
	// Drop top-level globals in reverse declaration order
	for i := len(g.prog.Globals) - 1; i >= 0; i-- {
		gl := g.prog.Globals[i]
		if !gl.Type.IsPlain() {
			g.emitTypeDrop("@"+gl.CName, gl.Type)
		}
	}
	isZero := g.newReg()
	lCheck := g.newBlockLabel("exit.leak_check")
	lDone := g.newBlockLabel("exit.ret")
	g.indentLine(1, "%s = icmp eq i32 %s, 0", isZero, rcVal)
	g.indentLine(1, "br i1 %s, label %%%s, label %%%s", isZero, lCheck, lDone)
	g.line("%s:", lCheck)
	g.indentLine(1, "call void @ll_check_leaks_at_exit()")
	g.indentLine(1, "br label %%%s", lDone)
	g.line("%s:", lDone)
	g.indentLine(1, "ret i32 %s", rcVal)
	g.isTerminated = true
}
