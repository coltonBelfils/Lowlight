package ast

// CloneExpr returns a deep structural copy of e so each unrolled `comptime for`
// iteration receives distinct AST node pointers for `BodyMaps` resolution while
// preserving original source spans for diagnostics.
func CloneExpr(e Expr) Expr {
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
		cp := *v
		return &cp
	case *InterpStringExpr:
		cp := *v
		cp.Segments = append([]string(nil), v.Segments...)
		if v.Exprs != nil {
			cp.Exprs = make([]Expr, len(v.Exprs))
			for i, ex := range v.Exprs {
				cp.Exprs[i] = CloneExpr(ex)
			}
		}
		return &cp
	case *UnaryExpr:
		cp := *v
		cp.Operand = CloneExpr(v.Operand)
		return &cp
	case *BinaryExpr:
		cp := *v
		cp.Left = CloneExpr(v.Left)
		cp.Right = CloneExpr(v.Right)
		return &cp
	case *MemberExpr:
		cp := *v
		cp.Base = CloneExpr(v.Base)
		cp.BaseTypeArgs = cloneTypeExprs(v.BaseTypeArgs)
		return &cp
	case *CallExpr:
		cp := *v
		cp.Callee = CloneExpr(v.Callee)
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		if v.Args != nil {
			cp.Args = make([]Expr, len(v.Args))
			for i, a := range v.Args {
				cp.Args[i] = CloneExpr(a)
			}
		}
		return &cp
	case *IndexExpr:
		cp := *v
		cp.Base = CloneExpr(v.Base)
		cp.Index = CloneExpr(v.Index)
		return &cp
	case *SliceExpr:
		cp := *v
		cp.Base = CloneExpr(v.Base)
		cp.Low = CloneExpr(v.Low)
		cp.High = CloneExpr(v.High)
		return &cp
	case *RangeExpr:
		cp := *v
		cp.Low = CloneExpr(v.Low)
		cp.High = CloneExpr(v.High)
		return &cp
	case *VecLit:
		cp := *v
		cp.Elems = cloneExprs(v.Elems)
		return &cp
	case *ArrayLit:
		cp := *v
		cp.Count = CloneExpr(v.Count)
		cp.Elems = cloneExprs(v.Elems)
		return &cp
	case *SetLit:
		cp := *v
		cp.Elems = cloneExprs(v.Elems)
		return &cp
	case *MapLit:
		cp := *v
		if v.Entries != nil {
			cp.Entries = make([]MapEntry, len(v.Entries))
			for i, ent := range v.Entries {
				cp.Entries[i] = MapEntry{
					Key:   CloneExpr(ent.Key),
					Value: CloneExpr(ent.Value),
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
		cp.Elems = cloneExprs(v.Elems)
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
					Value: CloneExpr(f.Value),
					Span:  f.Span,
				}
			}
		}
		return &cp
	case *FuncLit:
		cp := *v
		cp.Params = cloneParams(v.Params)
		cp.Ret = CloneTypeExpr(v.Ret)
		cp.Body = CloneExpr(v.Body)
		return &cp
	case *UnsafeExpr:
		cp := *v
		cp.Expr = CloneExpr(v.Expr)
		return &cp
	case *ComptimeExpr:
		cp := *v
		cp.Expr = CloneExpr(v.Expr)
		return &cp
	case *BlockExpr:
		cp := *v
		if v.Stmts != nil {
			cp.Stmts = make([]Stmt, len(v.Stmts))
			for i, st := range v.Stmts {
				cp.Stmts[i] = CloneStmt(st)
			}
		}
		cp.Tail = CloneExpr(v.Tail)
		return &cp
	case *StmtBodyExpr:
		cp := *v
		cp.Stmt = CloneStmt(v.Stmt)
		return &cp
	case *IfExpr:
		cp := *v
		cp.Cond = CloneExpr(v.Cond)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		cp.Then = CloneExpr(v.Then)
		cp.Else = CloneExpr(v.Else)
		return &cp
	case *ForExpr:
		cp := *v
		cp.CondOrIter = CloneExpr(v.CondOrIter)
		cp.RangeEnd = CloneExpr(v.RangeEnd)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		cp.Body = CloneExpr(v.Body)
		cp.Else = CloneExpr(v.Else)
		return &cp
	case *WhileExpr:
		cp := *v
		cp.CondOrIter = CloneExpr(v.CondOrIter)
		cp.RangeEnd = CloneExpr(v.RangeEnd)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		cp.Body = CloneExpr(v.Body)
		cp.Else = CloneExpr(v.Else)
		return &cp
	case *MatchExpr:
		cp := *v
		cp.Scrutinees = cloneExprs(v.Scrutinees)
		cp.EvalType = CloneTypeExpr(v.EvalType)
		if v.Arms != nil {
			cp.Arms = make([]MatchArm, len(v.Arms))
			for i, arm := range v.Arms {
				cArm := arm
				cArm.Body = CloneExpr(arm.Body)
				if arm.Cases != nil {
					cArm.Cases = make([]MatchCase, len(arm.Cases))
					for j, mc := range arm.Cases {
						cCase := mc
						if mc.Patterns != nil {
							cCase.Patterns = make([]Pattern, len(mc.Patterns))
							for k, p := range mc.Patterns {
								cCase.Patterns[k] = ClonePattern(p)
							}
						}
						cArm.Cases[j] = cCase
					}
				}
				cp.Arms[i] = cArm
			}
		}
		return &cp
	default:
		return e
	}
}

func cloneExprs(es []Expr) []Expr {
	if es == nil {
		return nil
	}
	out := make([]Expr, len(es))
	for i, e := range es {
		out[i] = CloneExpr(e)
	}
	return out
}

func CloneStmt(s Stmt) Stmt {
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
		cp.Value = CloneExpr(v.Value)
		if v.FallbackUnwind != nil {
			fu := *v.FallbackUnwind
			fu.Value = CloneExpr(fu.Value)
			cp.FallbackUnwind = &fu
		}
		return &cp
	case *AssignStmt:
		cp := *v
		cp.Target = CloneExpr(v.Target)
		cp.UnpackTargets = cloneExprs(v.UnpackTargets)
		cp.Value = CloneExpr(v.Value)
		if v.FallbackUnwind != nil {
			fu := *v.FallbackUnwind
			fu.Value = CloneExpr(fu.Value)
			cp.FallbackUnwind = &fu
		}
		return &cp
	case *UnwindStmt:
		cp := *v
		cp.Value = CloneExpr(v.Value)
		return &cp
	case *DeclStmt:
		cp := *v
		cp.Decl = CloneDecl(v.Decl)
		return &cp
	case *ExprStmt:
		cp := *v
		cp.Expr = CloneExpr(v.Expr)
		return &cp
	default:
		return s
	}
}

func ClonePattern(p Pattern) Pattern {
	if p == nil {
		return nil
	}
	switch v := p.(type) {
	case *ExprPattern:
		cp := *v
		cp.Expr = CloneExpr(v.Expr)
		return &cp
	case *ContainsPattern:
		cp := *v
		cp.Container = CloneExpr(v.Container)
		return &cp
	case *MethodPattern:
		cp := *v
		cp.TypeArgs = cloneTypeExprs(v.TypeArgs)
		cp.Args = cloneExprs(v.Args)
		return &cp
	case *WildcardPattern:
		cp := *v
		return &cp
	case *VariantPattern:
		cp := *v
		cp.Target = CloneExpr(v.Target)
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
