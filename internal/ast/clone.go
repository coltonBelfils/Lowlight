package ast

// CloneExpr returns a deep structural copy of e so each unrolled `comptime for`
// iteration receives distinct AST node pointers for `BodyMaps` resolution while
// preserving original source spans for diagnostics.
func CloneExpr(e Expr) Expr {
	return cloneExprSubst(e, nil)
}

// CloneExprWithSubst returns a deep structural copy of e replacing free `*IdentExpr`
// references matching keys in `subst` (respecting local shadowing).
func CloneExprWithSubst(e Expr, subst map[string]Expr) Expr {
	if len(subst) == 0 {
		return cloneExprSubst(e, nil)
	}
	return cloneExprSubst(e, subst)
}

func cloneWithoutNames(subst map[string]Expr, names ...string) map[string]Expr {
	if len(subst) == 0 || len(names) == 0 {
		return subst
	}
	copied := false
	cur := subst
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, has := cur[n]; has {
			if !copied {
				next := make(map[string]Expr, len(cur))
				for k, v := range cur {
					next[k] = v
				}
				cur = next
				copied = true
			}
			delete(cur, n)
		}
	}
	return cur
}

func cloneExprSubst(e Expr, subst map[string]Expr) Expr {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *IntLit:
		cp := *v
		return &cp
	case *FloatLit:
		cp := *v
		return &cp
	case *BoolLit:
		cp := *v
		return &cp
	case *CharLit:
		cp := *v
		return &cp
	case *StringLit:
		cp := *v
		return &cp
	case *NoneLit:
		cp := *v
		return &cp
	case *IdentExpr:
		if subst != nil {
			if rep, ok := subst[v.Name]; ok && rep != nil {
				out := cloneExprSubst(rep, nil)
				if idOut, isID := out.(*IdentExpr); isID {
					idOut.IsOpt = v.IsOpt
				}
				return out
			}
		}
		cp := *v
		return &cp
	case *InterpStringExpr:
		cp := *v
		cp.Segments = append([]string(nil), v.Segments...)
		if v.Exprs != nil {
			cp.Exprs = cloneExprsSubst(v.Exprs, subst)
		}
		return &cp
	case *UnaryExpr:
		cp := *v
		cp.Operand = cloneExprSubst(v.Operand, subst)
		return &cp
	case *BinaryExpr:
		cp := *v
		cp.Left = cloneExprSubst(v.Left, subst)
		cp.Right = cloneExprSubst(v.Right, subst)
		return &cp
	case *MemberExpr:
		cp := *v
		cp.Base = cloneExprSubst(v.Base, subst)
		cp.BaseTypeArgs = cloneTypeExprs(v.BaseTypeArgs)
		return &cp
	case *CallExpr:
		cp := *v
		cp.Callee = cloneExprSubst(v.Callee, subst)
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		if v.Args != nil {
			cp.Args = cloneExprsSubst(v.Args, subst)
		}
		return &cp
	case *IndexExpr:
		cp := *v
		cp.Base = cloneExprSubst(v.Base, subst)
		cp.Index = cloneExprSubst(v.Index, subst)
		return &cp
	case *SliceExpr:
		cp := *v
		cp.Base = cloneExprSubst(v.Base, subst)
		cp.Low = cloneExprSubst(v.Low, subst)
		cp.High = cloneExprSubst(v.High, subst)
		return &cp
	case *RangeExpr:
		cp := *v
		cp.Low = cloneExprSubst(v.Low, subst)
		cp.High = cloneExprSubst(v.High, subst)
		return &cp
	case *VecLit:
		cp := *v
		cp.Elems = cloneExprsSubst(v.Elems, subst)
		return &cp
	case *ArrayLit:
		cp := *v
		cp.Count = cloneExprSubst(v.Count, subst)
		cp.Elems = cloneExprsSubst(v.Elems, subst)
		return &cp
	case *SetLit:
		cp := *v
		cp.Elems = cloneExprsSubst(v.Elems, subst)
		return &cp
	case *MapLit:
		cp := *v
		if v.Entries != nil {
			cp.Entries = make([]MapEntry, len(v.Entries))
			for i, ent := range v.Entries {
				cp.Entries[i] = MapEntry{
					Key:   cloneExprSubst(ent.Key, subst),
					Value: cloneExprSubst(ent.Value, subst),
					Span:  ent.Span,
				}
			}
		}
		return &cp
	case *EmptyHashLit:
		cp := *v
		return &cp
	case *TupleLit:
		cp := *v
		cp.Elems = cloneExprsSubst(v.Elems, subst)
		return &cp
	case *StructLit:
		cp := *v
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		if v.Fields != nil {
			cp.Fields = make([]StructFieldInit, len(v.Fields))
			for i, f := range v.Fields {
				cp.Fields[i] = StructFieldInit{
					Name:  f.Name,
					IsOpt: f.IsOpt,
					Value: cloneExprSubst(f.Value, subst),
					Span:  f.Span,
				}
			}
		}
		return &cp
	case *FuncLit:
		cp := *v
		cp.Params = cloneParams(v.Params)
		cp.Ret = CloneTypeExpr(v.Ret)
		innerSubst := subst
		for _, p := range v.Params {
			innerSubst = cloneWithoutNames(innerSubst, p.Name)
		}
		cp.Body = cloneExprSubst(v.Body, innerSubst)
		return &cp
	case *UnsafeExpr:
		cp := *v
		cp.Expr = cloneExprSubst(v.Expr, subst)
		return &cp
	case *ComptimeExpr:
		cp := *v
		cp.Expr = cloneExprSubst(v.Expr, subst)
		return &cp
	case *BlockExpr:
		cp := *v
		curSubst := subst
		if v.Stmts != nil {
			cp.Stmts = make([]Stmt, len(v.Stmts))
			for i, st := range v.Stmts {
				cp.Stmts[i] = cloneStmtSubst(st, curSubst)
				if vs, isVar := st.(*VarStmt); isVar {
					curSubst = cloneWithoutNames(curSubst, vs.Name)
					for _, uv := range vs.UnpackVars {
						curSubst = cloneWithoutNames(curSubst, uv.Name)
					}
				}
			}
		}
		cp.Tail = cloneExprSubst(v.Tail, curSubst)
		return &cp
	case *StmtBodyExpr:
		cp := *v
		cp.Stmt = cloneStmtSubst(v.Stmt, subst)
		return &cp
	case *IfExpr:
		cp := *v
		cp.Cond = cloneExprSubst(v.Cond, subst)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		thenSubst := subst
		if v.BindName != "" {
			thenSubst = cloneWithoutNames(thenSubst, v.BindName)
		}
		cp.Then = cloneExprSubst(v.Then, thenSubst)
		cp.Else = cloneExprSubst(v.Else, subst)
		return &cp
	case *ForExpr:
		cp := *v
		cp.CondOrIter = cloneExprSubst(v.CondOrIter, subst)
		cp.RangeEnd = cloneExprSubst(v.RangeEnd, subst)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		bodySubst := cloneWithoutNames(subst, v.Var1, v.Var2)
		cp.Body = cloneExprSubst(v.Body, bodySubst)
		cp.Else = cloneExprSubst(v.Else, subst)
		return &cp
	case *WhileExpr:
		cp := *v
		cp.CondOrIter = cloneExprSubst(v.CondOrIter, subst)
		cp.RangeEnd = cloneExprSubst(v.RangeEnd, subst)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		bodySubst := cloneWithoutNames(subst, v.Var1, v.Var2)
		cp.Body = cloneExprSubst(v.Body, bodySubst)
		cp.Else = cloneExprSubst(v.Else, subst)
		return &cp
	case *MatchExpr:
		cp := *v
		cp.Scrutinees = cloneExprsSubst(v.Scrutinees, subst)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		if v.Arms != nil {
			cp.Arms = make([]MatchArm, len(v.Arms))
			for i, arm := range v.Arms {
				cArm := arm
				armSubst := subst
				if arm.Cases != nil {
					cArm.Cases = make([]MatchCase, len(arm.Cases))
					for j, mc := range arm.Cases {
						cCase := mc
						if mc.Patterns != nil {
							cCase.Patterns = make([]Pattern, len(mc.Patterns))
							for k, p := range mc.Patterns {
								cCase.Patterns[k] = clonePatternSubst(p, subst)
								if vp, isVP := p.(*VariantPattern); isVP {
									for _, b := range vp.Bindings {
										armSubst = cloneWithoutNames(armSubst, b.BindName)
									}
								}
							}
						}
						cArm.Cases[j] = cCase
					}
				}
				cArm.Body = cloneExprSubst(arm.Body, armSubst)
				cp.Arms[i] = cArm
			}
		}
		return &cp
	default:
		return e
	}
}

