package codegen

import (
	"fmt"
	"strings"

	"lowlight/internal/ir"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// EmitC generates a complete, standalone ISO C11 translation unit from `prog`.
func EmitC(prog *ir.Program) string {
	g := &cEmitter{
		prog:    prog,
		optSet:  make(map[string]bool),
		vecSet:  make(map[string]bool),
	}
	for _, o := range prog.Optionals {
		g.optSet[o.Mangled()] = true
	}
	for _, st := range prog.Structs {
		if st.BoxedOptional {
			g.optSet[types.NewOptional(st).Mangled()] = true
		}
	}
	for _, v := range prog.Vectors {
		g.vecSet[v.Mangled()] = true
	}

	g.emitAll()
	return g.sb.String()
}

type cEmitter struct {
	prog   *ir.Program
	sb     strings.Builder
	optSet map[string]bool
	vecSet map[string]bool
}

func (g *cEmitter) line(format string, args ...any) {
	if len(args) == 0 {
		g.sb.WriteString(format)
		g.sb.WriteByte('\n')
		return
	}
	g.sb.WriteString(fmt.Sprintf(format, args...))
	g.sb.WriteByte('\n')
}

func (g *cEmitter) indentLine(indent int, format string, args ...any) {
	for i := 0; i < indent; i++ {
		g.sb.WriteString("    ")
	}
	g.line(format, args...)
}

func (g *cEmitter) hasOptionalType(o *types.Optional) bool {
	return g.optSet[o.Mangled()]
}

func (g *cEmitter) hasVectorType(v *types.Vector) bool {
	return g.vecSet[v.Mangled()]
}

func (g *cEmitter) emitAll() {
	// 1. Embedded C11 Runtime Header + User `extern "<header>"` includes
	g.sb.WriteString(RuntimeHeaderC11)
	if len(g.prog.ExternHeaders) > 0 {
		g.line("/* User Extern Headers */")
		for _, hdr := range g.prog.ExternHeaders {
			if strings.HasPrefix(hdr, "./") || strings.HasPrefix(hdr, "../") || strings.HasPrefix(hdr, "/") {
				g.line("#include %q", hdr)
			} else {
				g.line("#include <%s>", hdr)
			}
		}
		g.line("")
	}

	// 2. Enum Typedefs & Forward Struct Declarations
	g.emitEnumsAndForwardDecls()

	// 3. Topologically Ordered Concrete Type Definitions
	g.emitOrderedTypeDefs()

	// 4. Lambda Environment Structs
	g.emitLambdaEnvStructs()

	// 5. Forward Function & Helper Prototypes (breaks all mutual cycles cleanly)
	g.emitForwardPrototypes()

	// 6. Synthesized Type Lifecycle Helpers (`drop`, `copy`, `eq`, `hash`, `to_string`, collections)
	g.emitSynthesizedTypeHelpers()
	g.emitBuiltinCompositeWrappers()

	// 7. Lambda Env Helpers, Static Closure Thunks, & Interface VTables
	g.emitLambdaAndThunkHelpers()
	g.emitVTables()

	// 8. Top-Level `bind` Global Constants Storage
	if len(g.prog.Globals) > 0 {
		g.line("/* Top-Level Constants */")
		for _, gl := range g.prog.Globals {
			g.line("static %s %s;", ir.CType(gl.Type), gl.CName)
		}
		g.line("")
	}

	// 9. Lowered MIR Function & Lambda Bodies
	g.line("/* Lowered Lowlight Functions */")
	for _, fn := range g.prog.Funcs {
		g.emitFunction(fn)
	}

	// 10. C11 `main` Entry Point (if `MainFunc` is present)
	if g.prog.MainFunc != nil {
		g.emitCMain()
	}
}

// -----------------------------------------------------------------------------
// Section 2 & 3: Enums, Forward Declarations, & Ordered Struct Definitions
// -----------------------------------------------------------------------------

func (g *cEmitter) emitEnumsAndForwardDecls() {
	if len(g.prog.Enums) > 0 {
		g.line("/* Enum Types */")
		for _, en := range g.prog.Enums {
			g.line("typedef %s %s;", ir.CType(en.Underlying), en.CName)
			for _, v := range en.Variants {
				g.line("#define %s__%s ((%s)%dLL)", en.CName, v.Name, en.CName, v.Value)
			}
		}
		g.line("")
	}

	g.line("/* Forward Struct Declarations */")
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Enum:
			g.line("typedef struct %s %s;", v.CName, v.CName)
		case *types.Struct:
			g.line("typedef struct %s %s;", v.CName, v.CName)
		case *types.Optional:
			if !v.IsBoxed() {
				cn := ir.CType(v)
				g.line("typedef struct %s %s;", cn, cn)
			}
		case *types.StackArray:
			cn := ir.CType(v)
			g.line("typedef struct %s %s;", cn, cn)
		case *types.Vector:
			cn := ir.CType(v)
			g.line("typedef struct %s %s;", cn, cn)
		case *types.Set:
			cn := ir.CType(v)
			g.line("typedef struct %s_ent %s_ent;", cn, cn)
			g.line("typedef struct %s %s;", cn, cn)
		case *types.Map:
			cn := ir.CType(v)
			g.line("typedef struct %s_ent %s_ent;", cn, cn)
			g.line("typedef struct %s %s;", cn, cn)
		case *types.Func:
			cn := ir.CType(v)
			g.line("typedef struct %s %s;", cn, cn)
		case *types.Interface:
			vt := ir.VTableTypeName(v)
			g.line("typedef struct %s %s;", vt, vt)
			g.line("typedef struct %s %s;", v.CName, v.CName)
		}
	}
	for _, t := range g.prog.OrderedTypes {
		if v, ok := t.(*types.Distinct); ok {
			g.line("typedef %s %s;", ir.CType(v.Base), v.CName)
		}
	}
	g.line("")
}

func (g *cEmitter) emitOrderedTypeDefs() {
	g.line("/* Concrete & Monomorphized Type Definitions */")
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Enum:
			hasPayload := false
			for _, vr := range v.Variants {
				if vr.Kind != types.VariantUnit {
					hasPayload = true
					break
				}
			}
			g.line("struct %s {", v.CName)
			g.line("    int32_t tag;")
			if hasPayload {
				g.line("    union {")
				for _, vr := range v.Variants {
					switch vr.Kind {
					case types.VariantTuple:
						g.line("        %s %s;", ir.CType(vr.Payload), vr.CName)
					case types.VariantStruct:
						g.line("        struct {")
						if len(vr.Fields) == 0 {
							g.line("            uint8_t _ll_empty;")
						} else {
							for _, sf := range vr.Fields {
								g.line("            %s %s;", ir.CType(sf.Type), sf.CName)
							}
						}
						g.line("        } %s;", vr.CName)
					}
				}
				g.line("    } data;")
			}
			g.line("};")

		case *types.Struct:
			attr := ""
			if v.Packed {
				attr = " __attribute__((packed))"
			}
			g.line("struct%s %s {", attr, v.CName)
			if len(v.Fields) == 0 {
				g.line("    uint8_t _ll_empty;")
			} else {
				for _, f := range v.Fields {
					g.line("    %s %s;", ir.CType(f.Type), f.CName)
				}
			}
			g.line("};")

		case *types.Optional:
			if !v.IsBoxed() {
				cn := ir.CType(v)
				g.line("struct %s { bool has_value; %s value; };", cn, ir.CType(v.Elem))
			}

		case *types.StackArray:
			cn := ir.CType(v)
			g.line("struct %s { %s data[%d]; };", cn, ir.CType(v.Elem), v.Size)

		case *types.Vector:
			cn := ir.CType(v)
			g.line("struct %s { %s* data; int64_t len; int64_t cap; };", cn, ir.CType(v.Elem))

		case *types.Set:
			cn := ir.CType(v)
			g.line("struct %s { int64_t* buckets; int64_t buck_cap; %s_ent* entries; int64_t ent_len; int64_t ent_cap; int64_t len; };", cn, cn)

		case *types.Map:
			cn := ir.CType(v)
			g.line("struct %s { int64_t* buckets; int64_t buck_cap; %s_ent* entries; int64_t ent_len; int64_t ent_cap; int64_t len; };", cn, cn)

		case *types.Func:
			cn := ir.CType(v)
			pTypes := []string{"void*"}
			for _, p := range v.Params {
				pTypes = append(pTypes, paramCType(p.Mode, p.Type))
			}
			g.line("struct %s {", cn)
			g.line("    %s (*fn)(%s);", ir.CType(v.Ret), strings.Join(pTypes, ", "))
			g.line("    void* env;")
			g.line("    void* (*copy_env)(const void*);")
			g.line("    void (*drop_env)(void*);")
			g.line("};")

		case *types.Interface:
			vt := ir.VTableTypeName(v)
			g.line("struct %s {", vt)
			g.line("    void (*drop_box)(void*);")
			g.line("    void* (*clone_box)(const void*);")
			for _, m := range v.Methods {
				pTypes := []string{"void*"}
				for _, p := range m.Params {
					pTypes = append(pTypes, paramCType(p.Mode, p.Type))
				}
				g.line("    %s (*m_%s)(%s);", ir.CType(m.Ret), m.Name, strings.Join(pTypes, ", "))
			}
			g.line("};")
			g.line("struct %s { void* data; const %s* vtable; bool owned; };", v.CName, vt)
		}
	}
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Set:
			cn := ir.CType(v)
			g.line("struct %s_ent { bool alive; uint64_t hash; %s key; };", cn, ir.CType(v.Elem))
		case *types.Map:
			cn := ir.CType(v)
			g.line("struct %s_ent { bool alive; uint64_t hash; %s key; %s val; };", cn, ir.CType(v.Key), ir.CType(v.Value))
		}
	}
	g.line("")
}

