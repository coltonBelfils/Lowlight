// std/math — Mathematical constants, rounding, powers, logarithms, and trigonometry.
//
// Implemented in pure Lowlight over 64-bit IEEE-754 floats (`float`) and integers (`int`).

// Ratio of a circle's circumference to its diameter (`pi`).
bind PI: float = 3.14159265358979323846

// Full turn circle constant (`2 * pi`).
bind TAU: float = 6.28318530717958647692

// Euler's number (`e`), base of natural logarithms.
bind E: float = 2.71828182845904523536

// Natural logarithm of `2`.
bind LN2: float = 0.69314718055994530942

// Natural logarithm of `10`.
bind LN10: float = 2.30258509299404568402

// Square root of `2`.
bind SQRT2: float = 1.41421356237309504880

// Square root of `3`.
bind SQRT3: float = 1.73205080756887729353

// Positive IEEE-754 infinity (`+Inf`).
bind INF: float = 1.0e308 * 10.0

// Quiet IEEE-754 Not-a-Number (`NaN`).
bind NAN: float = INF - INF

// Reports whether `x` is IEEE-754 Not-a-Number (`NaN`).
bind is_nan = (x: float) bool: not (x == x)

// Reports whether `x` is positive or negative IEEE-754 infinity (`+Inf` or `-Inf`).
bind is_inf = (x: float) bool: x == INF or x == -INF

// Returns the absolute value (`|x|`) of floating-point number `x`.
bind abs_float = (x: float) float:
	if(x < 0.0): -x else: x

// Returns the absolute value of `x` (alias for [`abs_float`]).
bind fabs = (x: float) float: abs_float(x)

// Truncates `x` toward zero, discarding any fractional digits.
bind trunc = (x: float) float: {
	if(abs_float(x) >= 4503599627370496.0): func <- x
	float(int(x))
}

// Returns the largest integer value (as [`float`]) less than or equal to `x`.
bind floor = (x: float) float: {
	let t = trunc(x)
	if(x < t): t - 1.0 else: t
}

// Returns the smallest integer value (as [`float`]) greater than or equal to `x`.
bind ceil = (x: float) float: {
	let t = trunc(x)
	if(x > t): t + 1.0 else: t
}

// Rounds `x` to the nearest integer (as [`float`]), rounding halves away from zero.
bind round = (x: float) float:
	if(x >= 0.0): floor(x + 0.5) else: ceil(x - 0.5)

// Returns the floating-point remainder of `x / y` (`x - trunc(x / y) * y`, or `0.0` if `y == 0.0`).
bind fmod = (x: float, y: float) float: {
	if(y == 0.0): func <- 0.0
	x - trunc(x / y) * y
}

// Computes the non-negative square root of `x` (`0.0` for `x <= 0.0`).
bind sqrt = (x: float) float: {
	if(x <= 0.0): func <- 0.0
	let scale = 1.0
	let m = x
	for(m >= 4.0): {
		m *= 0.25
		scale *= 2.0
	}
	for(m < 0.25): {
		m *= 4.0
		scale *= 0.5
	}
	let g = 0.5 * (1.0 + m)
	let i = 0
	for(i < 8): {
		g = 0.5 * (g + m / g)
		i += 1
	}
	g * scale
}

// Computes the real cube root (`x^(1/3)`) of `x` (preserving negative signs).
bind cbrt = (x: float) float: {
	if(x == 0.0): func <- 0.0
	let is_neg = x < 0.0
	let ax = if(is_neg): -x else: x
	let g = ax
	if(g > 1.0): g = ax / 3.0
	let i = 0
	for(i < 20): {
		g = (2.0 * g + ax / (g * g)) / 3.0
		i += 1
	}
	if(is_neg): -g else: g
}

// Returns Euclidean hypotenuse `sqrt(x * x + y * y)`.
bind hypot = (x: float, y: float) float: sqrt(x * x + y * y)

// Computes the base-[`E`] exponential function (`e^x`), returning `0.0` when `x < -709.0`.
bind exp = (x: float) float: {
	if(x == 0.0): func <- 1.0
	if(x < -709.0): func <- 0.0
	let k = int(round(x / LN2))
	let r = x - float(k) * LN2
	let term = 1.0
	let sum = 1.0
	let n = 1
	for(n <= 16): {
		term = term * r / float(n)
		sum += term
		n += 1
	}
	if(k > 0): {
		let i = 0
		for(i < k): {
			sum *= 2.0
			i += 1
		}
	} else if(k < 0): {
		let i = 0
		let nk = -k
		for(i < nk): {
			sum *= 0.5
			i += 1
		}
	}
	sum
}