func cloneExprs(es []Expr) []Expr {
	return cloneExprsSubst(es, nil)
}

func cloneExprsSubst(es []Expr, subst map[string]Expr) []Expr {
	if es == nil {
		return nil
	}
	out := make([]Expr, len(es))
	for i, e := range es {
		out[i] = cloneExprSubst(e, subst)
	}
	return out
}

func CloneStmt(s Stmt) Stmt {
	return cloneStmtSubst(s, nil)
}

func cloneStmtSubst(s Stmt, subst map[string]Expr) Stmt {
	if s == nil {
		return nil
	}
	switch v := s.(type) {
	case *VarStmt:
		cp := *v
		cp.Type = CloneTypeExpr(v.Type)
		if v.UnpackVars != nil {
			cp.UnpackVars = make([]UnpackVar, len(v.UnpackVars))
			for i, uv := range v.UnpackVars {
				cp.UnpackVars[i] = UnpackVar{
					Name:      uv.Name,
					IsOptName: uv.IsOptName,
					Type:      CloneTypeExpr(uv.Type),
					Span:      uv.Span,
				}
			}
		}
		cp.Value = cloneExprSubst(v.Value, subst)
		if v.FallbackUnwind != nil {
			fu := *v.FallbackUnwind
			fu.Value = cloneExprSubst(fu.Value, subst)
			cp.FallbackUnwind = &fu
		}
		return &cp
	case *AssignStmt:
		cp := *v
		cp.Target = cloneExprSubst(v.Target, subst)
		cp.UnpackTargets = cloneExprsSubst(v.UnpackTargets, subst)
		cp.Value = cloneExprSubst(v.Value, subst)
		if v.FallbackUnwind != nil {
			fu := *v.FallbackUnwind
			fu.Value = cloneExprSubst(fu.Value, subst)
			cp.FallbackUnwind = &fu
		}
		return &cp
	case *UnwindStmt:
		cp := *v
		cp.Value = cloneExprSubst(v.Value, subst)
		return &cp
	case *DeclStmt:
		cp := *v
		cp.Decl = CloneDecl(v.Decl)
		return &cp
	case *ExprStmt:
		cp := *v
		cp.Expr = cloneExprSubst(v.Expr, subst)
		return &cp
	default:
		return s
	}
}