func (g *cEmitter) emitLambdaEnvStructs() {
	if len(g.prog.Checked.Lambdas) == 0 {
		return
	}
	g.line("/* Closure Capture Environments */")
	for _, li := range g.prog.Checked.Lambdas {
		if len(li.Captures) == 0 {
			continue
		}
		g.line("typedef struct %s {", li.EnvCName)
		for _, c := range li.Captures {
			g.line("    %s %s;", ir.CType(c.Type), c.CName)
		}
		g.line("} %s;", li.EnvCName)
	}
	g.line("")
}

// -----------------------------------------------------------------------------
// Section 5: Forward Prototypes
// -----------------------------------------------------------------------------

var stdCHeaderFuncs = map[string]bool{
	"puts": true, "printf": true, "fprintf": true, "snprintf": true, "fopen": true, "fclose": true,
	"fread": true, "fwrite": true, "fseek": true, "ftell": true, "fflush": true, "remove": true,
	"malloc": true, "calloc": true, "realloc": true, "free": true, "exit": true, "abort": true, "getenv": true,
	"abs": true, "labs": true, "llabs": true, "qsort": true,
	"strlen": true, "strcmp": true, "strncmp": true, "strcpy": true, "memcpy": true, "memmove": true, "memset": true, "memcmp": true,
	"sin": true, "cos": true, "tan": true, "asin": true, "acos": true, "atan": true, "atan2": true,
	"sinh": true, "cosh": true, "tanh": true, "exp": true, "log": true, "log10": true, "log2": true,
	"pow": true, "sqrt": true, "cbrt": true, "hypot": true, "ceil": true, "floor": true, "round": true, "trunc": true, "fabs": true, "fmod": true,
}

func (g *cEmitter) emitForwardPrototypes() {
	if len(g.prog.ExternHeaders) == 0 && len(g.prog.ExternFuncs) > 0 {
		for _, ef := range g.prog.ExternFuncs {
			if stdCHeaderFuncs[ef.Name] || strings.HasPrefix(ef.Name, "ll_") {
				continue
			}
			var ps []string
			for _, p := range ef.Sig.Params {
				ps = append(ps, paramCType(p.Mode, p.Type))
			}
			if len(ps) == 0 {
				ps = []string{"void"}
			}
			g.line("extern %s %s(%s);", ir.CType(ef.Sig.Ret), ef.Name, strings.Join(ps, ", "))
		}
	}

	g.line("/* Function & Synthesized Helper Prototypes */")
	for _, fn := range g.prog.Funcs {
		g.line("%s;", funcSignatureC(fn))
		if fn.IsLambda {
			g.line("static inline %s;", lambdaEntrySigC(fn.LambdaInfo))
		}
	}

	for _, en := range g.prog.Enums {
		g.line("static inline ll_string %s(%s v);", ir.ToStrFnName(en), ir.CType(en))
		g.line("static inline uint64_t %s(%s v);", ir.HashFnName(en), ir.CType(en))
	}

	for _, t := range g.prog.OrderedTypes {
		if !t.IsPlain() {
			g.line("static inline void %s(%s* p);", ir.DropFnName(t), ir.CType(t))
		}
		if g.canCopy(t) && !t.IsPlain() {
			g.line("static inline %s %s(%s v);", ir.CType(t), ir.CopyFnName(t), ir.CType(t))
		}
		hasEq := types.SupportsEq(t) || g.prog.Checked.LookupConcreteMethod(t, "eq") != nil || g.prog.Checked.LookupConcreteMethod(t, "cmp") != nil
		if hasEq {
			switch t.(type) {
			case *types.Struct, *types.Optional, *types.Enum:
				g.line("static inline bool %s(%s a, %s b);", ir.EqFnName(t), ir.CType(t), ir.CType(t))
			}
		}
		if g.prog.Checked.LookupConcreteMethod(t, "cmp") != nil {
			g.line("static inline int64_t %s(%s a, %s b);", ir.CmpFnName(t), ir.CType(t), ir.CType(t))
		} else if d, isDist := t.(*types.Distinct); isDist {
			if g.prog.Checked.LookupConcreteMethod(d.Base, "cmp") != nil || types.UnderlyingPrimitive(d.Base) != nil {
				g.line("static inline int64_t %s(%s a, %s b);", ir.CmpFnName(t), ir.CType(t), ir.CType(t))
			}
		}
		if types.IsHashable(t) {
			if _, isDist := t.(*types.Distinct); isDist {
				g.line("static inline uint64_t %s(%s v);", ir.HashFnName(t), ir.CType(t))
			}
		}
		if g.prog.Checked.LookupConcreteMethod(t, "hash") != nil {
			g.line("static inline uint64_t %s(%s v);", ir.HashFnName(t), ir.CType(t))
		}
		if g.canToString(t) {
			g.line("static inline ll_string %s(%s v);", ir.ToStrFnName(t), ir.CType(t))
		}
		if st, isSt := t.(*types.Struct); isSt && st.BoxedOptional {
			optSt := types.NewOptional(st)
			g.line("static inline %s* %s(%s v);", st.CName, ir.BoxOptFnName(st), st.CName)
			g.line("static inline void %s(%s** p);", ir.DropFnName(optSt), st.CName)
			if g.canCopy(optSt) {
				g.line("static inline %s* %s(%s* p);", st.CName, ir.CopyFnName(optSt), st.CName)
			}
			if types.SupportsEq(optSt) || g.prog.Checked.LookupConcreteMethod(st, "eq") != nil || g.prog.Checked.LookupConcreteMethod(st, "cmp") != nil {
				g.line("static inline bool %s(%s* a, %s* b);", ir.EqFnName(optSt), st.CName, st.CName)
			}
			if g.canToString(optSt) {
				g.line("static inline ll_string %s(%s* p);", ir.ToStrFnName(optSt), st.CName)
			}
		}
	}
	g.line("")
}

// -----------------------------------------------------------------------------
// Section 6: Synthesized `drop`, `copy`, `eq`, `hash`, `to_string` & Collections
// -----------------------------------------------------------------------------

