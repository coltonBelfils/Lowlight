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

type companionDecl struct {
	fn         string
	retTy      string
	paramTypes string
}

type llvmEmitter struct {
	prog           *ir.Program
	sb             strings.Builder
	strings        map[string]*stringLitEntry
	stringList     []*stringLitEntry
	nextStrID      int
	nextRegID      int
	nextBlockID    int
	isTerminated   bool
	currentBlock   string
	localSlots     map[*ir.Local]string
	movedSlots     map[string]string
	declaredFns    map[string]bool
	declaredTyps   map[string]bool
	funcSet        map[string]bool
	companionDecls []companionDecl
	structMap      map[string]*types.Struct
	lambdaEnvMap   map[string]*sema.LambdaInfo
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
		g.sb.WriteString(format)
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

	// 2. Struct and Composite Type Definitions
	g.emitTypeDefs()

	// 3. Collect String Literals from the Program
	g.collectAllStrings()

	// 4. Emit String Literals Pool
	g.emitStringPool()

	// 5. External and Companion Runtime Declarations
	g.emitDeclarations()

	// 6. Top-Level Global Constants Storage
	g.emitGlobals()

	// 7. Lowered MIR Function and Method Bodies
	g.line("; Lowered Lowlight Functions")
	for _, fn := range g.prog.Funcs {
		g.emitFunction(fn)
	}

	// 8. Static Function Closure Thunks
	g.emitStaticThunks()

	// 9. Interface VTable Method Wrappers and Instances
	g.emitVTables()

	// 10. Main Entry Point / Test Harness Runner
	if g.prog.MainFunc != nil || g.prog.TestMode {
		g.emitMain()
	}

	// 9. Synthesized Companion Runtime Helper Declarations
	if len(g.companionDecls) > 0 {
		g.line("; Synthesized Companion Helper Declarations")
		for _, cd := range g.companionDecls {
			if g.funcSet[cd.fn] {
				continue
			}
			g.line("declare %s @%s(%s)", cd.retTy, cd.fn, cd.paramTypes)
		}
		g.line("")
	}
}