func ClonePattern(p Pattern) Pattern {
	return clonePatternSubst(p, nil)
}

func clonePatternSubst(p Pattern, subst map[string]Expr) Pattern {
	if p == nil {
		return nil
	}
	switch v := p.(type) {
	case *ExprPattern:
		cp := *v
		cp.Expr = cloneExprSubst(v.Expr, subst)
		return &cp
	case *ContainsPattern:
		cp := *v
		cp.Container = cloneExprSubst(v.Container, subst)
		return &cp
	case *MethodPattern:
		cp := *v
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		cp.Args = cloneExprsSubst(v.Args, subst)
		return &cp
	case *WildcardPattern:
		cp := *v
		return &cp
	case *VariantPattern:
		cp := *v
		cp.Target = cloneExprSubst(v.Target, subst)
		cp.Bindings = append([]VariantPatternBinding(nil), v.Bindings...)
		return &cp
	default:
		return p
	}
}

func CloneTypeExpr(te TypeExpr) TypeExpr {
	if te == nil {
		return nil
	}
	switch v := te.(type) {
	case *NamedType:
		cp := *v
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		return &cp
	case *MutBorrowType:
		cp := *v
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *OwnType:
		cp := *v
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *OptionalType:
		cp := *v
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *VectorType:
		cp := *v
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *StackArrayType:
		cp := *v
		cp.Size = CloneExpr(v.Size)
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *SetType:
		cp := *v
		cp.Elem = CloneTypeExpr(v.Elem)
		return &cp
	case *MapType:
		cp := *v
		cp.Key = CloneTypeExpr(v.Key)
		cp.Value = CloneTypeExpr(v.Value)
		return &cp
	case *TupleType:
		cp := *v
		cp.Elems = cloneTypeExprs(v.Elems)
		return &cp
	case *ConstArgType:
		cp := *v
		cp.Expr = CloneExpr(v.Expr)
		return &cp
	case *FuncType:
		cp := *v
		cp.Params = cloneParams(v.Params)
		cp.Ret = CloneTypeExpr(v.Ret)
		return &cp
	default:
		return te
	}
}

func cloneTypeExprs(tes []TypeExpr) []TypeExpr {
	if tes == nil {
		return nil
	}
	out := make([]TypeExpr, len(tes))
	for i, te := range tes {
		out[i] = CloneTypeExpr(te)
	}
	return out
}