func (g *cEmitter) emitSynthesizedTypeHelpers() {
	// Enum helpers
	for _, en := range g.prog.Enums {
		cEn := ir.CType(en)
		g.line("static inline uint64_t %s(%s v) { return ll_fn_hash_mix_u64((uint64_t)v); }", ir.HashFnName(en), cEn)
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(en), cEn)
		if custom := g.prog.Checked.LookupConcreteMethod(en, "to_string"); custom != nil {
			g.line("    return %s(v);", custom.CName)
		} else {
			g.line("    switch (v) {")
			for _, vr := range en.Variants {
				label := fmt.Sprintf("%s.%s", en.Name, vr.Name)
				g.line("        case %s__%s: return ll_str_lit(%q, %dLL);", en.CName, vr.Name, label, len(label))
			}
			fallback := en.Name + "(?)"
			g.line("        default: return ll_str_lit(%q, %dLL);", fallback, len(fallback))
			g.line("    }")
		}
		g.line("}")
	}

	// 6a. Ordered composite lifecycle & trait helpers (`drop`, `copy`, `eq`, `hash`, `to_string`)
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Distinct:
			g.emitDistinctHelpers(v)
		case *types.Enum:
			g.emitTaggedEnumHelpers(v)
		case *types.Struct:
			g.emitStructHelpers(v)
			if v.BoxedOptional {
				g.emitBoxedOptionalHelpers(v)
			}
		case *types.Optional:
			if !v.IsBoxed() {
				g.emitFlatOptionalHelpers(v)
			}
		case *types.StackArray:
			g.emitStackArrayHelpers(v)
		case *types.Vector:
			g.emitVectorHelpers(v)
		case *types.Set:
			g.emitSetHelpers(v)
		case *types.Map:
			g.emitMapHelpers(v)
		case *types.Func:
			g.emitFuncClosureHelpers(v)
		case *types.Interface:
			g.emitInterfaceHelpers(v)
		}
	}

	// 6b. Vector & stack-array methods first (so `Set.to_vec` / `Map.keys` / `Map.values` can call `ll_vec_push` / `ll_vec_reserve`)
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.StackArray:
			g.emitStackArrayMethods(v)
		case *types.Vector:
			g.emitVectorMethods(v)
		}
	}

	// 6c. Set & Map methods
	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Set:
			g.emitSetMethods(v)
		case *types.Map:
			g.emitMapMethods(v)
		}
	}
}

func (g *cEmitter) emitDistinctHelpers(d *types.Distinct) {
	cD := ir.CType(d)
	cBase := ir.CType(d.Base)
	if !d.IsPlain() {
		g.line("static inline void %s(%s* p) { %s((%s*)p); }", ir.DropFnName(d), cD, ir.DropFnName(d.Base), cBase)
	}
	if g.canCopy(d) && !d.IsPlain() {
		if custom := g.prog.Checked.LookupConcreteMethod(d, "copy"); custom != nil {
			g.line("static inline %s %s(%s v) { return %s(v); }", cD, ir.CopyFnName(d), cD, custom.CName)
		} else {
			g.line("static inline %s %s(%s v) { return (%s)%s((%s)v); }", cD, ir.CopyFnName(d), cD, cD, g.copyExpr(d.Base), cBase)
		}
	}
	if types.IsHashable(d) {
		g.line("static inline uint64_t %s(%s v) { return %s; }", ir.HashFnName(d), cD, g.hashInlineExpr(d.Base, fmt.Sprintf("((%s)v)", cBase)))
	}
	if customCmp := g.prog.Checked.LookupConcreteMethod(d, "cmp"); customCmp != nil {
		g.line("static inline int64_t %s(%s a, %s b) { return %s(a, b); }", ir.CmpFnName(d), cD, cD, customCmp.CName)
	} else if customBase := g.prog.Checked.LookupConcreteMethod(d.Base, "cmp"); customBase != nil {
		g.line("static inline int64_t %s(%s a, %s b) { return %s((%s)a, (%s)b); }", ir.CmpFnName(d), cD, cD, customBase.CName, cBase, cBase)
	} else if p := types.UnderlyingPrimitive(d.Base); p != nil {
		if p.Kind == types.PrimString {
			g.line("static inline int64_t %s(%s a, %s b) { return ll_str_cmp((ll_string)a, (ll_string)b); }", ir.CmpFnName(d), cD, cD)
		} else {
			g.line("static inline int64_t %s(%s a, %s b) { return (a < b) ? -1 : ((a > b) ? 1 : 0); }", ir.CmpFnName(d), cD, cD)
		}
	}
	if g.canToString(d) {
		if custom := g.prog.Checked.LookupConcreteMethod(d, "to_string"); custom != nil {
			g.line("static inline ll_string %s(%s v) { return %s(v); }", ir.ToStrFnName(d), cD, custom.CName)
		} else {
			g.line("static inline ll_string %s(%s v) { return %s((%s)v); }", ir.ToStrFnName(d), cD, ir.ToStrFnName(d.Base), cBase)
		}
	}
}

func (g *cEmitter) emitTaggedEnumHelpers(en *types.Enum) {
	cEn := ir.CType(en)

	// drop: if `p->tag == 0`, already moved/empty; run custom `Drop.drop(&self)` first if defined, then drop active variant!
	if !en.IsPlain() {
		g.line("static inline void %s(%s* p) {", ir.DropFnName(en), cEn)
		g.line("    if (!p || p->tag == 0) return;")
		if en.HasCustomDrop {
			if dropM := g.prog.Checked.LookupConcreteMethod(en, "drop"); dropM != nil {
				g.line("    %s(p);", dropM.CName)
			}
		}
		g.line("    switch (p->tag) {")
		for _, vr := range en.Variants {
			switch vr.Kind {
			case types.VariantTuple:
				if vr.Payload != nil && !vr.Payload.IsPlain() {
					g.line("        case %d: %s(&p->data.%s); break;", vr.Value, ir.DropFnName(vr.Payload), vr.CName)
				}
			case types.VariantStruct:
				hasRes := false
				for _, sf := range vr.Fields {
					if !sf.Type.IsPlain() {
						hasRes = true
						break
					}
				}
				if hasRes {
					g.line("        case %d:", vr.Value)
					for i := len(vr.Fields) - 1; i >= 0; i-- {
						sf := vr.Fields[i]
						if !sf.Type.IsPlain() {
							g.line("            %s(&p->data.%s.%s);", ir.DropFnName(sf.Type), vr.CName, sf.CName)
						}
					}
					g.line("            break;")
				}
			}
		}
		g.line("        default: break;")
		g.line("    }")
		g.line("    *p = (%s){0};", cEn)
		g.line("}")
	}

	// copy
	if g.canCopy(en) && !en.IsPlain() {
		g.line("static inline %s %s(%s v) {", cEn, ir.CopyFnName(en), cEn)
		if custom := g.prog.Checked.LookupConcreteMethod(en, "copy"); custom != nil {
			g.line("    return %s(v);", custom.CName)
		} else {
			g.line("    %s out = (%s){0};", cEn, cEn)
			g.line("    out.tag = v.tag;")
			g.line("    switch (v.tag) {")
			for _, vr := range en.Variants {
				switch vr.Kind {
				case types.VariantTuple:
					if vr.Payload != nil {
						if vr.Payload.IsPlain() {
							g.line("        case %d: out.data.%s = v.data.%s; break;", vr.Value, vr.CName, vr.CName)
						} else {
							g.line("        case %d: out.data.%s = %s(v.data.%s); break;", vr.Value, vr.CName, g.copyExpr(vr.Payload), vr.CName)
						}
					}
				case types.VariantStruct:
					if len(vr.Fields) > 0 {
						g.line("        case %d:", vr.Value)
						for _, sf := range vr.Fields {
							if sf.Type.IsPlain() {
								g.line("            out.data.%s.%s = v.data.%s.%s;", vr.CName, sf.CName, vr.CName, sf.CName)
							} else {
								g.line("            out.data.%s.%s = %s(v.data.%s.%s);", vr.CName, sf.CName, g.copyExpr(sf.Type), vr.CName, sf.CName)
							}
						}
						g.line("            break;")
					}
				}
			}
			g.line("        default: break;")
			g.line("    }")
			g.line("    return out;")
		}
		g.line("}")
	}

	// eq
	if types.SupportsEq(en) {
		g.line("static inline bool %s(%s a, %s b) {", ir.EqFnName(en), cEn, cEn)
		g.line("    if (a.tag != b.tag) return false;")
		g.line("    switch (a.tag) {")
		for _, vr := range en.Variants {
			switch vr.Kind {
			case types.VariantTuple:
				if vr.Payload != nil {
					g.line("        case %d: return %s;", vr.Value, g.eqInlineExpr(vr.Payload, "a.data."+vr.CName, "b.data."+vr.CName))
				}
			case types.VariantStruct:
				if len(vr.Fields) > 0 {
					g.line("        case %d:", vr.Value)
					for _, sf := range vr.Fields {
						lhs := fmt.Sprintf("a.data.%s.%s", vr.CName, sf.CName)
						rhs := fmt.Sprintf("b.data.%s.%s", vr.CName, sf.CName)
						g.line("            if (!(%s)) return false;", stripOuterParens(g.eqInlineExpr(sf.Type, lhs, rhs)))
					}
					g.line("            return true;")
				}
			}
		}
		g.line("        default: return true;")
		g.line("    }")
		g.line("}")
	}

	// to_string
	if g.canToString(en) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(en), cEn)
		if custom := g.prog.Checked.LookupConcreteMethod(en, "to_string"); custom != nil {
			g.line("    return %s(v);", custom.CName)
		} else {
			g.line("    switch (v.tag) {")
			for _, vr := range en.Variants {
				switch vr.Kind {
				case types.VariantUnit:
					label := fmt.Sprintf("%s.%s", en.Name, vr.Name)
					g.line("        case %d: return ll_str_lit(%q, %dLL);", vr.Value, label, len(label))
				case types.VariantTuple:
					prefix := fmt.Sprintf("%s.%s(", en.Name, vr.Name)
					g.line("        case %d: {", vr.Value)
					g.line("            ll_string s = ll_str_lit(%q, %dLL);", prefix, len(prefix))
					g.line("            ll_string p0 = %s(v.data.%s);", ir.ToStrFnName(vr.Payload), vr.CName)
					g.line("            ll_str_push(&s, p0);")
					g.line("            ll_drop__string(&p0);")
					g.line("            ll_str_push(&s, ll_str_lit(\")\", 1LL));")
					g.line("            return s;")
					g.line("        }")
				case types.VariantStruct:
					prefix := fmt.Sprintf("%s.%s[", en.Name, vr.Name)
					g.line("        case %d: {", vr.Value)
					g.line("            ll_string s = ll_str_lit(%q, %dLL);", prefix, len(prefix))
					for i, sf := range vr.Fields {
						if i > 0 {
							g.line("            ll_str_push(&s, ll_str_lit(\", \", 2LL));")
						}
						flbl := sf.Name + ": "
						g.line("            ll_str_push(&s, ll_str_lit(%q, %dLL));", flbl, len(flbl))
						g.line("            ll_string pf_%d = %s(v.data.%s.%s);", i, ir.ToStrFnName(sf.Type), vr.CName, sf.CName)
						g.line("            ll_str_push(&s, pf_%d);", i)
						g.line("            ll_drop__string(&pf_%d);", i)
					}
					g.line("            ll_str_push(&s, ll_str_lit(\"]\", 1LL));")
					g.line("            return s;")
					g.line("        }")
				}
			}
			fallback := en.Name + "(?)"
			g.line("        default: return ll_str_lit(%q, %dLL);", fallback, len(fallback))
			g.line("    }")
		}
		g.line("}")
	}
}

