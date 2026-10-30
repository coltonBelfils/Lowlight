// examples/control_flow_unwinds.ll
// Showcases Lowlight's unified `<-` unwind expressions:
// block `<-`, `if <-`, `match <-`, `for <-`, `next <-`, labeled loops,
// `for ... else`, and `?? func <-` optional guard returns.

bind find_first_divisible = (nums: [int], divisor: int): int {
    // `for (...) { ... } else ...` produces a value directly!
    for (n in nums) {
        if (n <= 0) {
            next <-
        }
        if (n % divisor == 0) {
            for <- n
        }
    } else -1
}

bind find_in_grid = (grid: [[int]], target: int): string {
    let coords = outer: for (r, row in grid) {
        for (c, cell in row) {
            if (cell < 0) {
                // Skip rest of this row and jump to next outer row!
                next outer <-
            }
            if (cell == target) {
                outer <- "($r, $c)"
            }
        }
    } else "not-found"
    coords
}

bind parse_and_scale = (raw: string, factor_table: #[string: int], key: string): ?int {
    // `?? func <- none` unwraps `?T` or exits the function cleanly on one line!
    let base = parse_int(raw) ?? func <- none
    let mult = factor_table[key] ?? func <- none

    // Value block + `if <-` + `match <-`
    let bonus = {
        if (base == 0) {
            <- 0
        }
        let b = if (mult > 5) {
            let extra = mult * 2
            if <- extra
        } else {
            1
        }
        <- b
    }

    let adjusted = match (base * mult + bonus)
    when 0: {
        match <- 0
    }
    else: {
        match <- base * mult + bonus
    }

    adjusted
}

bind main = () {
    let d1 = find_first_divisible([-7, 3, 5, 14, 21], 7)
    let d2 = find_first_divisible([2, 3, 5], 7)
    assert(d1 == 14)
    assert(d2 == -1)

    let grid = [
        [1, 2, 3],
        [4, -99, 42],
        [7, 42, 9],
    ]
    // Row 1 hits -99 and skips via `next outer <-`, so row 2 col 1 finds 42!
    let pos = find_in_grid(grid, 42)
    assert(pos == "(2, 1)")

    let factors = #["x10": 10, "x2": 2]
    let ok1?: ?int = parse_and_scale("7", factors, "x10")
    let ok2?: ?int = parse_and_scale("bad", factors, "x10")
    let ok3?: ?int = parse_and_scale("7", factors, "missing")

    assert((ok1? ?? -1) == 90)
    assert(ok2? == none)
    assert(ok3? == none)

    println("control_flow_unwinds: d1=$d1, d2=$d2, pos=$pos, ok1=${ok1? ?? -1}")
}
