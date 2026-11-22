// examples/mini_lexer_eval.ll
// A self-contained arithmetic + variable expression lexer, Arena-backed
// recursive-descent AST parser, and tree-walking evaluator written in Lowlight!
// Demonstrates how ergonomic Lowlight already is for writing compiler passes.

@derive(Equatable)
define TokKind enum [
    num,
    ident,
    plus,
    minus,
    star,
    slash,
    l_paren,
    r_paren,
    end,
]

define Token struct [
    kind: TokKind,
    text: string,
    num: float,
]

bind tokenize = (src: string) [Token]: {
    let toks: [Token] = []
    let i = 0
    let n = src.len()

    for (i < n): {
        let ch = char(src.byte_at(i))
        if (ch.is_whitespace()): {
            i += 1
            next <-
        }
        if (ch.is_digit()): {
            let start = i
            for (i < n): {
                let c2 = char(src.byte_at(i))
                if (c2.is_digit() or c2 == '.'): {
                    i += 1
                } else: {
                    for <-
                }
            }
            let slice = src[start..i].copy()
            let val = parse_float(slice) ?? 0.0
            toks.push(Token[kind: TokKind.num, text: own slice, num: val])
            next <-
        }
        if (ch.is_alpha() or ch == '_'): {
            let start = i
            for (i < n): {
                let c2 = char(src.byte_at(i))
                if (c2.is_alnum() or c2 == '_'): {
                    i += 1
                } else: {
                    for <-
                }
            }
            toks.push(Token[kind: TokKind.ident, text: src[start..i].copy(), num: 0.0])
            next <-
        }

        let single_kind = match (ch):
        when '+': TokKind.plus
        when '-': TokKind.minus
        when '*': TokKind.star
        when '/': TokKind.slash
        when '(': TokKind.l_paren
        when ')': TokKind.r_paren
        default:  panic("unexpected character in expression: ${string(ch)}")

        toks.push(Token[kind: single_kind, text: string(ch), num: 0.0])
        i += 1
    }

    toks.push(Token[kind: TokKind.end, text: "", num: 0.0])
    toks
}

define NodeKind enum [
    lit,
    var,
    neg,
    bin,
]

define ExprNode struct [
    kind: NodeKind,
    op: TokKind,
    num: float,
    name: string,
    left?: ?ref[ExprNode],
    right?: ?ref[ExprNode],
]

define ParserState struct [
    tokens: [Token],
    pos: int,
]

bind ParserState.peek = (self) TokKind: {
    self.tokens[self.pos].kind
}

bind parse_expr = (arena: &Arena, st: &ParserState) ref[ExprNode]: {
    let lhs = parse_term(&arena, &st)
    for (st.peek() == TokKind.plus or st.peek() == TokKind.minus): {
        let op = st.peek()
        st.pos += 1
        let rhs = parse_term(&arena, &st)
        lhs = arena.alloc(ExprNode[
            kind: NodeKind.bin,
            op: op,
            num: 0.0,
            name: "",
            left?: lhs,
            right?: rhs,
        ])
    }
    lhs
}

bind parse_term = (arena: &Arena, st: &ParserState) ref[ExprNode]: {
    let lhs = parse_unary(&arena, &st)
    for (st.peek() == TokKind.star or st.peek() == TokKind.slash): {
        let op = st.peek()
        st.pos += 1
        let rhs = parse_unary(&arena, &st)
        lhs = arena.alloc(ExprNode[
            kind: NodeKind.bin,
            op: op,
            num: 0.0,
            name: "",
            left?: lhs,
            right?: rhs,
        ])
    }
    lhs
}

bind parse_unary = (arena: &Arena, st: &ParserState) ref[ExprNode]: {
    if (st.peek() == TokKind.minus): {
        st.pos += 1
        let sub = parse_unary(&arena, &st)
        func <- arena.alloc(ExprNode[
            kind: NodeKind.neg,
            op: TokKind.minus,
            num: 0.0,
            name: "",
            left?: sub,
            right?: none,
        ])
    }
    parse_primary(&arena, &st)
}

bind parse_primary = (arena: &Arena, st: &ParserState) ref[ExprNode]: {
    let k = st.peek()
    if (k == TokKind.num): {
        let v = st.tokens[st.pos].num
        st.pos += 1
        func <- arena.alloc(ExprNode[
            kind: NodeKind.lit,
            op: TokKind.end,
            num: v,
            name: "",
            left?: none,
            right?: none,
        ])
    }
    if (k == TokKind.ident): {
        let nm = st.tokens[st.pos].text.copy()
        st.pos += 1
        func <- arena.alloc(ExprNode[
            kind: NodeKind.var,
            op: TokKind.end,
            num: 0.0,
            name: own nm,
            left?: none,
            right?: none,
        ])
    }
    if (k == TokKind.l_paren): {
        st.pos += 1
        let inner = parse_expr(&arena, &st)
        assert(st.peek() == TokKind.r_paren, "expected closing ')'")
        st.pos += 1
        func <- inner
    }
    panic("unexpected token in primary expression")
}

bind eval_node = (node: ref[ExprNode], env: #[string: float]) float: {
    match (node.kind):
    when lit: node.num
    when var: env[node.name] ?? panic("unbound variable: ${node.name}")
    when neg: {
        let l = node.left? ?? panic("missing neg operand")
        -eval_node(l, env)
    }
    default bin: {
        let l = node.left? ?? panic("missing left operand")
        let r = node.right? ?? panic("missing right operand")
        let lv = eval_node(l, env)
        let rv = eval_node(r, env)
        match (node.op):
        when plus:  lv + rv
        when minus: lv - rv
        when star:  lv * rv
        default:    lv / rv
    }
}

bind evaluate = (src: string, env: #[string: float]) float: {
    let arena = Arena.new()
    let st = ParserState[tokens: tokenize(src), pos: 0]
    let root = parse_expr(&arena, &st)
    assert(st.peek() == TokKind.end, "unexpected trailing tokens")
    eval_node(root, env)
}

bind main = (): {
    let vars = #[
        "x": 10.0,
        "y": 3.5,
        "scale": 4.0,
    ]
    let expr = "(x + y * 2) * scale - -5"
    let result = evaluate(expr, vars)
    // (10 + 7) * 4 - (-5) = 68 + 5 = 73
    assert(result == 73.0)
    println("mini_lexer_eval: '$expr' => $result")
}