bind _scale_pow2 = (v: float, k: int) float: {
	let res = v
	let rem = k
	if(rem > 0): {
		for(rem > 0): {
			let step = if(rem > 512): 512 else: rem
			let base = 2.0
			let e = step
			for(e > 0): {
				if((e & 1) != 0): res *= base
				e >>= 1
				if(e > 0): base *= base
			}
			rem -= step
		}
	} else if(rem < 0): {
		let neg_rem = -rem
		for(neg_rem > 0): {
			let step = if(neg_rem > 512): 512 else: neg_rem
			let base = 0.5
			let e = step
			for(e > 0): {
				if((e & 1) != 0): res *= base
				e >>= 1
				if(e > 0): base *= base
			}
			neg_rem -= step
		}
	}
	res
}

// Computes the base-2 exponential function (`2^x`), handling `NaN`, `±Inf`, overflow (`>= 1024.0`), and subnormal/underflow (`<= -1075.0`).
bind exp2 = (x: float) float: {
	if(is_nan(x)): func <- NAN
	if(x == 0.0): func <- 1.0
	if(x >= 1024.0): func <- INF
	if(x <= -1075.0): func <- 0.0
	let k = int(round(x))
	let f = x - float(k)
	let sum = 1.0
	if(f != 0.0): {
		let r = f * LN2
		let term = 1.0
		let n = 1
		for(n <= 16): {
			term = term * r / float(n)
			sum += term
			n += 1
		}
	}
	_scale_pow2(sum, k)
}

// Computes the natural logarithm `ln(x)` (`0.0` when `x <= 0.0`).
bind log = (x: float) float: {
	if(x <= 0.0): func <- 0.0
	let k = 0
	let m = x
	for(m >= SQRT2): {
		m *= 0.5
		k += 1
	}
	for(m < 0.70710678118654752440): {
		m *= 2.0
		k -= 1
	}
	let u = (m - 1.0) / (m + 1.0)
	let u2 = u * u
	let term = u
	let sum = u
	let n = 1
	for(n <= 15): {
		term *= u2
		sum += term / float(2 * n + 1)
		n += 1
	}
	2.0 * sum + float(k) * LN2
}

// Computes the base-10 logarithm of `x`.
bind log10 = (x: float) float: log(x) / LN10

// Computes the base-2 logarithm of `x` (`NaN` for `x < 0.0` or `NaN`, `-Inf` for `±0.0`, `+Inf` for `+Inf`).
bind log2 = (x: float) float: {
	if(is_nan(x) or x < 0.0): func <- NAN
	if(x == 0.0): func <- -INF
	if(x == INF): func <- INF
	if(x == 1.0): func <- 0.0
	let k = 0
	let m = x
	let p64 = 18446744073709551616.0
	let pm64 = 1.0 / p64
	for(m >= p64): {
		m *= pm64
		k += 64
	}
	for(m < pm64): {
		m *= p64
		k -= 64
	}
	for(m >= SQRT2): {
		m *= 0.5
		k += 1
	}
	for(m < 0.70710678118654752440): {
		m *= 2.0
		k -= 1
	}
	let u = (m - 1.0) / (m + 1.0)
	let u2 = u * u
	let term = u
	let sum = u
	let n = 1
	for(n <= 15): {
		term *= u2
		sum += term / float(2 * n + 1)
		n += 1
	}
	(2.0 * sum) / LN2 + float(k)
}

// Computes `base` raised to `exponent` (`base^exponent`), using binary exponentiation for integer exponents.
bind pow = (base: float, exponent: float) float: {
	if(exponent == 0.0): func <- 1.0
	if(base == 0.0): func <- 0.0
	if(base == 1.0): func <- 1.0
	let exp_trunc = trunc(exponent)
	if(exponent == exp_trunc and abs_float(exponent) <= 1048576.0): {
		let n = int(exp_trunc)
		let is_neg_exp = n < 0
		let p = if(is_neg_exp): -n else: n
		let acc = 1.0
		let b = base
		for(p > 0): {
			if((p & 1) == 1): acc *= b
			b *= b
			p = p >> 1
		}
		if(is_neg_exp): func <- 1.0 / acc
		func <- acc
	}
	exp(exponent * log(base))
}