func (g *llvmEmitter) emitHeader() {
	g.line("; Lowlight LLVM IR Module")
	if runtime.GOARCH == "arm64" {
		if runtime.GOOS == "darwin" {
			g.line("target datalayout = %q", "e-m:o-i64:64-i128:128-n32:64-S128")
			g.line("target triple = %q", "arm64-apple-macosx15.0.0")
		} else {
			g.line("target datalayout = %q", "e-m:e-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128")
			g.line("target triple = %q", "aarch64-unknown-linux-gnu")
		}
	} else {
		if runtime.GOOS == "darwin" {
			g.line("target datalayout = %q", "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-f80:128-n8:16:32:64-S128")
			g.line("target triple = %q", "x86_64-apple-macosx15.0.0")
		} else {
			g.line("target datalayout = %q", "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-f80:128-n8:16:32:64-S128")
			g.line("target triple = %q", "x86_64-unknown-linux-gnu")
		}
	}
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
	case *types.Map:
		return "%struct." + CType(v)
	case *types.Set:
		return "%struct." + CType(v)
	case *types.Func:
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
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Enum:
			if v.IsTagged {
				// Compute max payload size in 64-bit words (minimum 2 words = 16 bytes for pointer/int pairs)
				maxPayloadWords := 2
				for _, vr := range v.Variants {
					switch vr.Kind {
					case types.VariantTuple:
						w := (g.typeSize(vr.Payload) + 7) / 8
						if w > maxPayloadWords {
							maxPayloadWords = w
						}
					case types.VariantStruct:
						tot := 0
						for _, sf := range vr.Fields {
							tot += g.typeSize(sf.Type)
						}
						w := (tot + 7) / 8
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
				g.line("%%struct.%s = type { i1, %s }", cn, g.llvmType(v.Elem))
			}
		case *types.StackArray:
			cn := CType(v)
			g.line("%%struct.%s = type { [%d x %s] }", cn, v.Size, g.llvmType(v.Elem))
		case *types.Vector:
			cn := CType(v)
			g.line("%%struct.%s = type { ptr, i64, i64 }", cn)
		case *types.Set:
			cn := CType(v)
			g.line("%%struct.%s_ent = type { i1, i64, %s }", cn, g.llvmType(v.Elem))
			g.line("%%struct.%s = type { ptr, i64, ptr, i64, i64, i64 }", cn)
		case *types.Map:
			cn := CType(v)
			g.line("%%struct.%s_ent = type { i1, i64, %s, %s }", cn, g.llvmType(v.Key), g.llvmType(v.Value))
			g.line("%%struct.%s = type { ptr, i64, ptr, i64, i64, i64 }", cn)
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
			switch vr.Kind {
			case types.VariantTuple:
				w := (g.typeSize(vr.Payload) + 7) / 8
				if w > maxPayloadWords {
					maxPayloadWords = w
				}
			case types.VariantStruct:
				tot := 0
				for _, sf := range vr.Fields {
					tot += g.typeSize(sf.Type)
				}
				w := (tot + 7) / 8
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
	case *types.Map, *types.Set:
		return 48
	case *types.Func:
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

	// Collect string literals from all expressions across all functions and globals
	for _, gl := range g.prog.Globals {
		g.collectExprStrings(gl.InitExpr)
		for _, instr := range gl.InitInstr {
			g.collectInstrStrings(instr)
		}
	}
	for _, fn := range g.prog.Funcs {
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
	case *ir.ExprConstructVector:
		for _, el := range v.Elems {
			g.collectExprStrings(el)
		}
	case *ir.ExprConstructMap:
		for _, ent := range v.Entries {
			g.collectExprStrings(ent.Key)
			g.collectExprStrings(ent.Val)
		}
	case *ir.ExprConstructSet:
		for _, el := range v.Elems {
			g.collectExprStrings(el)
		}
	case *ir.ExprConstructClosure:
		g.collectExprStrings(v.EnvPtr)
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
	case *ir.ExprStringConcat:
		g.collectExprStrings(v.Left)
		g.collectExprStrings(v.Right)
	case *ir.ExprStringPush:
		g.collectExprStrings(v.StrPtr)
		g.collectExprStrings(v.Item)
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
	case *ir.ExprMapInsert:
		g.collectExprStrings(v.MapPtr)
		g.collectExprStrings(v.Key)
		g.collectExprStrings(v.Val)
	case *ir.ExprSetInsert:
		g.collectExprStrings(v.SetPtr)
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

func (g *llvmEmitter) emitDeclarations() {
	g.line("; External C & Runtime Declarations")
	g.line("@ll_rt_argc = external global i32")
	g.line("@ll_rt_argv = external global ptr")
	g.line("@ll_rt_live_allocs = external global i64")
	g.line("")

	g.declareBuiltin("ll_alloc", "declare ptr @ll_alloc(i64)")
	g.declareBuiltin("ll_realloc", "declare ptr @ll_realloc(ptr, i64)")
	g.declareBuiltin("ll_free", "declare void @ll_free(ptr)")
	g.declareBuiltin("ll_check_bounds", "declare i64 @ll_check_bounds(i64, i64, ptr, i64)")
	g.declareBuiltin("ll_panic_cstr", "declare void @ll_panic_cstr(ptr, ptr, i32)")
	g.declareBuiltin("ll_check_leaks_at_exit", "declare void @ll_check_leaks_at_exit()")
	g.declareBuiltin("ll_copy_bytes", "declare void @ll_copy_bytes(ptr, ptr, i64)")
	g.declareBuiltin("ll_zero_bytes", "declare void @ll_zero_bytes(ptr, i64)")
	g.line("")


	g.declareBuiltin("ll_print_str_p", "declare void @ll_print_str_p(ptr, i32, i32)")
	g.declareBuiltin("ll_panic_str_p", "declare void @ll_panic_str_p(ptr, ptr, i32)")
	g.declareBuiltin("ll_str_lit_p", "declare void @ll_str_lit_p(ptr, ptr, i64)")
	g.declareBuiltin("ll_str_concat_p", "declare void @ll_str_concat_p(ptr, ptr, ptr)")
	g.declareBuiltin("ll_str_clone_p", "declare void @ll_str_clone_p(ptr, ptr)")
	g.declareBuiltin("ll_str_free_p", "declare void @ll_str_free_p(ptr)")
	g.declareBuiltin("ll_str_eq_p", "declare i1 @ll_str_eq_p(ptr, ptr)")
	g.declareBuiltin("ll_str_cmp_p", "declare i64 @ll_str_cmp_p(ptr, ptr)")
	g.declareBuiltin("ll_str_utf8_next_p", "declare i32 @ll_str_utf8_next_p(ptr, i64, ptr)")
	g.declareBuiltin("ll_str_trim_p", "declare void @ll_str_trim_p(ptr, ptr)")
	g.declareBuiltin("ll_str_to_upper_p", "declare void @ll_str_to_upper_p(ptr, ptr)")
	g.declareBuiltin("ll_str_to_lower_p", "declare void @ll_str_to_lower_p(ptr, ptr)")
	g.declareBuiltin("ll_str_replace_p", "declare void @ll_str_replace_p(ptr, ptr, ptr, ptr)")
	g.declareBuiltin("ll_str_contains_p", "declare i1 @ll_str_contains_p(ptr, ptr)")
	g.declareBuiltin("ll_str_starts_with_p", "declare i1 @ll_str_starts_with_p(ptr, ptr)")
	g.declareBuiltin("ll_str_ends_with_p", "declare i1 @ll_str_ends_with_p(ptr, ptr)")
	g.declareBuiltin("ll_str_push_char_p", "declare void @ll_str_push_char_p(ptr, i32)")
	g.declareBuiltin("ll_str_push_byte_p", "declare void @ll_str_push_byte_p(ptr, i8)")
	g.declareBuiltin("ll_str_push_bytes_p", "declare void @ll_str_push_bytes_p(ptr, ptr, i64)")
	g.declareBuiltin("ll_str_clear_p", "declare void @ll_str_clear_p(ptr)")
	g.declareBuiltin("ll_read_file_p", "declare void @ll_read_file_p(ptr, ptr)")
	g.declareBuiltin("ll_write_file_p", "declare i1 @ll_write_file_p(ptr, ptr)")
	g.declareBuiltin("ll_exit", "declare void @ll_exit(i64)")
	g.line("")

	g.declareBuiltin("sqrt", "declare double @sqrt(double)")
	g.declareBuiltin("fabs", "declare double @fabs(double)")
	g.declareBuiltin("puts", "declare i32 @puts(ptr)")
	g.declareBuiltin("printf", "declare i32 @printf(ptr, ...)")
	g.declareBuiltin("fflush", "declare i32 @fflush(ptr)")
	g.line("")

	g.declareBuiltin("llvm.memcpy.p0.p0.i64", "declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)")
	g.declareBuiltin("llvm.memset.p0.i64", "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)")
	g.line("")

	g.declareBuiltin("ll_run_test_case", "declare i32 @ll_run_test_case(ptr, ptr)")
	g.declareBuiltin("ll_report_test_summary", "declare void @ll_report_test_summary(i32, i32, i32)")
	g.line("")

	// User extern functions
	if len(g.prog.ExternFuncs) > 0 {
		g.line("; User Declared Extern Functions")
		for _, ef := range g.prog.ExternFuncs {
			var paramTypes []string
			for _, p := range ef.Sig.Params {
				paramTypes = append(paramTypes, g.llvmType(p.Type))
			}
			retTy := g.llvmType(ef.Sig.Ret)
			g.declareBuiltin(ef.Name, fmt.Sprintf("declare %s @%s(%s)", retTy, ef.Name, strings.Join(paramTypes, ", ")))
		}
		g.line("")
	}
}

func (g *llvmEmitter) emitGlobals() {
	if len(g.prog.Globals) == 0 {
		return
	}
	g.line("; Top-Level Constants")
	for _, gl := range g.prog.Globals {
		g.line("@%s = internal global %s zeroinitializer, align 8", gl.CName, g.llvmType(gl.Type))
	}
	g.line("")
}

// -----------------------------------------------------------------------------
// Functions & Instructions
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitFunction(fn *ir.Function) {
	g.localSlots = make(map[*ir.Local]string)
	g.movedSlots = make(map[string]string)
	g.nextRegID = 0
	g.isTerminated = false

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

	g.line("define %s @%s(%s) {", retTy, fn.CName, strings.Join(paramDecls, ", "))
	g.line("entry:")
	g.currentBlock = "entry"

	if fn.IsLambda {
		g.indentRaw(1, "%_ll_env.slot = alloca ptr, align 8")
		g.indentRaw(1, "store ptr %_ll_env.param, ptr %_ll_env.slot, align 8")
	}

	// 1. Allocate stack slots for all parameters
	for _, p := range fn.Params {
		slot := fmt.Sprintf("%%%s.slot", p.CName)
		g.localSlots[p] = slot
		pTy := g.llvmType(p.Type)
		if p.IsMutPtr {
			pTy = "ptr"
		}
		g.indentLine(1, "%s = alloca %s, align 8", slot, pTy)
		g.indentLine(1, "store %s %%%s.param, ptr %s, align 8", pTy, p.CName, slot)
		if p.MovedFlag != "" {
			flagSlot := fmt.Sprintf("%%%s.slot", p.MovedFlag)
			g.movedSlots[p.MovedFlag] = flagSlot
			if p.MovedLocal != nil {
				g.localSlots[p.MovedLocal] = flagSlot
			}
			g.indentLine(1, "%s = alloca i1, align 1", flagSlot)
			g.indentLine(1, "store i1 false, ptr %s, align 1", flagSlot)
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
		g.indentLine(1, "%s = alloca %s, align 8", slot, locTy)
		// Zero-initialize slot
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", locTy, slot)

		if loc.MovedFlag != "" {
			flagSlot := fmt.Sprintf("%%%s.slot", loc.MovedFlag)
			g.movedSlots[loc.MovedFlag] = flagSlot
			if loc.MovedLocal != nil {
				g.localSlots[loc.MovedLocal] = flagSlot
			}
			g.indentLine(1, "%s = alloca i1, align 1", flagSlot)
			g.indentLine(1, "store i1 true, ptr %s, align 1", flagSlot)
		}
	}

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

	g.line("}")
	g.line("")
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
			if _, isPtr := v.Expr.Type().(*types.Ptr); isPtr {
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
		align := 8
		if v.Dst.Type() != nil && v.Dst.Type().Equals(types.TypeBool) {
			align = 1
		}
		g.indentLine(1, "store %s %s, ptr %s, align %d", g.llvmType(v.Dst.Type()), val, addr, align)

	case *ir.InstrCompoundAssign:
		addr := g.emitLValueAddr(v.Dst)
		currVal := g.emitLoad(addr, v.Dst.Type())
		rhsVal := g.emitExpr(v.Expr)
		resVal := g.emitBinaryOp(v.Op, currVal, rhsVal, v.Dst.Type())
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(v.Dst.Type()), resVal, addr)

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
			g.declareCompanionHelper("ll_drop__Arena", "void", "ptr")
			g.indentLine(1, "call void @ll_drop__Arena(ptr %s)", ptrSlot)
		}
	case *types.Vector, *types.Map, *types.Set, *types.Struct, *types.Interface, *types.StackArray, *types.Optional, *types.Func:
		fn := ir.DropFnName(v)
		g.declareCompanionHelper(fn, "void", "ptr")
		g.indentLine(1, "call void @%s(ptr %s)", fn, ptrSlot)
	case *types.Enum:
		if v.IsTagged {
			fn := ir.DropFnName(v)
			g.declareCompanionHelper(fn, "void", "ptr")
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
		g.declareCompanionHelper("ll_copy_p__string", "void", "ptr, ptr")
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
			pSrc := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", pSrc, srcAddr)
			isNotNull := g.newReg()
			g.indentLine(1, "%s = icmp ne ptr %s, null", isNotNull, pSrc)
			lSome := g.newBlockLabel("opt.copy.some")
			lNone := g.newBlockLabel("opt.copy.none")
			lCont := g.newBlockLabel("opt.copy.cont")
			g.indentLine(1, "br i1 %s, label %%%s, label %%%s", isNotNull, lSome, lNone)
			g.isTerminated = true
			g.emitLabel(lSome)
			sz := g.typeSize(opt.Elem)
			allocReg := g.newReg()
			g.indentLine(1, "%s = call ptr @ll_alloc(i64 %d)", allocReg, sz)
			g.emitCopyVal(allocReg, pSrc, opt.Elem)
			g.indentLine(1, "store ptr %s, ptr %s, align 8", allocReg, destAddr)
			g.indentLine(1, "br label %%%s", lCont)
			g.isTerminated = true
			g.emitLabel(lNone)
			g.indentLine(1, "store ptr null, ptr %s, align 8", destAddr)
			g.indentLine(1, "br label %%%s", lCont)
			g.isTerminated = true
			g.emitLabel(lCont)
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
	if d, ok := t.(*types.Distinct); ok {
		g.emitCopyVal(destAddr, srcAddr, d.Base)
		return
	}
	fn := "ll_copy_p__" + t.Mangled()
	g.declareCompanionHelper(fn, "void", "ptr, ptr")
	g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, destAddr, srcAddr)
}

func (g *llvmEmitter) declareCompanionHelper(fn, retTy, paramTypes string) {
	if g.funcSet[fn] || g.declaredFns[fn] {
		return
	}
	g.declaredFns[fn] = true
	g.companionDecls = append(g.companionDecls, companionDecl{
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
	case *types.Vector:
		switch fieldName {
		case "data":
			return 0
		case "len":
			return 1
		case "cap":
			return 2
		}
	case *types.Map, *types.Set:
		switch fieldName {
		case "buckets":
			return 0
		case "buck_cap":
			return 1
		case "entries":
			return 2
		case "ent_len":
			return 3
		case "ent_cap":
			return 4
		case "len":
			return 5
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
		} else {
			baseAddr = g.emitLValueAddr(v.Base)
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
		tmpSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", tmpSlot, g.llvmType(e.Type()))
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(e.Type()), val, tmpSlot)
		return tmpSlot

	case *ir.ExprDeref:
		return g.emitExpr(v.Ptr)

	default:
		// For non-memory places, allocate a temporary stack slot
		val := g.emitExpr(e)
		tmpSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", tmpSlot, g.llvmType(e.Type()))
		g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(e.Type()), val, tmpSlot)
		return tmpSlot
	}
}

func (g *llvmEmitter) emitLoad(addr string, t types.Type) string {
	r := g.newReg()
	g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(t), addr)
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
		sSlot := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", sSlot)
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
		optSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", optSlot, g.llvmType(v.Type()))
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
		zSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", zSlot, tyStr)
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
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.Local.Type), slot)
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
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.Typ), ptrVal)
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
			case token.Plus:
				resSlot := g.newReg()
				g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
				g.indentLine(1, "call void @ll_str_concat_p(ptr %s, ptr %s, ptr %s)", resSlot, leftAddr, rightAddr)
				r := g.newReg()
				g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
				return r
			}
		}
		if v.Left.Type() != nil && !g.isScalar(v.Left.Type()) && !g.isFloat(v.Left.Type()) && (v.Op == token.Eq || v.Op == token.Neq) {
			leftAddr := g.emitLValueAddr(v.Left)
			rightAddr := g.emitLValueAddr(v.Right)
			fn := "ll_eq_p__" + v.Left.Type().Mangled()
			g.declareCompanionHelper(fn, "i1", "ptr, ptr")
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
		return g.emitBinaryOp(v.Op, leftVal, rightVal, v.Left.Type())

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
		sSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", sSlot, g.llvmType(v.StructType))
		for _, f := range v.Fields {
			fVal := g.emitExpr(f.Value)
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
				fp, g.llvmType(v.StructType), sSlot, f.Index)
			g.indentLine(1, "store %s %s, ptr %s, align 8", g.llvmType(f.Value.Type()), fVal, fp)
		}
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(v.StructType), sSlot)
		return r

	case *ir.ExprConstructEnum:
		if !v.EnumType.IsTagged {
			return fmt.Sprintf("%d", v.Variant.Value)
		}
		eSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", eSlot, g.llvmType(v.EnumType))
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
		arrSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", arrSlot, g.llvmType(v.ArrayType))
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

	case *ir.ExprConstructVector:
		vecSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", vecSlot, g.llvmType(v.VecType))
		g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(v.VecType), vecSlot)
		if len(v.Elems) > 0 {
			resFn := "ll_vec_reserve__" + v.VecType.Mangled()
			g.declareCompanionHelper(resFn, "void", "ptr, i64")
			g.indentLine(1, "call void @%s(ptr %s, i64 %d)", resFn, vecSlot, len(v.Elems))
			pushFn := "ll_vec_push_p__" + v.VecType.Mangled()
			g.declareCompanionHelper(pushFn, "void", "ptr, ptr")
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
			g.declareCompanionHelper(entryFn, "void", "...")
		}
		if v.CopyFn != "" && !g.funcSet[v.CopyFn] {
			g.declareCompanionHelper(v.CopyFn, "ptr", "ptr")
		}
		if v.DropFn != "" && !g.funcSet[v.DropFn] {
			g.declareCompanionHelper(v.DropFn, "void", "ptr")
		}

		closSlot := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_closure, align 8", closSlot)
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
		optSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", optSlot, g.llvmType(v.OptType))
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
		vecSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", vecSlot, g.llvmType(v.VecType))
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
		ifcSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align 8", ifcSlot, g.llvmType(v.IfcType))
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
		r := g.newReg()
		g.indentLine(1, "%s = call ptr @ll_alloc(i64 %s)", r, totBytes)
		return r

	case *ir.ExprArenaAlloc:
		arenaPtr := g.emitExpr(v.Arena)
		sz := g.typeSize(v.AllocType)
		dtorVal := "null"
		if v.DtorFn != "" {
			g.declareCompanionHelper(v.DtorFn, "void", "ptr")
			dtorVal = "@" + v.DtorFn
		}
		g.declareCompanionHelper("ll_arena_alloc", "ptr", "ptr, i64, i64, ptr")
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
		g.indentLine(1, "call void @llvm.memcpy.p0.p0.i64(ptr %s, ptr %s, i64 %s, i1 false)", dstVal, srcVal, cntVal)
		return "void"

	case *ir.ExprSizeOf:
		return fmt.Sprintf("%d", g.typeSize(v.QueryType))

	case *ir.ExprAlignOf:
		return fmt.Sprintf("%d", g.typeAlign(v.QueryType))

	case *ir.ExprStringConcat:
		leftAddr := g.emitLValueAddr(v.Left)
		rightAddr := g.emitLValueAddr(v.Right)
		resSlot := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
		g.indentLine(1, "call void @ll_str_concat_p(ptr %s, ptr %s, ptr %s)", resSlot, leftAddr, rightAddr)
		r := g.newReg()
		g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
		return r

	case *ir.ExprStringPush:
		strPtr := g.emitExpr(v.StrPtr)
		suffixAddr := g.emitLValueAddr(v.Item)
		g.declareCompanionHelper("ll_str_push_p", "void", "ptr, ptr")
		g.indentLine(1, "call void @ll_str_push_p(ptr %s, ptr %s)", strPtr, suffixAddr)
		return "void"

	case *ir.ExprToString:
		srcAddr := g.emitLValueAddr(v.Val)
		fn := "ll_tostr_p__" + v.ValType.Mangled()
		g.declareCompanionHelper(fn, "void", "ptr, ptr")
		resSlot := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
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
		resSlot := g.newReg()
		g.indentLine(1, "%s = alloca %s, align %d", resSlot, g.llvmType(v.ValType), g.typeAlign(v.ValType))
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
		g.declareCompanionHelper(fn, "i1", "ptr, ptr")
		r := g.newReg()
		g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, fn, leftAddr, rightAddr)
		return r

	case *ir.ExprCmp:
		leftAddr := g.emitLValueAddr(v.Left)
		rightAddr := g.emitLValueAddr(v.Right)
		fn := "ll_cmp_p__" + v.ValType.Mangled()
		g.declareCompanionHelper(fn, "i64", "ptr, ptr")
		r := g.newReg()
		g.indentLine(1, "%s = call i64 @%s(ptr %s, ptr %s)", r, fn, leftAddr, rightAddr)
		return r

	case *ir.ExprHash:
		if v.ValType != nil && v.ValType.Equals(types.TypeString) {
			addr := g.emitLValueAddr(v.Val)
			r := g.newReg()
			g.declareCompanionHelper("ll_str_hash_p", "i64", "ptr")
			g.indentLine(1, "%s = call i64 @ll_str_hash_p(ptr %s)", r, addr)
			return r
		}
		val := g.emitExpr(v.Val)
		fn := ir.HashFnName(v.ValType)
		g.declareCompanionHelper(fn, "i64", g.llvmType(v.ValType))
		r := g.newReg()
		g.indentLine(1, "%s = call i64 @%s(%s %s)", r, fn, g.llvmType(v.ValType), val)
		return r

	case *ir.ExprVecPush:
		vecAddr := g.emitExpr(v.VecPtr)
		itemAddr := g.emitLValueAddr(v.Item)
		fn := "ll_vec_push_p__" + v.VecType.Mangled()
		g.declareCompanionHelper(fn, "void", "ptr, ptr")
		g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, vecAddr, itemAddr)
		return "void"

	case *ir.ExprMapInsert:
		mapAddr := g.emitExpr(v.MapPtr)
		keyAddr := g.emitLValueAddr(v.Key)
		valAddr := g.emitLValueAddr(v.Val)
		fn := "ll_map_insert_p__" + v.MapType.Mangled()
		g.declareCompanionHelper(fn, "void", "ptr, ptr, ptr")
		g.indentLine(1, "call void @%s(ptr %s, ptr %s, ptr %s)", fn, mapAddr, keyAddr, valAddr)
		return "void"

	case *ir.ExprSetInsert:
		setAddr := g.emitExpr(v.SetPtr)
		itemAddr := g.emitLValueAddr(v.Item)
		fn := "ll_set_insert_p__" + v.SetType.Mangled()
		g.declareCompanionHelper(fn, "void", "ptr, ptr")
		g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, setAddr, itemAddr)
		return "void"

	default:
		return "0"
	}
}

