// Lowlight Standard Library: std/math.ll
// Safe mathematical constants, trigonometry, exponentials, rounding, and interpolation
// implemented in 100% pure native Lowlight.

bind PI: float = 3.14159265358979323846
bind TAU: float = 6.28318530717958647692
bind E: float = 2.71828182845904523536
bind LN2: float = 0.69314718055994530942
bind LN10: float = 2.30258509299404568402
bind SQRT2: float = 1.41421356237309504880
bind SQRT3: float = 1.73205080756887729353

bind abs_float = (x: float) float: {
    if (x < 0.0): -x else: x
}

bind fabs = (x: float) float: {
    abs_float(x)
}

bind trunc = (x: float) float: {
    if (abs_float(x) >= 4503599627370496.0): func <- x
    float(int(x))
}

bind floor = (x: float) float: {
    let t = trunc(x)
    if (x < t): t - 1.0 else: t
}

bind ceil = (x: float) float: {
    let t = trunc(x)
    if (x > t): t + 1.0 else: t
}

bind round = (x: float) float: {
    if (x >= 0.0): floor(x + 0.5) else: ceil(x - 0.5)
}

bind fmod = (x: float, y: float) float: {
    if (y == 0.0): func <- 0.0
    x - trunc(x / y) * y
}

bind sqrt = (x: float) float: {
    if (x <= 0.0): func <- 0.0
    let scale = 1.0
    let m = x
    for (m >= 4.0): {
        m *= 0.25
        scale *= 2.0
    }
    for (m < 0.25): {
        m *= 4.0
        scale *= 0.5
    }
    let g = 0.5 * (1.0 + m)
    let i = 0
    for (i < 8): {
        g = 0.5 * (g + m / g)
        i += 1
    }
    g * scale
}

bind cbrt = (x: float) float: {
    if (x == 0.0): func <- 0.0
    let is_neg = x < 0.0
    let ax = if (is_neg): -x else: x
    let g = ax
    if (g > 1.0): {
        g = ax / 3.0
    }
    let i = 0
    for (i < 20): {
        g = (2.0 * g + ax / (g * g)) / 3.0
        i += 1
    }
    if (is_neg): -g else: g
}

bind hypot = (x: float, y: float) float: {
    sqrt(x * x + y * y)
}

bind exp = (x: float) float: {
    if (x == 0.0): func <- 1.0
    if (x < -709.0): func <- 0.0
    let k = int(round(x / LN2))
    let r = x - float(k) * LN2
    let term = 1.0
    let sum = 1.0
    let n = 1
    for (n <= 16): {
        term = term * r / float(n)
        sum += term
        n += 1
    }
    if (k > 0): {
        let i = 0
        for (i < k): {
            sum *= 2.0
            i += 1
        }
    } else if (k < 0): {
        let i = 0
        let nk = -k
        for (i < nk): {
            sum *= 0.5
            i += 1
        }
    }
    sum
}

bind log = (x: float) float: {
    if (x <= 0.0): func <- 0.0
    let k = 0
    let m = x
    for (m >= SQRT2): {
        m *= 0.5
        k += 1
    }
    for (m < 0.70710678118654752440): {
        m *= 2.0
        k -= 1
    }
    let u = (m - 1.0) / (m + 1.0)
    let u2 = u * u
    let term = u
    let sum = u
    let n = 1
    for (n <= 15): {
        term *= u2
        sum += term / float(2 * n + 1)
        n += 1
    }
    2.0 * sum + float(k) * LN2
}

bind log10 = (x: float) float: {
    log(x) / LN10
}

bind pow = (base: float, exponent: float) float: {
    if (exponent == 0.0): func <- 1.0
    if (base == 0.0): func <- 0.0
    if (base == 1.0): func <- 1.0
    let exp_trunc = trunc(exponent)
    if (exponent == exp_trunc and abs_float(exponent) <= 1048576.0): {
        let n = int(exp_trunc)
        let is_neg_exp = n < 0
        let p = if (is_neg_exp): -n else: n
        let acc = 1.0
        let b = base
        for (p > 0): {
            if ((p & 1) == 1): {
                acc *= b
            }
            b *= b
            p = p >> 1
        }
        if (is_neg_exp): func <- 1.0 / acc
        func <- acc
    }
    exp(exponent * log(base))
}

bind _reduce_angle = (x: float) float: {
    let r = fmod(x, TAU)
    if (r > PI): {
        r -= TAU
    } else if (r < -PI): {
        r += TAU
    }
    r
}

bind _sin_poly = (r: float) float: {
    let r2 = r * r
    let term = r
    let sum = r
    let n = 1
    for (n <= 10): {
        term = -term * r2 / float((2 * n) * (2 * n + 1))
        sum += term
        n += 1
    }
    sum
}

bind _cos_poly = (r: float) float: {
    let r2 = r * r
    let term = 1.0
    let sum = 1.0
    let n = 1
    for (n <= 10): {
        term = -term * r2 / float((2 * n - 1) * (2 * n))
        sum += term
        n += 1
    }
    sum
}