func (g *cEmitter) emitStructHelpers(st *types.Struct) {
	cSt := ir.CType(st)

	// drop: run custom `Drop.drop(&self)` FIRST (while fields are intact), then drop Resource fields in reverse order!
	if !st.IsPlain() {
		g.line("static inline void %s(%s* p) {", ir.DropFnName(st), cSt)
		g.line("    if (!p) return;")
		if st.HasCustomDrop {
			if dropM := g.prog.Checked.LookupConcreteMethod(st, "drop"); dropM != nil {
				g.line("    %s(p);", dropM.CName)
			}
		}
		for i := len(st.Fields) - 1; i >= 0; i-- {
			f := st.Fields[i]
			if !f.Type.IsPlain() {
				g.line("    %s(&p->%s);", ir.DropFnName(f.Type), f.CName)
			}
		}
		g.line("    *p = (%s){0};", cSt)
		g.line("}")
	}

	// copy
	if g.canCopy(st) && !st.IsPlain() {
		g.line("static inline %s %s(%s v) {", cSt, ir.CopyFnName(st), cSt)
		if custom := g.prog.Checked.LookupConcreteMethod(st, "copy"); custom != nil {
			g.line("    return %s(v);", custom.CName)
		} else {
			g.line("    %s out = (%s){0};", cSt, cSt)
			g.line("    (void)v;")
			for _, f := range st.Fields {
				if f.Type.IsPlain() {
					g.line("    out.%s = v.%s;", f.CName, f.CName)
				} else {
					g.line("    out.%s = %s(v.%s);", f.CName, g.copyExpr(f.Type), f.CName)
				}
			}
			g.line("    return out;")
		}
		g.line("}")
	}

	// eq
	if custom := g.prog.Checked.LookupConcreteMethod(st, "eq"); custom != nil {
		g.line("static inline bool %s(%s a, %s b) { return %s(a, b); }", ir.EqFnName(st), cSt, cSt, custom.CName)
	} else if customCmp := g.prog.Checked.LookupConcreteMethod(st, "cmp"); customCmp != nil {
		g.line("static inline bool %s(%s a, %s b) { return %s(a, b) == 0; }", ir.EqFnName(st), cSt, cSt, customCmp.CName)
	} else if types.SupportsEq(st) {
		g.line("static inline bool %s(%s a, %s b) {", ir.EqFnName(st), cSt, cSt)
		g.line("    (void)a; (void)b;")
		for _, f := range st.Fields {
			g.line("    if (!(%s)) return false;", stripOuterParens(g.eqInlineExpr(f.Type, "a."+f.CName, "b."+f.CName)))
		}
		g.line("    return true;")
		g.line("}")
	}

	// cmp (if custom cmp method defined)
	if customCmp := g.prog.Checked.LookupConcreteMethod(st, "cmp"); customCmp != nil {
		g.line("static inline int64_t %s(%s a, %s b) { return %s(a, b); }", ir.CmpFnName(st), cSt, cSt, customCmp.CName)
	}

	// to_string (only if custom `to_string` defined)
	if g.canToString(st) {
		if custom := g.prog.Checked.LookupConcreteMethod(st, "to_string"); custom != nil {
			g.line("static inline ll_string %s(%s v) { return %s(v); }", ir.ToStrFnName(st), cSt, custom.CName)
		}
	}

	// hash (if custom `hash` method defined)
	if custom := g.prog.Checked.LookupConcreteMethod(st, "hash"); custom != nil {
		g.line("static inline uint64_t %s(%s v) { return %s(v); }", ir.HashFnName(st), cSt, custom.CName)
	}
}

func (g *cEmitter) emitBoxedOptionalHelpers(st *types.Struct) {
	optSt := types.NewOptional(st)
	cSt := st.CName

	g.line("static inline %s* %s(%s v) {", cSt, ir.BoxOptFnName(st), cSt)
	g.line("    %s* p = (%s*)ll_alloc((int64_t)sizeof(%s));", cSt, cSt, cSt)
	g.line("    *p = v;")
	g.line("    return p;")
	g.line("}")

	g.line("static inline void %s(%s** p) {", ir.DropFnName(optSt), cSt)
	g.line("    if (!p || !*p) return;")
	if !st.IsPlain() {
		g.line("    %s(*p);", ir.DropFnName(st))
	}
	g.line("    ll_free(*p);")
	g.line("    *p = NULL;")
	g.line("}")

	if g.canCopy(optSt) {
		g.line("static inline %s* %s(%s* p) {", cSt, ir.CopyFnName(optSt), cSt)
		g.line("    if (!p) return NULL;")
		g.line("    return %s(%s(*p));", ir.BoxOptFnName(st), g.copyExpr(st))
		g.line("}")
	}

	if types.SupportsEq(optSt) {
		g.line("static inline bool %s(%s* a, %s* b) {", ir.EqFnName(optSt), cSt, cSt)
		g.line("    if (!a && !b) return true;")
		g.line("    if (!a || !b) return false;")
		g.line("    return %s(*a, *b);", ir.EqFnName(st))
		g.line("}")
	}

	if g.canToString(optSt) {
		g.line("static inline ll_string %s(%s* p) {", ir.ToStrFnName(optSt), cSt)
		g.line("    if (!p) return ll_str_lit(\"none\", 4LL);")
		g.line("    return %s(*p);", ir.ToStrFnName(st))
		g.line("}")
	}
}