// -----------------------------------------------------------------------------
// Operation Emitters (Binary, Unary, Cast, Call)
// -----------------------------------------------------------------------------

func (g *llvmEmitter) isIntegerType(t types.Type) bool {
	if prim, ok := t.(*types.Primitive); ok {
		return prim.IsInteger()
	}
	return false
}

func (g *llvmEmitter) isFloat(t types.Type) bool {
	if prim, ok := t.(*types.Primitive); ok {
		return prim.Kind == types.PrimF32 || prim.Kind == types.PrimF64
	}
	return false
}

func (g *llvmEmitter) isUnsigned(t types.Type) bool {
	if prim, ok := t.(*types.Primitive); ok {
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
		return v.Kind != types.PrimVoid && !g.isFloat(t)
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

func (g *llvmEmitter) emitBinaryOp(op token.Kind, lVal, rVal string, t types.Type) string {
	if t != nil && t.Equals(types.TypeString) {
		slotL := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", slotL)
		g.indentLine(1, "store %%struct.ll_string %s, ptr %s, align 8", lVal, slotL)

		slotR := g.newReg()
		g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", slotR)
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
		case token.Plus:
			slotRes := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", slotRes)
			g.indentLine(1, "call void @ll_str_concat_p(ptr %s, ptr %s, ptr %s)", slotRes, slotL, slotR)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, slotRes)
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
		} else if isSigned {
			g.indentLine(1, "%s = sdiv %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = udiv %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	case token.Percent:
		if isSigned {
			g.indentLine(1, "%s = srem %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = urem %s %s, %s", r, tyStr, lVal, rVal)
		}
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
		g.indentLine(1, "%s = shl %s %s, %s", r, tyStr, lVal, rVal)
		return r

	case token.Shr:
		if isSigned {
			g.indentLine(1, "%s = ashr %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			g.indentLine(1, "%s = lshr %s %s, %s", r, tyStr, lVal, rVal)
		}
		return r

	// Comparisons
	case token.Eq:
		if isFloat {
			g.indentLine(1, "%s = fcmp oeq %s %s, %s", r, tyStr, lVal, rVal)
		} else if g.isScalar(t) {
			g.indentLine(1, "%s = icmp eq %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			slotL := g.newReg()
			g.indentLine(1, "%s = alloca %s, align %d", slotL, tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, lVal, slotL, g.typeAlign(t))
			slotR := g.newReg()
			g.indentLine(1, "%s = alloca %s, align %d", slotR, tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, rVal, slotR, g.typeAlign(t))
			fn := "ll_eq_p__" + t.Mangled()
			g.declareCompanionHelper(fn, "i1", "ptr, ptr")
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, fn, slotL, slotR)
		}
		return r

	case token.Neq:
		if isFloat {
			g.indentLine(1, "%s = fcmp one %s %s, %s", r, tyStr, lVal, rVal)
		} else if g.isScalar(t) {
			g.indentLine(1, "%s = icmp ne %s %s, %s", r, tyStr, lVal, rVal)
		} else {
			slotL := g.newReg()
			g.indentLine(1, "%s = alloca %s, align %d", slotL, tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, lVal, slotL, g.typeAlign(t))
			slotR := g.newReg()
			g.indentLine(1, "%s = alloca %s, align %d", slotR, tyStr, g.typeAlign(t))
			g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, rVal, slotR, g.typeAlign(t))
			fn := "ll_eq_p__" + t.Mangled()
			g.declareCompanionHelper(fn, "i1", "ptr, ptr")
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
			fileSym := "null"
			lineVal := "0"
			if len(call.Args) >= 3 {
				fileSym = g.emitExpr(call.Args[1])
				lineVal = g.emitExpr(call.Args[2])
			}
			g.indentLine(1, "call void @ll_panic_str_p(ptr %s, ptr %s, i32 %s)", strAddr, fileSym, lineVal)
			g.indentLine(1, "unreachable")
			g.isTerminated = true
			return "void"
		}
		if fnRef.Name == "ll_arena_new" && len(call.Args) >= 1 {
			capVal := g.emitExpr(call.Args[0])
			arenaSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_arena, align 8", arenaSlot)
			g.declareCompanionHelper("ll_arena_new_p", "void", "ptr, i64")
			g.indentLine(1, "call void @ll_arena_new_p(ptr %s, i64 %s)", arenaSlot, capVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_arena, ptr %s, align 8", r, arenaSlot)
			return r
		}
		if fnRef.Name == "ll_vec_join_str" && len(call.Args) == 2 {
			vecAddr := g.emitLValueAddr(call.Args[0])
			sepAddr := g.emitLValueAddr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_vec_join_str_p", "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @ll_vec_join_str_p(ptr %s, ptr %s, ptr %s)", resSlot, vecAddr, sepAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_os_name" {
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_os_name_p", "void", "ptr")
			g.indentLine(1, "call void @ll_os_name_p(ptr %s)", resSlot)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_os_arch" {
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_os_arch_p", "void", "ptr")
			g.indentLine(1, "call void @ll_os_arch_p(ptr %s)", resSlot)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_os_argv" && len(call.Args) == 1 {
			idxVal := g.emitExpr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_os_argv_p", "void", "ptr, i64")
			g.indentLine(1, "call void @ll_os_argv_p(ptr %s, i64 %s)", resSlot, idxVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_str_from_char" && len(call.Args) == 1 {
			chVal := g.emitExpr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_str_from_char_p", "void", "ptr, i32")
			g.indentLine(1, "call void @ll_str_from_char_p(ptr %s, i32 %s)", resSlot, chVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_str_from_raw" && len(call.Args) == 2 {
			dataVal := g.emitExpr(call.Args[0])
			lenVal := g.emitExpr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_str_from_raw_p", "void", "ptr, ptr, i64")
			g.indentLine(1, "call void @ll_str_from_raw_p(ptr %s, ptr %s, i64 %s)", resSlot, dataVal, lenVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_fmt_float_prec" && len(call.Args) == 2 {
			vVal := g.emitExpr(call.Args[0])
			precVal := g.emitExpr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_fmt_float_prec_p", "void", "ptr, double, i64")
			g.indentLine(1, "call void @ll_fmt_float_prec_p(ptr %s, double %s, i64 %s)", resSlot, vVal, precVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_fmt_exp_f64" && len(call.Args) == 2 {
			vVal := g.emitExpr(call.Args[0])
			precVal := g.emitExpr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_fmt_exp_f64_p", "void", "ptr, double, i64")
			g.indentLine(1, "call void @ll_fmt_exp_f64_p(ptr %s, double %s, i64 %s)", resSlot, vVal, precVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_str_slice_view" && len(call.Args) >= 3 {
			strAddr := g.emitLValueAddr(call.Args[0])
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
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_str_slice_view_p", "void", "ptr, ptr, i64, i64, i32, ptr, i64")
			g.indentLine(1, "call void @ll_str_slice_view_p(ptr %s, ptr %s, i64 %s, i64 %s, i32 %s, ptr %s, i64 %s)",
				resSlot, strAddr, loVal, hiVal, checkVal, fileVal, lineVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_str_slice_owned" && len(call.Args) >= 3 {
			strAddr := g.emitLValueAddr(call.Args[0])
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
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", resSlot)
			g.declareCompanionHelper("ll_str_slice_owned_p", "void", "ptr, ptr, i64, i64, i32, ptr, i64")
			g.indentLine(1, "call void @ll_str_slice_owned_p(ptr %s, ptr %s, i64 %s, i64 %s, i32 %s, ptr %s, i64 %s)",
				resSlot, strAddr, loVal, hiVal, checkVal, fileVal, lineVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %%struct.ll_string, ptr %s, align 8", r, resSlot)
			return r
		}
		if fnRef.Name == "ll_assert" && len(call.Args) >= 4 {
			condVal := g.emitExpr(call.Args[0])
			msgAddr := g.emitLValueAddr(call.Args[1])
			fileVal := g.emitExpr(call.Args[2])
			lineVal := g.emitExpr(call.Args[3])
			g.declareCompanionHelper("ll_assert_p", "void", "i1, ptr, ptr, i64")
			g.indentLine(1, "call void @ll_assert_p(i1 %s, ptr %s, ptr %s, i64 %s)", condVal, msgAddr, fileVal, lineVal)
			return "void"
		}
		if fnRef.Name == "ll_parse_int" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_parse_int_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_parse_int_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_parse_float" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_parse_float_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_parse_float_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_read_file" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_read_file_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_read_file_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_write_file" && len(call.Args) == 2 {
			pathAddr := g.emitLValueAddr(call.Args[0])
			contAddr := g.emitLValueAddr(call.Args[1])
			g.declareCompanionHelper("ll_write_file_p", "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_write_file_p(ptr %s, ptr %s)", r, pathAddr, contAddr)
			return r
		}
		if fnRef.Name == "ll_get_cli_args" {
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_get_cli_args_p", "void", "ptr")
			g.indentLine(1, "call void @ll_get_cli_args_p(ptr %s)", resSlot)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_index_of" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			subAddr := g.emitLValueAddr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_index_of_p", "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @ll_str_index_of_p(ptr %s, ptr %s, ptr %s)", resSlot, strAddr, subAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_contains" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			subAddr := g.emitLValueAddr(call.Args[1])
			g.declareCompanionHelper("ll_str_contains_p", "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_str_contains_p(ptr %s, ptr %s)", r, strAddr, subAddr)
			return r
		}
		if fnRef.Name == "ll_str_starts_with" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			subAddr := g.emitLValueAddr(call.Args[1])
			g.declareCompanionHelper("ll_str_starts_with_p", "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_str_starts_with_p(ptr %s, ptr %s)", r, strAddr, subAddr)
			return r
		}
		if fnRef.Name == "ll_str_ends_with" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			subAddr := g.emitLValueAddr(call.Args[1])
			g.declareCompanionHelper("ll_str_ends_with_p", "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @ll_str_ends_with_p(ptr %s, ptr %s)", r, strAddr, subAddr)
			return r
		}
		if fnRef.Name == "ll_str_trim" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_trim_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_str_trim_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_to_upper" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_to_upper_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_str_to_upper_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_to_lower" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_to_lower_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_str_to_lower_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_replace" && len(call.Args) == 3 {
			strAddr := g.emitLValueAddr(call.Args[0])
			oldAddr := g.emitLValueAddr(call.Args[1])
			newAddr := g.emitLValueAddr(call.Args[2])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_replace_p", "void", "ptr, ptr, ptr, ptr")
			g.indentLine(1, "call void @ll_str_replace_p(ptr %s, ptr %s, ptr %s, ptr %s)", resSlot, strAddr, oldAddr, newAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_get_byte" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			idxVal := g.emitExpr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_get_byte_p", "void", "ptr, ptr, i64")
			g.indentLine(1, "call void @ll_str_get_byte_p(ptr %s, ptr %s, i64 %s)", resSlot, strAddr, idxVal)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_bytes" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_bytes_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_str_bytes_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_chars" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_chars_p", "void", "ptr, ptr")
			g.indentLine(1, "call void @ll_str_chars_p(ptr %s, ptr %s)", resSlot, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_split" && len(call.Args) == 2 {
			strAddr := g.emitLValueAddr(call.Args[0])
			sepAddr := g.emitLValueAddr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper("ll_str_split_p", "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @ll_str_split_p(ptr %s, ptr %s, ptr %s)", resSlot, strAddr, sepAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load %s, ptr %s, align 8", r, g.llvmType(call.Typ), resSlot)
			return r
		}
		if fnRef.Name == "ll_str_c_str" && len(call.Args) == 1 {
			strAddr := g.emitLValueAddr(call.Args[0])
			gep := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %%struct.ll_string, ptr %s, i32 0, i32 0", gep, strAddr)
			r := g.newReg()
			g.indentLine(1, "%s = load ptr, ptr %s, align 8", r, gep)
			return r
		}
		if (fnRef.Name == "ll_str_utf8_next" || fnRef.Name == "ll_str_utf8_next_p") && len(call.Args) == 3 {
			strAddr := g.emitLValueAddr(call.Args[0])
			offVal := g.emitExpr(call.Args[1])
			nextOffAddr := g.emitExpr(call.Args[2])
			g.declareCompanionHelper("ll_str_utf8_next_p", "i32", "ptr, i64, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i32 @ll_str_utf8_next_p(ptr %s, i64 %s, ptr %s)", r, strAddr, offVal, nextOffAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_push__") && len(call.Args) == 2 {
			vecAddr := g.emitAsPtr(call.Args[0])
			itemAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_push__")
			wrapper := "ll_vec_push_p__" + mangled
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s)", wrapper, vecAddr, itemAddr)
			return "void"
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_get__") && len(call.Args) == 2 {
			vecAddr := g.emitAsPtr(call.Args[0])
			idxVal := g.emitExpr(call.Args[1])
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
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
			g.declareCompanionHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, vecAddr, itemAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_vec_pop__") && len(call.Args) == 1 {
			vecAddr := g.emitAsPtr(call.Args[0])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_vec_pop__")
			wrapper := "ll_vec_pop_p__" + mangled
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr")
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
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s)", wrapper, setAddr, keyAddr)
			return "void"
		}
		if strings.HasPrefix(fnRef.Name, "ll_set_has__") && len(call.Args) == 2 {
			setAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_set_has__")
			wrapper := "ll_set_has_p__" + mangled
			g.declareCompanionHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, setAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_set_remove__") && len(call.Args) == 2 {
			setAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_set_remove__")
			wrapper := "ll_set_remove_p__" + mangled
			g.declareCompanionHelper(wrapper, "i1", "ptr, ptr")
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
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr, ptr")
			g.indentLine(1, "call void @%s(ptr %s, ptr %s, ptr %s)", wrapper, mapAddr, keyAddr, valAddr)
			return "void"
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_get__") && len(call.Args) == 2 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_get__")
			wrapper := "ll_map_get_p__" + mangled
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr, ptr")
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
			g.declareCompanionHelper(wrapper, "i1", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, wrapper, mapAddr, keyAddr)
			return r
		}
		if strings.HasPrefix(fnRef.Name, "ll_map_remove__") && len(call.Args) == 2 {
			mapAddr := g.emitAsPtr(call.Args[0])
			keyAddr := g.emitLValueAddr(call.Args[1])
			mangled := strings.TrimPrefix(fnRef.Name, "ll_map_remove__")
			wrapper := "ll_map_remove_p__" + mangled
			resSlot := g.newReg()
			g.indentLine(1, "%s = alloca %s, align 8", resSlot, g.llvmType(call.Typ))
			g.declareCompanionHelper(wrapper, "void", "ptr, ptr, ptr")
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
			g.declareCompanionHelper(fnRef.Name, g.llvmType(call.Typ), strings.Join(argTys, ", "))
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
		var callArgs []string
		paramDecls = append(paramDecls, "ptr %_ll_env.param")
		for idx, p := range st.FuncType.Params {
			pn := fmt.Sprintf("%%_p%d.param", idx)
			pTy := g.llvmType(p.Type)
			if p.Mode == types.ModeMut {
				pTy = "ptr"
			}
			paramDecls = append(paramDecls, fmt.Sprintf("%s %s", pTy, pn))
			callArgs = append(callArgs, fmt.Sprintf("%s %s", pTy, pn))
		}
		retTy := g.llvmType(st.FuncType.Ret)
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

		// 1. drop_box
		dropName := vtInst + "__drop"
		g.line("define void @%s(ptr %%_data.param) {", dropName)
		g.line("entry:")
		g.indentRaw(1, "%isNull = icmp eq ptr %_data.param, null")
		g.indentRaw(1, "br i1 %isNull, label %done, label %doDrop")
		g.line("doDrop:")
		if !vt.Concrete.IsPlain() {
			dropFn := ir.DropFnName(vt.Concrete)
			g.declareCompanionHelper(dropFn, "void", "ptr")
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
			g.declareCompanionHelper(copyP, "void", "ptr, ptr")
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
					g.indentLine(1, "%%selfVal = load %s, ptr %%_self.param, align 8", cTy)
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
				g.declareCompanionHelper(toStrP, "void", "ptr, ptr")
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
		g.line("@%s = internal constant %%struct.%s { %s }, align 8",
			vtInst, vtTypeName, strings.Join(vtElems, ", "))
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

	g.line("; Program Entry Point")
	g.raw("define i32 @main(i32 %argc, ptr %argv) {")
	g.line("entry:")
	g.currentBlock = "entry"

	// 1. Store argc & argv into runtime globals
	g.indentRaw(1, "store i32 %argc, ptr @ll_rt_argc, align 4")
	g.indentRaw(1, "store ptr %argv, ptr @ll_rt_argv, align 8")

	// 2. Initialize top-level `bind` constants in declaration order
	for _, gl := range g.prog.Globals {
		for _, loc := range gl.Locals {
			slot := fmt.Sprintf("%%%s.slot", loc.CName)
			g.localSlots[loc] = slot
			g.indentLine(1, "%s = alloca %s, align 8", slot, g.llvmType(loc.Type))
			g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", g.llvmType(loc.Type), slot)
			if loc.MovedFlag != "" {
				flagSlot := fmt.Sprintf("%%%s.slot", loc.MovedFlag)
				g.movedSlots[loc.MovedFlag] = flagSlot
				if loc.MovedLocal != nil {
					g.localSlots[loc.MovedLocal] = flagSlot
				}
				g.indentLine(1, "%s = alloca i1, align 1", flagSlot)
				g.indentLine(1, "store i1 true, ptr %s, align 1", flagSlot)
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
			// Run each test function via companion runner
			passSlot := g.newReg()
			failSlot := g.newReg()
			g.indentLine(1, "%s = alloca i32, align 4", passSlot)
			g.indentLine(1, "%s = alloca i32, align 4", failSlot)
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
	g.indentLine(1, "call void @ll_check_leaks_at_exit()")
	g.indentLine(1, "ret i32 %s", rcVal)
	g.isTerminated = true
}
