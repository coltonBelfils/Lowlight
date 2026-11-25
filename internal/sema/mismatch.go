package sema

import (
	"fmt"
	"strings"

	"lowlight/internal/types"
)

// mismatchMessage words a type mismatch for the place it happened, so the message says what was
// expected there and what the expression is: "`describe` returns `string`, but this is `int`".
// Messages keep the "type mismatch" prefix so they are easy to search for.
func mismatchMessage(context string, expected, got types.Type) string {
	e, g := "`"+expected.String()+"`", "`"+got.String()+"`"
	name := func(prefix string) string {
		return "`" + strings.Trim(strings.TrimPrefix(context, prefix), `"`) + "`"
	}
	var body string
	switch {
	case strings.HasPrefix(context, "return value of "):
		body = fmt.Sprintf("%s returns %s, but this is %s", name("return value of "), e, g)
	case strings.HasPrefix(context, "declaration of "):
		body = fmt.Sprintf("%s is declared as %s, but this is %s", name("declaration of "), e, g)
	case context == "variable declaration" || context == "top-level bind":
		body = fmt.Sprintf("the variable is declared as %s, but this is %s", e, g)
	case context == "assignment":
		body = fmt.Sprintf("cannot assign %s to a place of type %s", g, e)
	case strings.HasPrefix(context, "argument ") || strings.HasPrefix(context, "mutable argument "):
		body = fmt.Sprintf("%s expects %s, but this is %s", context, e, g)
	case context == "block expression":
		body = fmt.Sprintf("this block must evaluate to %s, but its last expression is %s", e, g)
	case context == "`if` then-branch" || context == "`if` else-branch":
		body = fmt.Sprintf("this `if` must evaluate to %s, but this branch is %s", e, g)
	case context == "`match` arm":
		body = fmt.Sprintf("this `match` must evaluate to %s, but this arm is %s", e, g)
	case context == "unwind value":
		body = fmt.Sprintf("this unwind must carry %s, but this is %s", e, g)
	case context == "`??` fallback":
		body = fmt.Sprintf("the `??` fallback must be %s, the type being unwrapped, but this is %s", e, g)
	case strings.HasPrefix(context, "field "):
		body = fmt.Sprintf("%s has type %s, but this is %s", context, e, g)
	case strings.HasSuffix(context, " payload"):
		body = fmt.Sprintf("`%s` holds %s, but this is %s", strings.TrimSuffix(context, " payload"), e, g)
	default:
		body = fmt.Sprintf("%s expects %s, but this is %s", context, e, g)
	}
	return "type mismatch: " + body
}

// mismatchHint suggests the usual fix for a mismatch between two types, or "". `fix` names the
// fix for editor quick fixes: "convert:T", "to_string", "unwrap_optional", "handle_result",
// "wrap_value", "wrap_failure:Name", or "nonzero".
func mismatchHint(expected, got types.Type, implements func(types.Type, *types.Interface) bool, printable bool) (hint, fix string) {
	ep, eIsPrim := expected.(*types.Primitive)
	gp, gIsPrim := got.(*types.Primitive)
	switch {
	case eIsPrim && gIsPrim && ep.IsNumeric() && gp.IsNumeric():
		return fmt.Sprintf("numbers never convert implicitly; write `%s(...)` to convert", expected.String()), "convert:" + expected.String()
	case expected.Equals(types.TypeBool) && gIsPrim && gp.IsNumeric():
		return "Lowlight has no implicit truthiness; compare explicitly, e.g. `x != 0`", "nonzero"
	case expected.Equals(types.TypeString) && (gIsPrim || printable):
		return "convert with `.to_string()` or interpolate it: `\"$value\"`", "to_string"
	}
	if gotOpt, ok := got.(*types.Optional); ok && gotOpt.Elem.Equals(expected) {
		return "unwrap the optional first: `x ?? fallback`, `if (x as v)`, or `?.` chaining", "unwrap_optional"
	}
	if en := types.AsResult(got); en != nil {
		if vv, _ := en.ResultValue(); vv != nil && vv.Payload != nil && vv.Payload.Equals(expected) {
			return "handle the failure first: a result chain (`expr: default panic`), `expr ?? fallback`, or `if (expr as v)`", "handle_result"
		}
	}
	if en := types.AsResult(expected); en != nil {
		if vv, _ := en.ResultValue(); vv != nil && vv.Payload != nil && vv.Payload.Equals(got) {
			return "construct the success value: `value(...)`", "wrap_value"
		}
		for _, f := range en.ResultFailures() {
			if f.Payload != nil && f.Payload.Equals(got) {
				return fmt.Sprintf("construct the failure: `%s(...)`", f.Name), "wrap_failure:" + f.Name
			}
		}
	}
	if ifc, ok := expected.(*types.Interface); ok && implements != nil && !implements(got, ifc) {
		if _, isStruct := got.(*types.Struct); isStruct {
			return fmt.Sprintf("`%s` does not implement `%s`; add its methods (the \"Implement %s\" quick fix on the type can generate them)", got.String(), ifc.String(), ifc.Name), ""
		}
		return fmt.Sprintf("`%s` does not implement `%s`", got.String(), ifc.String()), ""
	}
	return "", ""
}