func (g *cEmitter) emitFlatOptionalHelpers(o *types.Optional) {
	cOpt := ir.CType(o)
	if !o.IsPlain() {
		g.line("static inline void %s(%s* p) {", ir.DropFnName(o), cOpt)
		g.line("    if (p && p->has_value) { %s(&p->value); p->has_value = false; }", ir.DropFnName(o.Elem))
		g.line("}")
	}
	if g.canCopy(o) && !o.IsPlain() {
		g.line("static inline %s %s(%s v) {", cOpt, ir.CopyFnName(o), cOpt)
		g.line("    if (!v.has_value) return (%s){ .has_value = false };", cOpt)
		g.line("    return (%s){ .has_value = true, .value = %s(v.value) };", cOpt, g.copyExpr(o.Elem))
		g.line("}")
	}
	if types.SupportsEq(o) {
		g.line("static inline bool %s(%s a, %s b) {", ir.EqFnName(o), cOpt, cOpt)
		g.line("    if (!a.has_value && !b.has_value) return true;")
		g.line("    if (!a.has_value || !b.has_value) return false;")
		g.line("    return %s;", g.eqInlineExpr(o.Elem, "a.value", "b.value"))
		g.line("}")
	}
	if g.canToString(o) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(o), cOpt)
		g.line("    if (!v.has_value) return ll_str_lit(\"none\", 4LL);")
		g.line("    return %s(v.value);", ir.ToStrFnName(o.Elem))
		g.line("}")
	}
}

func (g *cEmitter) emitStackArrayHelpers(a *types.StackArray) {
	cArr := ir.CType(a)
	if !a.IsPlain() {
		g.line("static inline void %s(%s* p) {", ir.DropFnName(a), cArr)
		g.line("    if (!p) return;")
		g.line("    for (int64_t i = %dLL - 1LL; i >= 0; i--) %s(&p->data[i]);", a.Size, ir.DropFnName(a.Elem))
		g.line("}")
	}
	if g.canCopy(a) && !a.IsPlain() {
		g.line("static inline %s %s(%s v) {", cArr, ir.CopyFnName(a), cArr)
		g.line("    %s out = (%s){0};", cArr, cArr)
		g.line("    for (int64_t i = 0; i < %dLL; i++) out.data[i] = %s(v.data[i]);", a.Size, g.copyExpr(a.Elem))
		g.line("    return out;")
		g.line("}")
	}
	if g.canToString(a) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(a), cArr)
		g.line("    ll_string s = ll_str_lit(\"[\", 1LL);")
		g.line("    for (int64_t i = 0; i < %dLL; i++) {", a.Size)
		g.line("        if (i > 0) ll_str_push(&s, ll_str_lit(\", \", 2LL));")
		g.line("        ll_string el = %s(v.data[i]);", ir.ToStrFnName(a.Elem))
		g.line("        ll_str_push(&s, el);")
		g.line("        ll_drop__string(&el);")
		g.line("    }")
		g.line("    ll_str_push(&s, ll_str_lit(\"]\", 1LL));")
		g.line("    return s;")
		g.line("}")
	}
}

func (g *cEmitter) emitVectorHelpers(v *types.Vector) {
	cVec := ir.CType(v)
	cElem := ir.CType(v.Elem)

	// drop (`cap > 0` owns heap buffer; `cap == 0` is borrowed view -> no-op!)
	g.line("static inline void %s(%s* p) {", ir.DropFnName(v), cVec)
	g.line("    if (!p) return;")
	g.line("    if (p->cap > 0 && p->data) {")
	if !v.Elem.IsPlain() {
		g.line("        for (int64_t i = p->len - 1; i >= 0; i--) %s(&p->data[i]);", ir.DropFnName(v.Elem))
	}
	g.line("        ll_free(p->data);")
	g.line("    }")
	g.line("    *p = (%s){0};", cVec)
	g.line("}")

	// copy
	if g.canCopy(v) {
		g.line("static inline %s %s(%s v) {", cVec, ir.CopyFnName(v), cVec)
		g.line("    if (v.len <= 0) return (%s){0};", cVec)
		g.line("    %s* nd = (%s*)ll_alloc(v.len * (int64_t)sizeof(%s));", cElem, cElem, cElem)
		if v.Elem.IsPlain() {
			g.line("    memcpy(nd, v.data, (size_t)v.len * sizeof(%s));", cElem)
		} else {
			g.line("    for (int64_t i = 0; i < v.len; i++) nd[i] = %s(v.data[i]);", g.copyExpr(v.Elem))
		}
		g.line("    return (%s){ .data = nd, .len = v.len, .cap = v.len };", cVec)
		g.line("}")
	}

	// to_string
	if g.canToString(v) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(v), cVec)
		g.line("    ll_string s = ll_str_lit(\"[\", 1LL);")
		g.line("    for (int64_t i = 0; i < v.len; i++) {")
		g.line("        if (i > 0) ll_str_push(&s, ll_str_lit(\", \", 2LL));")
		g.line("        ll_string el = %s(v.data[i]);", ir.ToStrFnName(v.Elem))
		g.line("        ll_str_push(&s, el);")
		g.line("        ll_drop__string(&el);")
		g.line("    }")
		g.line("    ll_str_push(&s, ll_str_lit(\"]\", 1LL));")
		g.line("    return s;")
		g.line("}")
	}
}

func (g *cEmitter) emitSetHelpers(s *types.Set) {
	cSet := ir.CType(s)
	cEnt := cSet + "_ent"
	g.line("static inline void %s(%s* p) {", ir.DropFnName(s), cSet)
	g.line("    if (!p) return;")
	if !s.Elem.IsPlain() {
		g.line("    for (int64_t i = 0; i < p->ent_len; i++) {")
		g.line("        if (p->entries[i].alive) %s(&p->entries[i].key);", ir.DropFnName(s.Elem))
		g.line("    }")
	}
	g.line("    if (p->entries) ll_free(p->entries);")
	g.line("    if (p->buckets) ll_free(p->buckets);")
	g.line("    *p = (%s){0};", cSet)
	g.line("}")

	if g.canCopy(s) {
		g.line("static inline %s %s(%s v) {", cSet, ir.CopyFnName(s), cSet)
		g.line("    if (v.len <= 0 || !v.buckets) return (%s){0};", cSet)
		g.line("    %s out = (%s){0};", cSet, cSet)
		g.line("    out.buck_cap = v.buck_cap;")
		g.line("    out.buckets = (int64_t*)ll_alloc(v.buck_cap * (int64_t)sizeof(int64_t));")
		g.line("    memcpy(out.buckets, v.buckets, (size_t)v.buck_cap * sizeof(int64_t));")
		g.line("    out.ent_cap = v.ent_len;")
		g.line("    out.ent_len = v.ent_len;")
		g.line("    out.len = v.len;")
		g.line("    out.entries = (%s*)ll_alloc(v.ent_len * (int64_t)sizeof(%s));", cEnt, cEnt)
		g.line("    for (int64_t i = 0; i < v.ent_len; i++) {")
		g.line("        out.entries[i].alive = v.entries[i].alive;")
		g.line("        out.entries[i].hash = v.entries[i].hash;")
		g.line("        if (v.entries[i].alive) out.entries[i].key = %s(v.entries[i].key);", g.copyExpr(s.Elem))
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}

	if g.canToString(s) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(s), cSet)
		g.line("    ll_string out = ll_str_lit(\"#[\", 2LL);")
		g.line("    bool first = true;")
		g.line("    for (int64_t i = 0; i < v.ent_len; i++) {")
		g.line("        if (!v.entries[i].alive) continue;")
		g.line("        if (!first) ll_str_push(&out, ll_str_lit(\", \", 2LL));")
		g.line("        first = false;")
		g.line("        ll_string el = %s(v.entries[i].key);", ir.ToStrFnName(s.Elem))
		g.line("        ll_str_push(&out, el);")
		g.line("        ll_drop__string(&el);")
		g.line("    }")
		g.line("    ll_str_push(&out, ll_str_lit(\"]\", 1LL));")
		g.line("    return out;")
		g.line("}")
	}
}

