package sema

import (
	"lowlight/internal/ast"
	"lowlight/internal/diag"
)

// Diagnostics about a whole expression underline only the part that discloses the problem,
// instead of every line of a large expression:
//   - a call underlines its function or method name (`parse_config` in `parse_config(text, opts)`);
//   - a member access underlines the member (`len` in `items.len`);
//   - a block underlines the expression it evaluates to;
//   - `if`, `match`, `for`, and `while` underline their header (`match(status):`);
//   - a result chain underlines its subject and `:`;
//   - anything else spanning several lines is clipped to its first line.

// focusSpan returns the span a diagnostic about expression `e` should underline.
func (a *Analyzer) focusSpan(e ast.Expr) diag.Span {
	if e == nil {
		return diag.Span{}
	}
	switch v := e.(type) {
	case *ast.CallExpr:
		if sp, ok := calleeNameSpan(v.Callee); ok {
			return sp
		}
	case *ast.MemberExpr:
		if sp, ok := memberNameSpan(v); ok {
			return sp
		}
	case *ast.UnaryExpr:
		if v.Operand != nil && v.Span.Start.Line != v.Span.End.Line {
			return a.focusSpan(v.Operand)
		}
	case *ast.BlockExpr:
		if v.Tail != nil {
			return a.focusSpan(v.Tail)
		}
		if len(v.Stmts) > 0 {
			if es, ok := v.Stmts[len(v.Stmts)-1].(*ast.ExprStmt); ok {
				return a.focusSpan(es.Expr)
			}
		}
		return a.firstLine(v.Span)
	case *ast.StmtBodyExpr:
		return a.firstLine(v.Span)
	case *ast.IfExpr:
		return a.headerSpan(v.Span, v.Cond)
	case *ast.ForExpr:
		return a.headerSpan(v.Span, v.CondOrIter)
	case *ast.WhileExpr:
		return a.headerSpan(v.Span, v.CondOrIter)
	case *ast.MatchExpr:
		return a.matchHeaderSpan(v)
	}
	return a.firstLine(e.GetSpan())
}

// matchHeaderSpan is `match(x):` for a match, or `subject:` for a result chain.
func (a *Analyzer) matchHeaderSpan(me *ast.MatchExpr) diag.Span {
	if me.ResultChain && len(me.Scrutinees) > 0 {
		sp := a.firstLine(me.Scrutinees[0].GetSpan())
		sp.Start = me.Span.Start
		return a.extend(sp, 1) // the chain's `:`
	}
	var last ast.Expr
	if len(me.Scrutinees) > 0 {
		last = me.Scrutinees[len(me.Scrutinees)-1]
	}
	return a.headerSpan(me.Span, last)
}

// headerSpan covers a control-flow header from its keyword through `(cond):`. Without a
// condition (`match:`), the keyword's first line is used.
func (a *Analyzer) headerSpan(whole diag.Span, cond ast.Expr) diag.Span {
	if cond == nil {
		return a.firstLine(whole)
	}
	end := cond.GetSpan().End
	sp := diag.Span{Start: whole.Start, End: end}
	if end.Line != whole.Start.Line {
		return a.firstLine(sp)
	}
	return a.extend(sp, 2) // `):`
}

// extend moves a single-line span's end `n` columns right, staying within its line.
func (a *Analyzer) extend(sp diag.Span, n int) diag.Span {
	if sp.End.Line != sp.Start.Line {
		return sp
	}
	if ll, ok := a.Rep.LineLen(sp.End.File, sp.End.Line); ok && sp.End.Col-1+n > ll {
		n = ll - (sp.End.Col - 1)
	}
	if n <= 0 {
		return sp
	}
	sp.End.Offset += n
	sp.End.Col += n
	return sp
}

// firstLine clips a multi-line span to the end of its first line.
func (a *Analyzer) firstLine(sp diag.Span) diag.Span {
	if sp.End.Line == sp.Start.Line || sp.Start.Line == 0 {
		return sp
	}
	ll, ok := a.Rep.LineLen(sp.Start.File, sp.Start.Line)
	if !ok {
		return sp
	}
	n := ll - (sp.Start.Col - 1)
	sp.End = diag.Pos{File: sp.Start.File, Offset: sp.Start.Offset + n, Line: sp.Start.Line, Col: sp.Start.Col + n}
	return sp
}

func calleeNameSpan(callee ast.Expr) (diag.Span, bool) {
	switch c := callee.(type) {
	case *ast.IdentExpr:
		return c.Span, c.Span.Start.Line == c.Span.End.Line
	case *ast.MemberExpr:
		return memberNameSpan(c)
	}
	return diag.Span{}, false
}

// memberNameSpan is the trailing `name` of `base.name` (assumed to end the member expression).
func memberNameSpan(m *ast.MemberExpr) (diag.Span, bool) {
	n := len(m.Member)
	end := m.Span.End
	if n == 0 || end.Col-1 < n {
		return diag.Span{}, false
	}
	start := diag.Pos{File: end.File, Offset: end.Offset - n, Line: end.Line, Col: end.Col - n}
	return diag.Span{Start: start, End: end}, true
}
