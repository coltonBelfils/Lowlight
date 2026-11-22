package ir

import (
	"fmt"

	"lowlight/internal/ast"
	"lowlight/internal/types"
)

// callMethod calls a method of recvTy that sema has already instantiated,
// such as the std/core Set and Map methods behind `#[...]` literals, `m[k]`,
// and `x in s`. Pass a mutable receiver as an address and a read receiver
// as a value.
func (fl *funcLowerer) callMethod(recvTy types.Type, name string, args ...Expr) Expr {
	return fl.callMethodAt(nil, recvTy, name, args...)
}

// callMethodAt is callMethod for a method that may be `@track_caller`: it
// passes the location of `at` (or this function's own caller, if it tracks
// its caller too) as the caller location.
func (fl *funcLowerer) callMethodAt(at ast.Expr, recvTy types.Type, name string, args ...Expr) Expr {
	fi := fl.prog.LookupConcreteMethod(recvTy, name)
	if fi == nil {
		panic(fmt.Sprintf("ir: %s has no instantiated method %q", recvTy.String(), name))
	}
	ret := fi.Sig.Ret
	if ret == nil {
		ret = types.TypeVoid
	}
	if fi.TrackCaller {
		if fl.currFunc != nil && fl.currFunc.TrackCaller && fl.callerFileLoc != nil {
			args = append(args, NewLocalExpr(fl.callerFileLoc), NewLocalExpr(fl.callerLineLoc), NewLocalExpr(fl.callerColLoc))
		} else {
			file, line, col := formatSpanPos(at)
			args = append(args, NewLitString(file), NewLitInt(uint64(line), false, types.TypeInt), NewLitInt(uint64(col), false, types.TypeInt))
		}
	}
	return &ExprCall{
		Callee: &ExprFuncRef{Name: fi.CName, TargetFunc: fi, FuncType: fi.Sig},
		Args:   args,
		Typ:    ret,
	}
}

// collField returns a field of a std/core Set, Map, or entry struct.
func collField(st *types.Struct, name string) *types.StructField {
	f, _ := st.FieldByName(name)
	if f == nil {
		panic(fmt.Sprintf("ir: %s has no field %q", st.String(), name))
	}
	return f
}

// collEntries returns the `entries` and `ent_len` fields of a set or map
// struct and the entry struct that `entries` points to.
func collEntries(colTy types.Type) (entries, entLen *types.StructField, entSt *types.Struct) {
	st := colTy.(*types.Struct)
	entries = collField(st, "entries")
	entLen = collField(st, "ent_len")
	entSt = entries.Type.(*types.Ptr).Elem.(*types.Struct)
	return entries, entLen, entSt
}

// hashCallExpr hashes a value of a hashable type that has no `hash` method
// of its own: tuples use the compiler's per-type helper, and tag-only enums,
// pointers, and distinct types hash their underlying integer or base value.
func (fl *funcLowerer) hashCallExpr(t types.Type, v Expr) Expr {
	fl.tc.visit(t)
	if fi := fl.prog.LookupConcreteMethod(t, "hash"); fi != nil {
		return fl.callMethod(t, "hash", EnsureExpr(v, t))
	}
	switch tv := t.(type) {
	case *types.Distinct:
		return fl.hashCallExpr(tv.Base, &ExprCast{Val: EnsureExpr(v, t), From: t, To: tv.Base})
	case *types.Enum:
		return fl.hashCallExpr(types.TypeInt, &ExprCast{Val: EnsureExpr(v, t), From: t, To: types.TypeInt})
	case *types.Ptr:
		return fl.hashCallExpr(types.TypeU64, &ExprCast{Val: EnsureExpr(v, t), From: t, To: types.TypeU64})
	}
	return &ExprHash{Val: EnsureExpr(v, t), ValType: t}
}

// strPush appends a string to the string at strPtr with the native `string.push`.
func (fl *funcLowerer) strPush(strPtr, item Expr) Instr {
	return &InstrExpr{Expr: fl.callMethod(types.TypeString, "push", EnsureExpr(strPtr, &types.Ptr{Elem: types.TypeString}), EnsureExpr(item, types.TypeString))}
}