func (g *cEmitter) emitMapHelpers(m *types.Map) {
	cMap := ir.CType(m)
	cEnt := cMap + "_ent"
	g.line("static inline void %s(%s* p) {", ir.DropFnName(m), cMap)
	g.line("    if (!p) return;")
	if !m.Key.IsPlain() || !m.Value.IsPlain() {
		g.line("    for (int64_t i = 0; i < p->ent_len; i++) {")
		g.line("        if (!p->entries[i].alive) continue;")
		if !m.Key.IsPlain() {
			g.line("        %s(&p->entries[i].key);", ir.DropFnName(m.Key))
		}
		if !m.Value.IsPlain() {
			g.line("        %s(&p->entries[i].val);", ir.DropFnName(m.Value))
		}
		g.line("    }")
	}
	g.line("    if (p->entries) ll_free(p->entries);")
	g.line("    if (p->buckets) ll_free(p->buckets);")
	g.line("    *p = (%s){0};", cMap)
	g.line("}")

	if g.canCopy(m) {
		g.line("static inline %s %s(%s v) {", cMap, ir.CopyFnName(m), cMap)
		g.line("    if (v.len <= 0 || !v.buckets) return (%s){0};", cMap)
		g.line("    %s out = (%s){0};", cMap, cMap)
		g.line("    out.buck_cap = v.buck_cap;")
		g.line("    out.buckets = (int64_t*)ll_alloc(v.buck_cap * (int64_t)sizeof(int64_t));")
		g.line("    memcpy(out.buckets, v.buckets, (size_t)v.buck_cap * sizeof(int64_t));")
		g.line("    out.ent_cap = v.ent_len;")
		g.line("    out.ent_len = v.ent_len;")
		g.line("    out.len = v.len;")
		g.line("    out.entries = (%s*)ll_alloc(v.ent_len * (int64_t)sizeof(%s));", cEnt, cEnt)
		g.line("    for (int64_t i = 0; i < v.ent_len; i++) {")
		g.line("        out.entries[i].alive = v.entries[i].alive;")
		g.line("        out.entries[i].hash = v.entries[i].hash;")
		g.line("        if (v.entries[i].alive) {")
		g.line("            out.entries[i].key = %s(v.entries[i].key);", g.copyExpr(m.Key))
		g.line("            out.entries[i].val = %s(v.entries[i].val);", g.copyExpr(m.Value))
		g.line("        }")
		g.line("    }")
		g.line("    return out;")
		g.line("}")
	}

	if g.canToString(m) {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(m), cMap)
		g.line("    ll_string out = ll_str_lit(\"#[\", 2LL);")
		g.line("    bool first = true;")
		g.line("    for (int64_t i = 0; i < v.ent_len; i++) {")
		g.line("        if (!v.entries[i].alive) continue;")
		g.line("        if (!first) ll_str_push(&out, ll_str_lit(\", \", 2LL));")
		g.line("        first = false;")
		g.line("        ll_string ks = %s(v.entries[i].key);", ir.ToStrFnName(m.Key))
		g.line("        ll_string vs = %s(v.entries[i].val);", ir.ToStrFnName(m.Value))
		g.line("        ll_str_push(&out, ks);")
		g.line("        ll_str_push(&out, ll_str_lit(\": \", 2LL));")
		g.line("        ll_str_push(&out, vs);")
		g.line("        ll_drop__string(&ks);")
		g.line("        ll_drop__string(&vs);")
		g.line("    }")
		g.line("    ll_str_push(&out, ll_str_lit(\"]\", 1LL));")
		g.line("    return out;")
		g.line("}")
	}
}

func (g *cEmitter) emitFuncClosureHelpers(fn *types.Func) {
	cFn := ir.CType(fn)
	g.line("static inline void %s(%s* p) {", ir.DropFnName(fn), cFn)
	g.line("    if (p && p->env && p->drop_env) p->drop_env(p->env);")
	g.line("    if (p) *p = (%s){0};", cFn)
	g.line("}")

	g.line("static inline %s %s(%s v) {", cFn, ir.CopyFnName(fn), cFn)
	g.line("    void* nenv = (v.env && v.copy_env) ? v.copy_env(v.env) : NULL;")
	g.line("    return (%s){ .fn = v.fn, .env = nenv, .copy_env = v.copy_env, .drop_env = v.drop_env };", cFn)
	g.line("}")
}

func (g *cEmitter) emitInterfaceHelpers(ifc *types.Interface) {
	cIfc := ir.CType(ifc)
	g.line("static inline void %s(%s* p) {", ir.DropFnName(ifc), cIfc)
	g.line("    if (p && p->owned && p->data && p->vtable && p->vtable->drop_box) {")
	g.line("        p->vtable->drop_box(p->data);")
	g.line("    }")
	g.line("    if (p) *p = (%s){0};", cIfc)
	g.line("}")

	g.line("static inline %s %s(%s v) {", cIfc, ir.CopyFnName(ifc), cIfc)
	g.line("    if (!v.data || !v.vtable || !v.vtable->clone_box) return (%s){0};", cIfc)
	g.line("    return (%s){ .data = v.vtable->clone_box(v.data), .vtable = v.vtable, .owned = true };", cIfc)
	g.line("}")

	if ifc == g.prog.Checked.IfacePrintable {
		g.line("static inline ll_string %s(%s v) {", ir.ToStrFnName(ifc), cIfc)
		g.line("    return v.vtable->m_to_string(v.data);")
		g.line("}")
	}
}

// -----------------------------------------------------------------------------
// Section 7: Lambda Envs, Static Thunks, & Interface VTables
// -----------------------------------------------------------------------------

func (g *cEmitter) emitLambdaAndThunkHelpers() {
	for _, li := range g.prog.Checked.Lambdas {
		if len(li.Captures) > 0 {
			// env drop
			g.line("static inline void %s_drop(void* _raw) {", li.EnvCName)
			g.line("    if (!_raw) return;")
			g.line("    %s* e = (%s*)_raw;", li.EnvCName, li.EnvCName)
			g.line("    (void)e;")
			for i := len(li.Captures) - 1; i >= 0; i-- {
				c := li.Captures[i]
				if !c.Type.IsPlain() {
					g.line("    %s(&e->%s);", ir.DropFnName(c.Type), c.CName)
				}
			}
			g.line("    ll_free(e);")
			g.line("}")

			// env copy
			allCopyable := true
			for _, c := range li.Captures {
				if !g.canCopy(c.Type) {
					allCopyable = false
					break
				}
			}
			g.line("static inline void* %s_copy(const void* _raw) {", li.EnvCName)
			if !allCopyable {
				g.line("    (void)_raw; ll_panic_cstr(\"closure environment is non-copyable\", \"<lambda>\", 0);")
			} else {
				g.line("    const %s* src = (const %s*)_raw;", li.EnvCName, li.EnvCName)
				g.line("    %s* dst = (%s*)ll_alloc((int64_t)sizeof(%s));", li.EnvCName, li.EnvCName, li.EnvCName)
				for _, c := range li.Captures {
					if c.Type.IsPlain() {
						g.line("    dst->%s = src->%s;", c.CName, c.CName)
					} else {
						g.line("    dst->%s = %s(src->%s);", c.CName, g.copyExpr(c.Type), c.CName)
					}
				}
				g.line("    return (void*)dst;")
			}
			g.line("}")
		}

		// `_entry` wrapper matching `fn(void* _env, ...)` pointer signature
		var pDecls []string
		var pArgs []string
		pDecls = append(pDecls, "void* _ll_env")
		pArgs = append(pArgs, "_ll_env")
		for idx, p := range li.Sig.Params {
			pn := fmt.Sprintf("_p%d", idx)
			pDecls = append(pDecls, fmt.Sprintf("%s %s", paramCType(p.Mode, p.Type), pn))
			pArgs = append(pArgs, pn)
		}
		g.line("static inline %s %s_entry(%s) {", ir.CType(li.Sig.Ret), li.CName, strings.Join(pDecls, ", "))
		if li.Sig.Ret.Equals(types.TypeVoid) {
			g.line("    %s(%s);", li.CName, strings.Join(pArgs, ", "))
		} else {
			g.line("    return %s(%s);", li.CName, strings.Join(pArgs, ", "))
		}
		g.line("}")
	}

	// Static function closure thunks
	for _, st := range g.prog.StaticThunks {
		var pDecls []string
		var pArgs []string
		pDecls = append(pDecls, "void* _ll_env")
		for idx, p := range st.FuncType.Params {
			pn := fmt.Sprintf("_p%d", idx)
			pDecls = append(pDecls, fmt.Sprintf("%s %s", paramCType(p.Mode, p.Type), pn))
			pArgs = append(pArgs, pn)
		}
		g.line("static inline %s %s(%s) {", ir.CType(st.FuncType.Ret), st.ThunkCName, strings.Join(pDecls, ", "))
		g.line("    (void)_ll_env;")
		if st.FuncType.Ret.Equals(types.TypeVoid) {
			g.line("    %s(%s);", st.TargetFunc.CName, strings.Join(pArgs, ", "))
		} else {
			g.line("    return %s(%s);", st.TargetFunc.CName, strings.Join(pArgs, ", "))
		}
		g.line("}")
	}
	g.line("")
}