func cloneParams(ps []Param) []Param {
	if ps == nil {
		return nil
	}
	out := make([]Param, len(ps))
	for i, p := range ps {
		cp := p
		cp.Type = CloneTypeExpr(p.Type)
		out[i] = cp
	}
	return out
}

func cloneAttributes(attrs []Attribute) []Attribute {
	if attrs == nil {
		return nil
	}
	out := make([]Attribute, len(attrs))
	for i, a := range attrs {
		cp := a
		cp.Args = append([]string(nil), a.Args...)
		out[i] = cp
	}
	return out
}

func cloneTypeParams(tps []TypeParam) []TypeParam {
	if tps == nil {
		return nil
	}
	out := make([]TypeParam, len(tps))
	for i, tp := range tps {
		cp := tp
		cp.ConstType = CloneTypeExpr(tp.ConstType)
		cp.Bounds = append([]string(nil), tp.Bounds...)
		out[i] = cp
	}
	return out
}

func cloneDecls(ds []Decl) []Decl {
	if ds == nil {
		return nil
	}
	out := make([]Decl, len(ds))
	for i, d := range ds {
		out[i] = CloneDecl(d)
	}
	return out
}

func cloneTopComptimeIfDecl(v *TopComptimeIfDecl) *TopComptimeIfDecl {
	if v == nil {
		return nil
	}
	cp := *v
	cp.Cond = CloneExpr(v.Cond)
	cp.ThenDecls = cloneDecls(v.ThenDecls)
	cp.ElseIf = cloneTopComptimeIfDecl(v.ElseIf)
	cp.ElseDecls = cloneDecls(v.ElseDecls)
	return &cp
}

func CloneDecl(d Decl) Decl {
	if d == nil {
		return nil
	}
	switch v := d.(type) {
	case *ImportDecl:
		cp := *v
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *ExternHeaderDecl:
		cp := *v
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *ExternFnDecl:
		cp := *v
		cp.Params = cloneParams(v.Params)
		cp.Ret = CloneTypeExpr(v.Ret)
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *StructDecl:
		cp := *v
		cp.TypeParams = cloneTypeParams(v.TypeParams)
		if v.Fields != nil {
			cp.Fields = make([]StructField, len(v.Fields))
			for i, f := range v.Fields {
				cf := f
				cf.Type = CloneTypeExpr(f.Type)
				cf.Default = CloneExpr(f.Default)
				cf.Attributes = cloneAttributes(f.Attributes)
				cp.Fields[i] = cf
			}
		}
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *EnumDecl:
		cp := *v
		cp.TypeParams = cloneTypeParams(v.TypeParams)
		cp.BaseType = CloneTypeExpr(v.BaseType)
		if v.Variants != nil {
			cp.Variants = make([]EnumVariant, len(v.Variants))
			for i, vr := range v.Variants {
				cv := vr
				cv.Value = CloneExpr(vr.Value)
				cv.PayloadType = CloneTypeExpr(vr.PayloadType)
				cv.Attributes = cloneAttributes(vr.Attributes)
				cp.Variants[i] = cv
			}
		}
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *InterfaceDecl:
		cp := *v
		cp.TypeParams = cloneTypeParams(v.TypeParams)
		if v.Methods != nil {
			cp.Methods = make([]InterfaceMethod, len(v.Methods))
			for i, m := range v.Methods {
				cm := m
				cm.Params = cloneParams(m.Params)
				cm.Ret = CloneTypeExpr(m.Ret)
				cp.Methods[i] = cm
			}
		}
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *DistinctTypeDecl:
		cp := *v
		cp.Base = CloneTypeExpr(v.Base)
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *VarDecl:
		cp := *v
		cp.TypeParams = cloneTypeParams(v.TypeParams)
		cp.Type = CloneTypeExpr(v.Type)
		cp.Value = CloneExpr(v.Value)
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *MethodDecl:
		cp := *v
		cp.RecvTypeParams = append([]string(nil), v.RecvTypeParams...)
		cp.TypeParams = cloneTypeParams(v.TypeParams)
		cp.RecvExplicitTy = CloneTypeExpr(v.RecvExplicitTy)
		cp.Params = cloneParams(v.Params)
		cp.Ret = CloneTypeExpr(v.Ret)
		cp.Body = CloneExpr(v.Body)
		cp.Attributes = cloneAttributes(v.Attributes)
		return &cp
	case *TopComptimeIfDecl:
		return cloneTopComptimeIfDecl(v)
	default:
		return d
	}
}
