package ir

import (
	"fmt"

	"lowlight/internal/types"
)

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