func (g *cEmitter) emitVTables() {
	if len(g.prog.VTables) == 0 {
		return
	}
	g.line("/* Interface VTables */")
	for _, vt := range g.prog.VTables {
		cCon := ir.CType(vt.Concrete)
		vtInst := ir.VTableInstanceName(vt.Concrete, vt.Iface)
		vtTy := ir.VTableTypeName(vt.Iface)

		// drop_box
		dropName := vtInst + "__drop"
		g.line("static inline void %s(void* _data) {", dropName)
		g.line("    if (!_data) return;")
		if !vt.Concrete.IsPlain() {
			g.line("    %s((%s*)_data);", ir.DropFnName(vt.Concrete), cCon)
		}
		g.line("    ll_free(_data);")
		g.line("}")

		// clone_box
		cloneName := vtInst + "__clone"
		g.line("static inline void* %s(const void* _data) {", cloneName)
		if g.canCopy(vt.Concrete) {
			g.line("    %s* nb = (%s*)ll_alloc((int64_t)sizeof(%s));", cCon, cCon, cCon)
			g.line("    *nb = %s(*(const %s*)_data);", g.copyExpr(vt.Concrete), cCon)
			g.line("    return (void*)nb;")
		} else {
			g.line("    (void)_data; ll_panic_cstr(\"boxed interface concrete value does not support .copy()\", \"<vtable>\", 0);")
		}
		g.line("}")

		// method wrappers
		var mPtrNames []string
		for idx, im := range vt.Iface.Methods {
			wName := fmt.Sprintf("%s__m_%s", vtInst, im.Name)
			mPtrNames = append(mPtrNames, fmt.Sprintf(".m_%s = %s", im.Name, wName))

			var pDecls []string
			var callArgs []string
			pDecls = append(pDecls, "void* _self")
			if im.RecvMode == types.ModeMut {
				callArgs = append(callArgs, fmt.Sprintf("((%s*)_self)", cCon))
			} else {
				callArgs = append(callArgs, fmt.Sprintf("(*(const %s*)_self)", cCon))
			}
			for pi, p := range im.Params {
				pn := fmt.Sprintf("_p%d", pi)
				pDecls = append(pDecls, fmt.Sprintf("%s %s", paramCType(p.Mode, p.Type), pn))
				callArgs = append(callArgs, pn)
			}

			g.line("static inline %s %s(%s) {", ir.CType(im.Ret), wName, strings.Join(pDecls, ", "))
			fi := vt.Methods[idx]
			targetFn := ""
			if fi != nil {
				targetFn = fi.CName
			} else if im.Name == "to_string" {
				targetFn = ir.ToStrFnName(vt.Concrete)
			}
			if im.Ret.Equals(types.TypeVoid) {
				g.line("    %s(%s);", targetFn, strings.Join(callArgs, ", "))
			} else {
				g.line("    return %s(%s);", targetFn, strings.Join(callArgs, ", "))
			}
			g.line("}")
		}

		// static const vtable instance
		g.line("static const %s %s = {", vtTy, vtInst)
		g.line("    .drop_box = %s,", dropName)
		g.line("    .clone_box = %s,", cloneName)
		for _, mp := range mPtrNames {
			g.line("    %s,", mp)
		}
		g.line("};")
	}
	g.line("")
}

// -----------------------------------------------------------------------------
// Section 9 & 10: MIR Function Printing & C11 `main`
// -----------------------------------------------------------------------------

func funcSignatureC(fn *ir.Function) string {
	var ps []string
	if fn.IsLambda {
		ps = append(ps, "void* _ll_env")
	}
	for _, p := range fn.Params {
		tyStr := ir.CType(p.Type)
		if p.IsMutPtr {
			tyStr += "*"
		}
		ps = append(ps, fmt.Sprintf("%s %s", tyStr, p.CName))
	}
	if len(ps) == 0 {
		ps = []string{"void"}
	}
	attr := ""
	if fn.IsAlwaysInline {
		attr = "inline __attribute__((always_inline)) "
	} else if fn.IsNoInline {
		attr = "__attribute__((noinline)) "
	}
	return fmt.Sprintf("static %s%s %s(%s)", attr, ir.CType(fn.RetType), fn.CName, strings.Join(ps, ", "))
}

func lambdaEntrySigC(li *sema.LambdaInfo) string {
	var ps []string
	ps = append(ps, "void* _ll_env")
	for idx, p := range li.Sig.Params {
		ps = append(ps, fmt.Sprintf("%s _p%d", paramCType(p.Mode, p.Type), idx))
	}
	return fmt.Sprintf("%s %s_entry(%s)", ir.CType(li.Sig.Ret), li.CName, strings.Join(ps, ", "))
}

func paramCType(mode types.ParamMode, ty types.Type) string {
	if mode == types.ModeMut {
		return ir.CType(ty) + "*"
	}
	return ir.CType(ty)
}

func (g *cEmitter) emitFunction(fn *ir.Function) {
	g.line("%s {", funcSignatureC(fn))
	if fn.IsLambda {
		g.indentLine(1, "(void)_ll_env;")
	}
	for _, p := range fn.Params {
		g.indentLine(1, "(void)%s;", p.CName)
		if p.MovedFlag != "" {
			// Owned parameter enters the function already live (`moved = false`)!
			g.indentLine(1, "bool %s = false; (void)%s;", p.MovedFlag, p.MovedFlag)
		}
	}
	for _, loc := range fn.Locals {
		tyStr := ir.CType(loc.Type)
		if loc.IsMutPtr {
			g.indentLine(1, "%s* %s = NULL;", tyStr, loc.CName)
		} else {
			g.indentLine(1, "%s %s = (%s){0};", tyStr, loc.CName, tyStr)
		}
		if loc.MovedFlag != "" {
			// Local slots start uninitialized (`moved = true`) until assigned!
			g.indentLine(1, "bool %s = true; (void)%s;", loc.MovedFlag, loc.MovedFlag)
		}
	}

	g.emitInstrList(1, fn.Body)
	g.line("}")
	g.line("")
}

func (g *cEmitter) emitInstrList(indent int, ins []ir.Instr) {
	for _, in := range ins {
		switch v := in.(type) {
		case *ir.InstrAssign:
			g.indentLine(indent, "%s = %s;", v.Dst, v.Expr)

		case *ir.InstrExpr:
			if v.Expr != "" {
				g.indentLine(indent, "%s;", v.Expr)
			}

		case *ir.InstrZero:
			g.indentLine(indent, "%s = (%s){0};", v.Var, ir.CType(v.Type))
			if v.MovedFlag != "" {
				g.indentLine(indent, "%s = true;", v.MovedFlag)
			}

		case *ir.InstrDrop:
			if v.Type.IsPlain() {
				continue
			}
			if v.MovedFlag != "" {
				g.indentLine(indent, "if (!%s) { %s = true; %s(&%s); }",
					v.MovedFlag, v.MovedFlag, ir.DropFnName(v.Type), v.Var)
			} else {
				g.indentLine(indent, "%s(&%s);", ir.DropFnName(v.Type), v.Var)
			}

		case *ir.InstrIf:
			g.indentLine(indent, "if (%s) {", stripOuterParens(v.Cond))
			g.emitInstrList(indent+1, v.Then)
			if len(v.Else) > 0 {
				g.indentLine(indent, "} else {")
				g.emitInstrList(indent+1, v.Else)
			}
			g.indentLine(indent, "}")

		case *ir.InstrWhile:
			g.indentLine(indent, "while (%s) {", stripOuterParens(v.Cond))
			g.emitInstrList(indent+1, v.Body)
			g.indentLine(indent, "}")

		case *ir.InstrBlock:
			g.indentLine(indent, "{")
			g.emitInstrList(indent+1, v.Body)
			g.indentLine(indent, "}")

		case *ir.InstrLabel:
			g.indentLine(indent, "%s: ;", v.Name)

		case *ir.InstrGoto:
			g.indentLine(indent, "goto %s;", v.Label)

		case *ir.InstrReturn:
			if v.Expr == "" {
				g.indentLine(indent, "return;")
			} else {
				g.indentLine(indent, "return %s;", v.Expr)
			}
		}
	}
}