bind _reduce_angle = (x: float) float: {
	let r = fmod(x, TAU)
	if(r > PI): r -= TAU
	else if(r < -PI): r += TAU
	r
}

bind _sin_poly = (r: float) float: {
	let r2 = r * r
	let term = r
	let sum = r
	let n = 1
	for(n <= 10): {
		term = -term * r2 / float(2 * n * (2 * n + 1))
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
	for(n <= 10): {
		term = -term * r2 / float((2 * n - 1) * 2 * n)
		sum += term
		n += 1
	}
	sum
}

// Computes the sine of angle `x` (in radians).
bind sin = (x: float) float: {
	let r = _reduce_angle(x)
	let half_pi = PI * 0.5
	if(r > half_pi): r = PI - r
	else if(r < -half_pi): r = -PI - r
	_sin_poly(r)
}

// Computes the cosine of angle `x` (in radians).
bind cos = (x: float) float: {
	let r = _reduce_angle(x)
	let half_pi = PI * 0.5
	if(r > half_pi): func <- -_cos_poly(PI - r)
	if(r < -half_pi): func <- -_cos_poly(-PI - r)
	_cos_poly(r)
}

// Computes the tangent (`sin(x) / cos(x)`) of angle `x` (in radians).
bind tan = (x: float) float: sin(x) / cos(x)

bind _atan_unit = (x: float) float: {
	let offset = 0.0
	let t = x
	if(t > 2.0 - SQRT3): {
		offset = PI / 6.0
		t = (SQRT3 * t - 1.0) / (SQRT3 + t)
	}
	let t2 = t * t
	let term = t
	let sum = t
	let n = 1
	for(n <= 12): {
		term = -term * t2
		sum += term / float(2 * n + 1)
		n += 1
	}
	offset + sum
}

// Computes the principal value arctangent of `x` in radians (`-PI/2..=PI/2`).
bind atan = (x: float) float: {
	if(x == 0.0): func <- 0.0
	let is_neg = x < 0.0
	let ax = if(is_neg): -x else: x
	let res = if(ax > 1.0): PI * 0.5 - _atan_unit(1.0 / ax) else: _atan_unit(ax)
	if(is_neg): -res else: res
}

// Computes the four-quadrant inverse tangent of `y / x` in radians (`-PI..=PI`), returning `0.0` at `(0, 0)`.
bind atan2 = (y: float, x: float) float: {
	if(x > 0.0): func <- atan(y / x)
	if(x < 0.0): {
		if(y >= 0.0): func <- atan(y / x) + PI
		func <- atan(y / x) - PI
	}
	if(y > 0.0): func <- PI * 0.5
	if(y < 0.0): func <- -PI * 0.5
	0.0
}

// Computes the inverse sine (`arcsin(x)`) in radians, clamping inputs outside `-1.0..=1.0`.
bind asin = (x: float) float: {
	if(x >= 1.0): func <- PI * 0.5
	if(x <= -1.0): func <- -PI * 0.5
	atan2(x, sqrt(1.0 - x * x))
}

// Computes the inverse cosine (`arccos(x)`) in radians, clamping inputs outside `-1.0..=1.0`.
bind acos = (x: float) float: {
	if(x >= 1.0): func <- 0.0
	if(x <= -1.0): func <- PI
	atan2(sqrt(1.0 - x * x), x)
}

// Converts degrees to radians.
bind deg_to_rad = (deg: float) float: deg * (PI / 180.0)

// Converts radians to degrees.
bind rad_to_deg = (rad: float) float: rad * (180.0 / PI)

// Clamps integer `val` inclusively within `[lo, hi]`.
bind clamp_int = (val: int, lo: int, hi: int) int:
	if(val < lo): lo
	else if(val > hi): hi
	else: val

// Clamps floating-point `val` inclusively within `[lo, hi]`.
bind clamp_float = (val: float, lo: float, hi: float) float:
	if(val < lo): lo
	else if(val > hi): hi
	else: val

// Linearly interpolates between `a` (`t == 0.0`) and `b` (`t == 1.0`).
bind lerp = (a: float, b: float, t: float) float: a + (b - a) * t

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

@test("math log2 and exp2 accuracy and IEEE-754 edge cases")
bind test_math_log2_exp2 = (): {
	// exp2 exact integer powers
	assert(exp2(0.0) == 1.0, "exp2(0)")
	assert(exp2(-0.0) == 1.0, "exp2(-0)")
	assert(exp2(1.0) == 2.0, "exp2(1)")
	assert(exp2(3.0) == 8.0, "exp2(3)")
	assert(exp2(10.0) == 1024.0, "exp2(10)")
	assert(exp2(-3.0) == 0.125, "exp2(-3)")
	let e1023 = exp2(1023.0)
	assert(e1023 > 8.98e307 and not is_inf(e1023), "exp2(1023) finite")
	assert(exp2(-1074.0) == 5.0e-324, "exp2(-1074) smallest subnormal")

	// exp2 fractional powers
	assert(abs_float(exp2(0.5) - SQRT2) < 1.0e-12, "exp2(0.5) == sqrt(2)")
	assert(abs_float(exp2(-0.5) - 0.7071067811865475) < 1.0e-12, "exp2(-0.5)")
	assert(abs_float(exp2(3.5) - 11.313708498984761) < 1.0e-12, "exp2(3.5)")
	let e1023_5 = exp2(1023.5)
	assert(e1023_5 > 1.27e308 and not is_inf(e1023_5), "exp2(1023.5) finite")

	// exp2 IEEE-754 edge cases
	assert(exp2(1024.0) == INF, "exp2(1024) == INF")
	assert(exp2(INF) == INF, "exp2(INF) == INF")
	assert(exp2(-1075.0) == 0.0, "exp2(-1075) == 0")
	assert(exp2(-INF) == 0.0, "exp2(-INF) == 0")
	assert(is_nan(exp2(NAN)), "exp2(NAN) is NaN")

	// log2 exact powers of 2
	assert(log2(1.0) == 0.0, "log2(1)")
	assert(log2(2.0) == 1.0, "log2(2)")
	assert(log2(8.0) == 3.0, "log2(8)")
	assert(log2(1024.0) == 10.0, "log2(1024)")
	assert(log2(0.125) == -3.0, "log2(0.125)")
	assert(log2(5.0e-324) == -1074.0, "log2(5e-324)")

	// log2 non-powers of 2 & round-trips
	assert(abs_float(log2(SQRT2) - 0.5) < 1.0e-12, "log2(sqrt(2)) == 0.5")
	assert(abs_float(log2(10.0) - 3.321928094887362) < 1.0e-12, "log2(10)")
	assert(abs_float(log2(exp2(7.25)) - 7.25) < 1.0e-12, "log2(exp2(7.25))")
	assert(abs_float(exp2(log2(42.5)) - 42.5) < 1.0e-11, "exp2(log2(42.5))")
	assert(abs_float(log2(1.7976931348623157e308) - 1024.0) < 1.0e-12, "log2(DBL_MAX)")

	// log2 IEEE-754 edge cases
	assert(log2(0.0) == -INF, "log2(0) == -INF")
	assert(log2(-0.0) == -INF, "log2(-0) == -INF")
	assert(log2(INF) == INF, "log2(INF) == INF")
	assert(is_nan(log2(-1.0)), "log2(-1) is NaN")
	assert(is_nan(log2(-INF)), "log2(-INF) is NaN")
	assert(is_nan(log2(NAN)), "log2(NAN) is NaN")
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

@test("math non-finite IEEE-754 constants and predicates: INF, NAN, is_nan, is_inf")
bind test_math_non_finite = (): {
	assert(is_inf(INF), "INF is_inf")
	assert(is_inf(-INF), "-INF is_inf")
	assert(not is_nan(INF), "INF not is_nan")
	assert(not is_nan(-INF), "-INF not is_nan")
	assert(INF > 1.0e308, "INF > 1e308")
	assert(-INF < -1.0e308, "-INF < -1e308")
	assert(INF + 1.0 == INF, "INF + 1 == INF")
	assert(-INF - 1.0 == -INF, "-INF - 1 == -INF")

	assert(is_nan(NAN), "NAN is_nan")
	assert(not is_inf(NAN), "NAN not is_inf")
	assert(not (NAN == NAN), "NAN != NAN equality")
	assert(not (NAN < 0.0) and not (NAN > 0.0), "NAN unordered")
	assert(is_nan(NAN + 1.0), "NAN + 1 stays NaN")

	assert(not is_nan(0.0) and not is_inf(0.0), "0.0 finite")
	assert(not is_nan(-0.0) and not is_inf(-0.0), "-0.0 finite")
	assert(not is_nan(PI) and not is_inf(PI), "PI finite")
	assert(not is_nan(1.0e308) and not is_inf(1.0e308), "1e308 finite")
}
