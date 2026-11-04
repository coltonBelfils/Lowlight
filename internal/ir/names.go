package ir

import (
	"fmt"

	"lowlight/internal/types"
)

// CType returns the exact C11 type name used for `t` in generated code.
func CType(t types.Type) string {
	if t == nil {
		return "void"
	}
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PrimVoid, types.PrimNever:
			return "void"
		case types.PrimBool:
			return "bool"
		case types.PrimChar:
			return "uint32_t"
		case types.PrimI8:
			return "int8_t"
		case types.PrimI16:
			return "int16_t"
		case types.PrimI32:
			return "int32_t"
		case types.PrimI64:
			return "int64_t"
		case types.PrimU8:
			return "uint8_t"
		case types.PrimU16:
			return "uint16_t"
		case types.PrimU32:
			return "uint32_t"
		case types.PrimU64:
			return "uint64_t"
		case types.PrimF32:
			return "float"
		case types.PrimF64:
			return "double"
		case types.PrimString:
			return "ll_string"
		case types.PrimArena:
			return "ll_arena"
		default:
			return "int64_t"
		}
	case *types.Ptr:
		if v.Elem.Equals(types.TypeVoid) {
			return "void*"
		}
		return CType(v.Elem) + "*"
	case *types.Ref:
		return CType(v.Elem) + "*"
	case *types.Enum:
		return v.CName
	case *types.Distinct:
		return v.CName
	case *types.Struct:
		return v.CName
	case *types.Optional:
		if v.IsBoxed() {
			return CType(v.Elem) + "*"
		}
		return "ll_" + v.Mangled()
	case *types.Vector:
		return "ll_" + v.Mangled()
	case *types.StackArray:
		return "ll_" + v.Mangled()
	case *types.Set:
		return "ll_" + v.Mangled()
	case *types.Map:
		return "ll_" + v.Mangled()
	case *types.Func:
		return "ll_" + v.Mangled()
	case *types.Interface:
		return v.CName
	default:
		return "int64_t"
	}
}

// NeedsMovedFlag reports whether a zeroed `{0}` value of `t` is ambiguous with a live value
// (true when `t` is or directly embeds by value a Struct with custom `Drop`, such as `File[fd: 0]`).
func NeedsMovedFlag(t types.Type) bool {
	return needsMovedFlagVisited(t, make(map[*types.Struct]bool))
}

func needsMovedFlagVisited(t types.Type, seen map[*types.Struct]bool) bool {
	if t == nil || t.IsPlain() {
		return false
	}
	switch v := t.(type) {
	case *types.Struct:
		if v.HasCustomDrop {
			return true
		}
		if seen[v] {
			return false
		}
		seen[v] = true
		for _, f := range v.Fields {
			if needsMovedFlagVisited(f.Type, seen) {
				return true
			}
		}
		return false
	case *types.StackArray:
		return needsMovedFlagVisited(v.Elem, seen)
	case *types.Distinct:
		return needsMovedFlagVisited(v.Base, seen)
	default:
		// string (cap==0), Vector (cap==0), Set (buckets==NULL), Map (buckets==NULL),
		// Optional (has_value==false or ptr==NULL), Interface (owned==false), Func (env==NULL),
		// Arena (head==NULL) all have unambiguous `{0}` empty states!
		return false
	}
}

func DropFnName(t types.Type) string {
	return "ll_drop__" + t.Mangled()
}

func CopyFnName(t types.Type) string {
	return "ll_copy__" + t.Mangled()
}

func EqFnName(t types.Type) string {
	return "ll_eq__" + t.Mangled()
}

func CmpFnName(t types.Type) string {
	return "ll_cmp__" + t.Mangled()
}

func ToStrFnName(t types.Type) string {
	return "ll_tostr__" + t.Mangled()
}

func HashFnName(t types.Type) string {
	return "ll_hash__" + t.Mangled()
}

func BoxOptFnName(st *types.Struct) string {
	return "ll_box__" + st.Mangled()
}

func VTableTypeName(ifc *types.Interface) string {
	return "ll_vt_" + ifc.CName
}

func VTableInstanceName(concrete types.Type, ifc *types.Interface) string {
	return fmt.Sprintf("ll_vtinst_%s__as__%s", concrete.Mangled(), ifc.Mangled())
}

func StaticFuncThunkName(funcCName string, fnTy *types.Func) string {
	return fmt.Sprintf("ll_thunk_%s__%s", funcCName, fnTy.Mangled())
}