bind sin = (x: float) float: {
    let r = _reduce_angle(x)
    let half_pi = PI * 0.5
    if (r > half_pi): {
        r = PI - r
    } else if (r < -half_pi): {
        r = -PI - r
    }
    _sin_poly(r)
}

bind cos = (x: float) float: {
    let r = _reduce_angle(x)
    let half_pi = PI * 0.5
    if (r > half_pi): {
        func <- -_cos_poly(PI - r)
    }
    if (r < -half_pi): {
        func <- -_cos_poly(-PI - r)
    }
    _cos_poly(r)
}

bind tan = (x: float) float: {
    sin(x) / cos(x)
}

bind _atan_unit = (x: float) float: {
    let offset = 0.0
    let t = x
    if (t > 2.0 - SQRT3): {
        offset = PI / 6.0
        t = (SQRT3 * t - 1.0) / (SQRT3 + t)
    }
    let t2 = t * t
    let term = t
    let sum = t
    let n = 1
    for (n <= 12): {
        term = -term * t2
        sum += term / float(2 * n + 1)
        n += 1
    }
    offset + sum
}

bind atan = (x: float) float: {
    if (x == 0.0): func <- 0.0
    let is_neg = x < 0.0
    let ax = if (is_neg): -x else: x
    let res = if (ax > 1.0): (PI * 0.5 - _atan_unit(1.0 / ax)) else: _atan_unit(ax)
    if (is_neg): -res else: res
}

bind atan2 = (y: float, x: float) float: {
    if (x > 0.0): func <- atan(y / x)
    if (x < 0.0): {
        if (y >= 0.0): func <- atan(y / x) + PI
        func <- atan(y / x) - PI
    }
    if (y > 0.0): func <- PI * 0.5
    if (y < 0.0): func <- -PI * 0.5
    0.0
}

bind asin = (x: float) float: {
    if (x >= 1.0): func <- PI * 0.5
    if (x <= -1.0): func <- -PI * 0.5
    atan2(x, sqrt(1.0 - x * x))
}

bind acos = (x: float) float: {
    if (x >= 1.0): func <- 0.0
    if (x <= -1.0): func <- PI
    atan2(sqrt(1.0 - x * x), x)
}

bind deg_to_rad = (deg: float) float: { deg * (PI / 180.0) }
bind rad_to_deg = (rad: float) float: { rad * (180.0 / PI) }

bind clamp_int = (val: int, lo: int, hi: int) int: {
    if (val < lo): lo
    else if (val > hi): hi
    else: val
}

bind clamp_float = (val: float, lo: float, hi: float) float: {
    if (val < lo): lo
    else if (val > hi): hi
    else: val
}

bind lerp = (a: float, b: float, t: float) float: { a + (b - a) * t }

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("math trigonometry, constants, and angle conversions")
bind test_math_trig_and_angles = (): {
    assert(sin(0.0) == 0.0, "sin(0)")
    assert(cos(0.0) == 1.0, "cos(0)")
    assert(tan(0.0) == 0.0, "tan(0)")

    let rad = deg_to_rad(180.0)
    assert(abs_float(rad - PI) < 0.0001, "180 deg to rad")

    let deg = rad_to_deg(PI)
    assert(abs_float(deg - 180.0) < 0.0001, "PI rad to deg")

    assert(hypot(3.0, 4.0) == 5.0, "hypot 3-4-5")
}

@test("math powers, exponentials, and logarithms")
bind test_math_powers_and_logs = (): {
    assert(pow(2.0, 3.0) == 8.0, "pow 2^3")
    assert(pow(10.0, 2.0) == 100.0, "pow 10^2")
    assert(exp(0.0) == 1.0, "exp(0)")
    assert(abs_float(log(E) - 1.0) < 0.0001, "log(e) == 1")
    assert(abs_float(log10(100.0) - 2.0) < 0.0001, "log10(100) == 2")
}

@test("math rounding, clamping, and interpolation")
bind test_math_rounding_and_interpolation = (): {
    assert(floor(3.7) == 3.0, "floor positive")
    assert(floor(-3.2) == -4.0, "floor negative")

    assert(ceil(3.2) == 4.0, "ceil positive")
    assert(ceil(-3.7) == -3.0, "ceil negative")

    assert(round(3.4) == 3.0, "round down")
    assert(round(3.6) == 4.0, "round up")

    assert(abs_float(-42.5) == 42.5, "abs_float negative")
    assert(abs_float(42.5) == 42.5, "abs_float positive")

    assert(clamp_int(15, 0, 10) == 10, "clamp_int upper")
    assert(clamp_int(-5, 0, 10) == 0, "clamp_int lower")
    assert(clamp_int(5, 0, 10) == 5, "clamp_int within")

    assert(clamp_float(15.5, 0.0, 10.0) == 10.0, "clamp_float upper")
    assert(lerp(10.0, 20.0, 0.5) == 15.0, "lerp midpoint")
    assert(lerp(0.0, 100.0, 0.25) == 25.0, "lerp quarter")
}