func (g *cEmitter) emitCMain() {
	g.line("int main(int argc, char** argv) {")
	g.line("    ll_rt_argc = argc;")
	g.line("    ll_rt_argv = argv;")

	// Initialize top-level `bind` constants
	for _, gl := range g.prog.Globals {
		if len(gl.InitInstr) > 0 || len(gl.Locals) > 0 {
			g.line("    {")
			for _, loc := range gl.Locals {
				tyStr := ir.CType(loc.Type)
				if loc.IsMutPtr {
					g.indentLine(2, "%s* %s = NULL;", tyStr, loc.CName)
				} else {
					g.indentLine(2, "%s %s = (%s){0};", tyStr, loc.CName, tyStr)
				}
				if loc.MovedFlag != "" {
					g.indentLine(2, "bool %s = true; (void)%s;", loc.MovedFlag, loc.MovedFlag)
				}
			}
			g.emitInstrList(2, gl.InitInstr)
			g.line("        %s = %s;", gl.CName, gl.InitExpr)
			g.line("    }")
		} else {
			g.line("    %s = %s;", gl.CName, gl.InitExpr)
		}
	}

	g.line("    int _ll_rc = 0;")
	if g.prog.MainFunc.RetType.Equals(types.TypeVoid) {
		g.line("    %s();", g.prog.MainFunc.CName)
	} else {
		g.line("    _ll_rc = (int)%s();", g.prog.MainFunc.CName)
	}

	// Deterministically drop top-level Resource constants in reverse initialization order!
	for i := len(g.prog.Globals) - 1; i >= 0; i-- {
		gl := g.prog.Globals[i]
		if !gl.Type.IsPlain() {
			g.line("    %s(&%s);", ir.DropFnName(gl.Type), gl.CName)
		}
	}

	g.line("    ll_check_leaks_at_exit();")
	g.line("    return _ll_rc;")
	g.line("}")
}

// -----------------------------------------------------------------------------
// Capability & Inline Expression Helpers
// -----------------------------------------------------------------------------

func (g *cEmitter) copyExpr(t types.Type) string {
	if custom := g.prog.Checked.LookupConcreteMethod(t, "copy"); custom != nil {
		return custom.CName
	}
	if t.IsPlain() {
		return ""
	}
	return ir.CopyFnName(t)
}

func (g *cEmitter) eqInlineExpr(t types.Type, a, b string) string {
	if p := types.UnderlyingPrimitive(t); p != nil {
		if p.Kind == types.PrimString {
			return fmt.Sprintf("ll_str_eq(%s, %s)", a, b)
		}
		return fmt.Sprintf("((%s) == (%s))", a, b)
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "eq"); custom != nil {
		return fmt.Sprintf("%s(%s, %s)", custom.CName, a, b)
	}
	if customCmp := g.prog.Checked.LookupConcreteMethod(t, "cmp"); customCmp != nil {
		return fmt.Sprintf("(%s(%s, %s) == 0)", customCmp.CName, a, b)
	}
	switch v := t.(type) {
	case *types.Primitive:
		if v.Kind == types.PrimString {
			return fmt.Sprintf("ll_str_eq(%s, %s)", a, b)
		}
		return fmt.Sprintf("((%s) == (%s))", a, b)
	case *types.Enum:
		if v.IsTagged {
			return fmt.Sprintf("%s(%s, %s)", ir.EqFnName(t), a, b)
		}
		return fmt.Sprintf("((%s) == (%s))", a, b)
	case *types.Ptr, *types.Ref:
		return fmt.Sprintf("((%s) == (%s))", a, b)
	case *types.Distinct:
		cBase := ir.CType(v.Base)
		return g.eqInlineExpr(v.Base, fmt.Sprintf("((%s)(%s))", cBase, a), fmt.Sprintf("((%s)(%s))", cBase, b))
	case *types.Struct, *types.Optional:
		return fmt.Sprintf("%s(%s, %s)", ir.EqFnName(t), a, b)
	default:
		return fmt.Sprintf("((%s) == (%s))", a, b)
	}
}

func (g *cEmitter) hashInlineExpr(t types.Type, expr string) string {
	if custom := g.prog.Checked.LookupConcreteMethod(t, "hash"); custom != nil {
		return fmt.Sprintf("%s(%s)", custom.CName, expr)
	}
	switch v := t.(type) {
	case *types.Distinct:
		return fmt.Sprintf("%s(%s)", ir.HashFnName(v), expr)
	case *types.Enum:
		return fmt.Sprintf("ll_fn_hash_mix_u64((uint64_t)(%s))", expr)
	case *types.Ptr:
		return fmt.Sprintf("ll_fn_hash_mix_u64((uint64_t)(uintptr_t)(%s))", expr)
	default:
		return fmt.Sprintf("ll_fn_hash_mix_u64((uint64_t)(%s))", expr)
	}
}

func (g *cEmitter) canCopy(t types.Type) bool {
	return g.canCopyVisited(t, make(map[types.Type]bool))
}

func (g *cEmitter) canCopyVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if t.IsPlain() {
		return true
	}
	if g.prog.Checked.LookupConcreteMethod(t, "copy") != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind == types.PrimString
	case *types.Vector:
		return g.canCopyVisited(v.Elem, seen)
	case *types.StackArray:
		return g.canCopyVisited(v.Elem, seen)
	case *types.Set:
		return g.canCopyVisited(v.Elem, seen)
	case *types.Map:
		return g.canCopyVisited(v.Key, seen) && g.canCopyVisited(v.Value, seen)
	case *types.Optional:
		return g.canCopyVisited(v.Elem, seen)
	case *types.Distinct:
		return g.canCopyVisited(v.Base, seen)
	case *types.Interface:
		return !v.UsesSelf
	case *types.Func:
		return true
	case *types.Enum:
		if v.HasCustomDrop {
			return false
		}
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			switch vr.Kind {
			case types.VariantTuple:
				if vr.Payload != nil && !g.canCopyVisited(vr.Payload, seen) {
					return false
				}
			case types.VariantStruct:
				for _, sf := range vr.Fields {
					if !g.canCopyVisited(sf.Type, seen) {
						return false
					}
				}
			}
		}
		return true
	case *types.Struct:
		if v.HasCustomDrop {
			return false
		}
		if seen[v] {
			return true
		}
		seen[v] = true
		for _, f := range v.Fields {
			if !g.canCopyVisited(f.Type, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (g *cEmitter) canToString(t types.Type) bool {
	return g.canToStringVisited(t, make(map[types.Type]bool))
}

func (g *cEmitter) canToStringVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if g.prog.Checked.LookupConcreteMethod(t, "to_string") != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid && v.Kind != types.PrimArena
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			switch vr.Kind {
			case types.VariantTuple:
				if vr.Payload != nil && !g.canToStringVisited(vr.Payload, seen) {
					return false
				}
			case types.VariantStruct:
				for _, sf := range vr.Fields {
					if !g.canToStringVisited(sf.Type, seen) {
						return false
					}
				}
			}
		}
		return true
	case *types.Distinct:
		return g.canToStringVisited(v.Base, seen)
	case *types.Vector:
		return g.canToStringVisited(v.Elem, seen)
	case *types.StackArray:
		return g.canToStringVisited(v.Elem, seen)
	case *types.Set:
		return g.canToStringVisited(v.Elem, seen)
	case *types.Map:
		return g.canToStringVisited(v.Key, seen) && g.canToStringVisited(v.Value, seen)
	case *types.Optional:
		return g.canToStringVisited(v.Elem, seen)
	case *types.Interface:
		return v == g.prog.Checked.IfacePrintable
	default:
		return false
	}
}

// stripOuterParens removes redundant balanced outer parentheses around a C condition
// expression so Clang `-Wparentheses-equality` never warns on `if (((a) == (b)))`.
func stripOuterParens(s string) string {
	for {
		s = strings.TrimSpace(s)
		if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
			return s
		}
		depth := 0
		inStr := false
		inChar := false
		wrapsAll := true
		for i := 0; i < len(s)-1; i++ {
			ch := s[i]
			if inStr {
				if ch == '\\' {
					i++
				} else if ch == '"' {
					inStr = false
				}
				continue
			}
			if inChar {
				if ch == '\\' {
					i++
				} else if ch == '\'' {
					inChar = false
				}
				continue
			}
			switch ch {
			case '"':
				inStr = true
			case '\'':
				inChar = true
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					wrapsAll = false
				}
			}
			if !wrapsAll {
				break
			}
		}
		if !wrapsAll || depth != 1 {
			return s
		}
		s = s[1 : len(s)-1]
	}
}
