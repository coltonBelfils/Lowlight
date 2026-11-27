package types_test

import (
	"testing"

	"lowlight/internal/types"
)

func TestStackArrayEqualsStrictSize(t *testing.T) {
	arr0 := &types.StackArray{Size: 0, Elem: types.TypeInt}
	arr3 := &types.StackArray{Size: 3, Elem: types.TypeInt}
	arr4 := &types.StackArray{Size: 4, Elem: types.TypeInt}
	arr3Str := &types.StackArray{Size: 3, Elem: types.TypeString}

	if !arr0.Equals(&types.StackArray{Size: 0, Elem: types.TypeInt}) {
		t.Fatal("expected [0: int] to equal [0: int]")
	}
	if !arr3.Equals(&types.StackArray{Size: 3, Elem: types.TypeInt}) {
		t.Fatal("expected [3: int] to equal [3: int]")
	}
	if arr0.Equals(arr3) || arr3.Equals(arr0) {
		t.Fatal("StackArray.Size == 0 must not act as a size wildcard")
	}
	if arr3.Equals(arr4) {
		t.Fatal("expected [3: int] not to equal [4: int]")
	}
	if arr3.Equals(arr3Str) {
		t.Fatal("expected [3: int] not to equal [3: string]")
	}

	viewArr := &types.Struct{
		Name:     "Array",
		Lang:     "array",
		TypeArgs: []types.Type{types.TypeInt},
	}
	if arr3.Equals(viewArr) || viewArr.Equals(arr3) {
		t.Fatal("expected [3: int] not to equal 1-arg Array[int] view type")
	}

	userArr := &types.Struct{
		Name:     "Array",
		TypeArgs: []types.Type{types.NewConstInt(3, 3, types.TypeInt), types.TypeInt},
	}
	if arr3.Equals(userArr) || userArr.Equals(arr3) {
		t.Fatal("expected user struct Array[3, int] without Lang==\"array\" not to equal [3: int]")
	}

	coreUnsignedArr := &types.Struct{
		Name:     "Array",
		Lang:     "array",
		TypeArgs: []types.Type{types.NewConstInt(0, 3, types.TypeU64), types.TypeInt},
	}
	if !arr3.Equals(coreUnsignedArr) || !coreUnsignedArr.Equals(arr3) {
		t.Fatal("expected [3: int] to equal core Array[u64(3), int]")
	}

	vecInt := &types.Vector{Elem: types.TypeInt}
	userList := &types.Struct{Name: "List", TypeArgs: []types.Type{types.TypeInt}}
	if vecInt.Equals(userList) || userList.Equals(vecInt) {
		t.Fatal("expected user struct List[int] without Lang==\"list\" not to equal [int]")
	}
	coreList := &types.Struct{Name: "List", Lang: "list", TypeArgs: []types.Type{types.TypeInt}}
	if !vecInt.Equals(coreList) || !coreList.Equals(vecInt) {
		t.Fatal("expected core List[int] to equal [int]")
	}
}

// Structs only get `==` from an `eq` method (written or `@derive(Equatable)`), which sema checks.
func TestSupportsEqRejectsResourceAndDropStructs(t *testing.T) {
	plainPt := &types.Struct{
		Name: "Pt",
		Fields: []types.StructField{
			{Name: "x", Type: types.TypeInt},
			{Name: "y", Type: types.TypeInt},
		},
	}
	if types.SupportsEq(plainPt) {
		t.Fatal("expected struct Pt to have no built-in == (it needs @derive(Equatable) or an eq method)")
	}

	dropHandle := &types.Struct{
		Name:          "Handle",
		HasCustomDrop: true,
		Fields: []types.StructField{
			{Name: "fd", Type: types.TypeInt},
		},
	}
	if types.SupportsEq(dropHandle) {
		t.Fatal("expected Drop struct Handle to reject derived ==")
	}

	resUser := &types.Struct{
		Name: "User",
		Fields: []types.StructField{
			{Name: "name", Type: types.TypeString},
		},
	}
	if types.SupportsEq(resUser) {
		t.Fatal("expected Resource struct User (containing string) to reject derived ==")
	}

	nestedDrop := &types.Struct{
		Name: "Wrapper",
		Fields: []types.StructField{
			{Name: "h", Type: dropHandle},
		},
	}
	if types.SupportsEq(nestedDrop) {
		t.Fatal("expected struct wrapping Drop struct to reject derived ==")
	}
}
