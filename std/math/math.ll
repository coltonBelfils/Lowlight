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
	if(is_nan(x) or abs_float(x) >= 4503599627370496.0): func <- x
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

// Returns the IEEE-754 floating-point remainder of `x / y` (`NaN` if `x` is `±Inf`, `y == 0.0`, or either is `NaN`; `x` if `y` is `±Inf` and `x` is finite).
bind fmod = (x: float, y: float) float: {
	if(is_nan(x) or is_nan(y) or is_inf(x) or y == 0.0): func <- NAN
	if(is_inf(y) or x == 0.0): func <- x
	let ax = abs_float(x)
	let ay = abs_float(y)
	if(ax < ay): func <- x
	if(ax == ay): {
		if(x < 0.0): func <- -0.0
		func <- 0.0
	}
	let p64 = 18446744073709551616.0
	let pm64 = 1.0 / p64
	let d = ay
	let k = 0
	for(d <= ax * pm64): {
		d *= p64
		k += 64
	}
	for(d <= ax * 0.5): {
		d *= 2.0
		k += 1
	}
	for(ax >= ay): {
		if(ax >= d): ax -= d
		if(ax >= ay): {
			for(k >= 64 and ax < d * pm64): {
				d *= pm64
				k -= 64
			}
			for(k > 0 and ax < d): {
				d *= 0.5
				k -= 1
			}
		}
	}
	if(x < 0.0): -ax else: ax
}

// Computes the non-negative square root of `x` (`NaN` for `x < 0.0` or `NaN`, `+Inf` for `+Inf`, `±0.0` preserved).
bind sqrt = (x: float) float: {
	if(is_nan(x) or x < 0.0): func <- NAN
	if(x == 0.0 or x == INF): func <- x
	let scale = 1.0
	let m = x
	let p64 = 18446744073709551616.0
	let pm64 = 1.0 / p64
	let s32 = 4294967296.0
	let sm32 = 1.0 / s32
	for(m >= p64): {
		m *= pm64
		scale *= s32
	}
	for(m < pm64): {
		m *= p64
		scale *= sm32
	}
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

bind _exp_scaled = (x: float, extra_k: int) float: {
	let ln2_hi = 0.693147180369123816490
	let ln2_lo = 1.90821492927058770002e-10
	let k = int(round(x / LN2))
	let r = (x - float(k) * ln2_hi) - float(k) * ln2_lo
	let term = 1.0
	let sum = 1.0
	let n = 1
	for(n <= 16): {
		term = term * r / float(n)
		sum += term
		n += 1
	}
	_scale_pow2(sum, k + extra_k)
}

// Computes the base-[`E`] exponential function (`e^x`), handling `NaN`, `±Inf`, overflow (`> 710.0`), and subnormal/underflow (`< -745.2`).
bind exp = (x: float) float: {
	if(is_nan(x)): func <- NAN
	if(x == 0.0): func <- 1.0
	if(x > 710.0): func <- INF
	if(x < -745.2): func <- 0.0
	_exp_scaled(x, 0)
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

// Computes the natural logarithm `ln(x)` (`NaN` for `x < 0.0` or `NaN`, `-Inf` for `±0.0`, `+Inf` for `+Inf`).
bind log = (x: float) float: {
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

bind _is_odd_int = (y: float) bool: {
	if(is_nan(y) or is_inf(y)): func <- false
	if(y != trunc(y)): func <- false
	let ay = abs_float(y)
	if(ay >= 9007199254740992.0): func <- false
	(int(ay) & 1) != 0
}

// Computes `base` raised to `exponent` (`base^exponent`), following IEEE-754 special-case rules.
bind pow = (base: float, exponent: float) float: {
	if(exponent == 0.0): func <- 1.0
	if(base == 1.0): func <- 1.0
	if(is_nan(base) or is_nan(exponent)): func <- NAN
	if(base == 0.0): {
		let is_neg_zero = (1.0 / base) < 0.0
		if(exponent < 0.0): {
			if(is_neg_zero and _is_odd_int(exponent)): func <- -INF
			func <- INF
		}
		if(is_neg_zero and _is_odd_int(exponent)): func <- -0.0
		func <- 0.0
	}
	if(base == -1.0 and is_inf(exponent)): func <- 1.0
	if(is_inf(exponent)): {
		let ab = abs_float(base)
		if(exponent == INF): {
			if(ab > 1.0): func <- INF
			func <- 0.0
		}
		if(ab > 1.0): func <- 0.0
		func <- INF
	}
	if(is_inf(base)): {
		if(base == INF): {
			if(exponent > 0.0): func <- INF
			func <- 0.0
		}
		if(exponent > 0.0): {
			if(_is_odd_int(exponent)): func <- -INF
			func <- INF
		}
		if(_is_odd_int(exponent)): func <- -0.0
		func <- 0.0
	}
	if(base < 0.0): {
		if(exponent != trunc(exponent)): func <- NAN
		if(base == -1.0): {
			if(_is_odd_int(exponent)): func <- -1.0
			func <- 1.0
		}
		let mag = pow(-base, exponent)
		if(_is_odd_int(exponent)): func <- -mag
		func <- mag
	}
	let exp_trunc = trunc(exponent)
	if(exponent == exp_trunc and abs_float(exponent) <= 1048576.0): {
		let n = int(exp_trunc)
		let is_neg_exp = n < 0
		let p = if(is_neg_exp): -n else: n
		let acc = 1.0
		let b = base
		for(p > 0): {
			if((p & 1) == 1): acc *= b
			p = p >> 1
			if(p > 0): b *= b
		}
		if(is_neg_exp): {
			if(not is_inf(acc)): func <- 1.0 / acc
		} else: {
			func <- acc
		}
	}
	exp(exponent * log(base))
}

bind _reduce_angle = (x: float) float: {
	if(is_nan(x) or is_inf(x)): func <- NAN
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

// Computes the sine of angle `x` (in radians), returning `NaN` for `NaN` and `±Inf`.
bind sin = (x: float) float: {
	if(is_nan(x) or is_inf(x)): func <- NAN
	let r = _reduce_angle(x)
	let half_pi = PI * 0.5
	if(r > half_pi): r = PI - r
	else if(r < -half_pi): r = -PI - r
	_sin_poly(r)
}

// Computes the cosine of angle `x` (in radians), returning `NaN` for `NaN` and `±Inf`.
bind cos = (x: float) float: {
	if(is_nan(x) or is_inf(x)): func <- NAN
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

// Computes the hyperbolic sine of `x`, handling `NaN`, `±Inf`, small-angle precision, and large values up to `~710.475`.
bind sinh = (x: float) float: {
	if(is_nan(x)): func <- NAN
	if(is_inf(x) or x == 0.0): func <- x
	let ax = abs_float(x)
	if(ax < 1.0): {
		let x2 = x * x
		let term = x
		let sum = x
		let n = 1
		for(n <= 10): {
			term = term * x2 / float(2 * n * (2 * n + 1))
			sum += term
			n += 1
		}
		func <- sum
	}
	if(ax <= 22.0): {
		let e = exp(ax)
		let res = 0.5 * (e - 1.0 / e)
		if(x < 0.0): func <- -res
		func <- res
	}
	if(ax <= 711.0): {
		let res = _exp_scaled(ax, -1)
		if(x < 0.0): func <- -res
		func <- res
	}
	if(x < 0.0): -INF else: INF
}

// Computes the hyperbolic cosine of `x`, handling `NaN`, `±Inf`, and large values up to `~710.475`.
bind cosh = (x: float) float: {
	if(is_nan(x)): func <- NAN
	if(is_inf(x)): func <- INF
	if(x == 0.0): func <- 1.0
	let ax = abs_float(x)
	if(ax <= 22.0): {
		let e = exp(ax)
		func <- 0.5 * (e + 1.0 / e)
	}
	if(ax <= 711.0): func <- _exp_scaled(ax, -1)
	INF
}

// Computes the hyperbolic tangent of `x` (`±1.0` for `±Inf` and `|x| >= 20.0`, `NaN` for `NaN`).
bind tanh = (x: float) float: {
	if(is_nan(x)): func <- NAN
	if(x == 0.0): func <- x
	let ax = abs_float(x)
	if(ax >= 20.0): {
		if(x < 0.0): func <- -1.0
		func <- 1.0
	}
	if(ax < 1.0): func <- sinh(x) / cosh(x)
	let e2 = exp(2.0 * ax)
	let res = (e2 - 1.0) / (e2 + 1.0)
	if(x < 0.0): -res else: res
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

@test("math hyperbolic functions: sinh, cosh, tanh")
bind test_math_hyperbolic = (): {
	// sinh
	let sh0 = sinh(0.0)
	assert(sh0 == 0.0 and 1.0 / sh0 > 0.0, "sinh(+0.0)")
	let shn0 = sinh(-0.0)
	assert(shn0 == 0.0 and 1.0 / shn0 < 0.0, "sinh(-0.0)")
	assert(abs_float(sinh(1.0e-12) - 1.0e-12) < 1.0e-22, "sinh(1e-12)")
	assert(abs_float(sinh(-1.0e-12) + 1.0e-12) < 1.0e-22, "sinh(-1e-12)")
	assert(abs_float(sinh(0.5) - 0.5210953054937474) < 1.0e-13, "sinh(0.5)")
	assert(abs_float(sinh(-0.5) + 0.5210953054937474) < 1.0e-13, "sinh(-0.5)")
	assert(abs_float(sinh(1.0) - 1.1752011936438014) < 1.0e-13, "sinh(1.0)")
	assert(abs_float(sinh(-1.0) + 1.1752011936438014) < 1.0e-13, "sinh(-1.0)")
	assert(abs_float(sinh(5.0) - 74.20321057778875) < 1.0e-11, "sinh(5.0)")
	assert(abs_float(sinh(-5.0) + 74.20321057778875) < 1.0e-11, "sinh(-5.0)")
	assert(abs_float(sinh(22.5) - 2955261031.5116453) / 2955261031.5116453 < 1.0e-12, "sinh(22.5)")
	let sh710 = sinh(710.0)
	assert(sh710 > 1.11e308 and not is_inf(sh710), "sinh(710) finite")
	let shm710 = sinh(-710.0)
	assert(shm710 < -1.11e308 and not is_inf(shm710), "sinh(-710) finite")
	assert(sinh(711.0) == INF, "sinh(711) == INF")
	assert(sinh(-711.0) == -INF, "sinh(-711) == -INF")
	assert(sinh(INF) == INF, "sinh(INF) == INF")
	assert(sinh(-INF) == -INF, "sinh(-INF) == -INF")
	assert(is_nan(sinh(NAN)), "sinh(NAN) is NaN")

	// cosh
	assert(cosh(0.0) == 1.0, "cosh(+0.0)")
	assert(cosh(-0.0) == 1.0, "cosh(-0.0)")
	assert(abs_float(cosh(1.0e-8) - 1.0) < 1.0e-15, "cosh(1e-8)")
	assert(abs_float(cosh(1.0) - 1.5430806348152437) < 1.0e-13, "cosh(1.0)")
	assert(cosh(-1.0) == cosh(1.0), "cosh even symmetry")
	assert(abs_float(cosh(5.0) - 74.20994852478785) < 1.0e-11, "cosh(5.0)")
	let ch710 = cosh(710.0)
	assert(ch710 > 1.11e308 and not is_inf(ch710), "cosh(710) finite")
	let chm710 = cosh(-710.0)
	assert(chm710 > 1.11e308 and not is_inf(chm710), "cosh(-710) finite")
	assert(cosh(711.0) == INF, "cosh(711) == INF")
	assert(cosh(-711.0) == INF, "cosh(-711) == INF")
	assert(cosh(INF) == INF, "cosh(INF) == INF")
	assert(cosh(-INF) == INF, "cosh(-INF) == INF")
	assert(is_nan(cosh(NAN)), "cosh(NAN) is NaN")

	// cosh^2(x) - sinh^2(x) == 1
	assert(abs_float(cosh(2.5) * cosh(2.5) - sinh(2.5) * sinh(2.5) - 1.0) < 1.0e-11, "cosh^2 - sinh^2 == 1")

	// tanh
	let th0 = tanh(0.0)
	assert(th0 == 0.0 and 1.0 / th0 > 0.0, "tanh(+0.0)")
	let thn0 = tanh(-0.0)
	assert(thn0 == 0.0 and 1.0 / thn0 < 0.0, "tanh(-0.0)")
	assert(abs_float(tanh(1.0e-12) - 1.0e-12) < 1.0e-22, "tanh(1e-12)")
	assert(abs_float(tanh(0.5) - 0.46211715726000974) < 1.0e-13, "tanh(0.5)")
	assert(abs_float(tanh(-0.5) + 0.46211715726000974) < 1.0e-13, "tanh(-0.5)")
	assert(abs_float(tanh(1.0) - 0.7615941559557649) < 1.0e-13, "tanh(1.0)")
	assert(abs_float(tanh(-1.0) + 0.7615941559557649) < 1.0e-13, "tanh(-1.0)")
	assert(abs_float(tanh(5.0) - 0.9999092042625951) < 1.0e-13, "tanh(5.0)")
	assert(tanh(20.0) == 1.0, "tanh(20) == 1")
	assert(tanh(-20.0) == -1.0, "tanh(-20) == -1")
	assert(tanh(1000.0) == 1.0, "tanh(1000) == 1")
	assert(tanh(-1000.0) == -1.0, "tanh(-1000) == -1")
	assert(tanh(INF) == 1.0, "tanh(INF) == 1")
	assert(tanh(-INF) == -1.0, "tanh(-INF) == -1")
	assert(is_nan(tanh(NAN)), "tanh(NAN) is NaN")
}

@test("math IEEE-754 out-of-domain and non-finite edge cases")
bind test_math_ieee754_edge_cases = (): {
	// rounding on non-finite inputs
	assert(is_nan(trunc(NAN)), "trunc(NAN)")
	assert(trunc(INF) == INF and trunc(-INF) == -INF, "trunc(±INF)")
	assert(is_nan(floor(NAN)), "floor(NAN)")
	assert(floor(INF) == INF and floor(-INF) == -INF, "floor(±INF)")
	assert(is_nan(ceil(NAN)), "ceil(NAN)")
	assert(ceil(INF) == INF and ceil(-INF) == -INF, "ceil(±INF)")
	assert(is_nan(round(NAN)), "round(NAN)")
	assert(round(INF) == INF and round(-INF) == -INF, "round(±INF)")

	// fmod IEEE-754
	assert(fmod(5.5, 2.0) == 1.5, "fmod(5.5, 2.0)")
	assert(fmod(-5.5, 2.0) == -1.5, "fmod(-5.5, 2.0)")
	assert(fmod(5.5, -2.0) == 1.5, "fmod(5.5, -2.0)")
	assert(fmod(-5.5, -2.0) == -1.5, "fmod(-5.5, -2.0)")
	let fmn0 = fmod(-4.0, 2.0)
	assert(fmn0 == 0.0 and 1.0 / fmn0 < 0.0, "fmod(-4, 2) == -0.0")
	assert(fmod(5.0, INF) == 5.0, "fmod(5, INF) == 5")
	assert(fmod(-5.0, -INF) == -5.0, "fmod(-5, -INF) == -5")
	assert(is_nan(fmod(1.0, 0.0)), "fmod(1, 0) is NaN")
	assert(is_nan(fmod(0.0, 0.0)), "fmod(0, 0) is NaN")
	assert(is_nan(fmod(INF, 2.0)), "fmod(INF, 2) is NaN")
	assert(is_nan(fmod(-INF, 2.0)), "fmod(-INF, 2) is NaN")
	assert(is_nan(fmod(INF, INF)), "fmod(INF, INF) is NaN")
	assert(is_nan(fmod(NAN, 2.0)), "fmod(NAN, 2) is NaN")
	assert(is_nan(fmod(2.0, NAN)), "fmod(2, NAN) is NaN")
	let fm_big = fmod(1.0e20, 3.0)
	assert(fm_big >= 0.0 and fm_big < 3.0, "fmod large finite range")
	assert(fmod(1.5e-323, 5.0e-324) == 0.0, "fmod subnormal exact")

	// sqrt IEEE-754
	assert(sqrt(0.0) == 0.0, "sqrt(+0.0)")
	let sn0 = sqrt(-0.0)
	assert(sn0 == 0.0 and 1.0 / sn0 < 0.0, "sqrt(-0.0) preserves sign")
	assert(sqrt(INF) == INF, "sqrt(INF) == INF")
	assert(is_nan(sqrt(-1.0)), "sqrt(-1) is NaN")
	assert(is_nan(sqrt(-INF)), "sqrt(-INF) is NaN")
	assert(is_nan(sqrt(NAN)), "sqrt(NAN) is NaN")
	assert(abs_float(sqrt(5.0e-324) - 2.2227587494850775e-162) / 2.2227587494850775e-162 < 1.0e-13, "sqrt(5e-324)")
	let s_max = sqrt(1.7976931348623157e308)
	assert(not is_inf(s_max) and s_max > 1.34e154, "sqrt(DBL_MAX) finite")

	// exp IEEE-754
	assert(exp(INF) == INF, "exp(INF) == INF")
	assert(exp(710.0) == INF, "exp(710) == INF")
	assert(exp(1.0e10) == INF, "exp(1e10) == INF")
	assert(exp(-INF) == 0.0, "exp(-INF) == 0")
	assert(exp(-746.0) == 0.0, "exp(-746) == 0")
	assert(is_nan(exp(NAN)), "exp(NAN) is NaN")
	let e709 = exp(709.0)
	assert(e709 > 8.21e307 and not is_inf(e709), "exp(709) finite")
	assert(exp(-744.4400719213812) == 5.0e-324, "exp(-744.44) subnormal")

	// log & log10 IEEE-754
	assert(log(0.0) == -INF, "log(+0) == -INF")
	assert(log(-0.0) == -INF, "log(-0) == -INF")
	assert(log(INF) == INF, "log(INF) == INF")
	assert(log(1.0) == 0.0, "log(1) == 0")
	assert(is_nan(log(-1.0)), "log(-1) is NaN")
	assert(is_nan(log(-INF)), "log(-INF) is NaN")
	assert(is_nan(log(NAN)), "log(NAN) is NaN")
	assert(log10(0.0) == -INF, "log10(0) == -INF")
	assert(log10(INF) == INF, "log10(INF) == INF")
	assert(is_nan(log10(-1.0)), "log10(-1) is NaN")
	assert(abs_float(log(5.0e-324) + 744.4400719213812) / 744.4400719213812 < 1.0e-13, "log(5e-324)")
	assert(abs_float(log(1.7976931348623157e308) - 709.782712893384) / 709.782712893384 < 1.0e-13, "log(DBL_MAX)")

	// pow IEEE-754
	assert(pow(NAN, 0.0) == 1.0, "pow(NAN, +0)")
	assert(pow(NAN, -0.0) == 1.0, "pow(NAN, -0)")
	assert(pow(0.0, 0.0) == 1.0, "pow(0, 0)")
	assert(pow(INF, 0.0) == 1.0, "pow(INF, 0)")
	assert(pow(1.0, NAN) == 1.0, "pow(1, NAN)")
	assert(pow(1.0, INF) == 1.0, "pow(1, INF)")
	assert(pow(1.0, -INF) == 1.0, "pow(1, -INF)")
	assert(pow(-1.0, INF) == 1.0, "pow(-1, INF)")
	assert(pow(-1.0, -INF) == 1.0, "pow(-1, -INF)")
	assert(pow(0.0, -1.0) == INF, "pow(+0, -1)")
	assert(pow(-0.0, -1.0) == -INF, "pow(-0, -1)")
	assert(pow(-0.0, -2.0) == INF, "pow(-0, -2)")
	let pn0_3 = pow(-0.0, 3.0)
	assert(pn0_3 == 0.0 and 1.0 / pn0_3 < 0.0, "pow(-0, 3) == -0.0")
	assert(is_nan(pow(0.0, NAN)), "pow(0, NAN)")
	assert(is_nan(pow(2.0, NAN)), "pow(2, NAN)")
	assert(is_nan(pow(NAN, 2.0)), "pow(NAN, 2)")
	assert(pow(2.0, INF) == INF, "pow(2, INF)")
	assert(pow(2.0, -INF) == 0.0, "pow(2, -INF)")
	assert(pow(0.5, INF) == 0.0, "pow(0.5, INF)")
	assert(pow(0.5, -INF) == INF, "pow(0.5, -INF)")
	assert(pow(INF, 2.0) == INF, "pow(INF, 2)")
	assert(pow(INF, -2.0) == 0.0, "pow(INF, -2)")
	assert(pow(-INF, 3.0) == -INF, "pow(-INF, 3)")
	assert(pow(-INF, 2.0) == INF, "pow(-INF, 2)")
	let pinf_m3 = pow(-INF, -3.0)
	assert(pinf_m3 == 0.0 and 1.0 / pinf_m3 < 0.0, "pow(-INF, -3) == -0.0")
	assert(pow(-INF, -2.0) == 0.0, "pow(-INF, -2)")
	assert(is_nan(pow(-2.0, 0.5)), "pow(-2, 0.5) is NaN")
	assert(is_nan(pow(-2.0, -0.5)), "pow(-2, -0.5) is NaN")
	assert(pow(-2.0, 3.0) == -8.0, "pow(-2, 3)")
	assert(pow(-2.0, 2.0) == 4.0, "pow(-2, 2)")
	assert(pow(-2.0, -3.0) == -0.125, "pow(-2, -3)")
	assert(pow(2.0, -1074.0) == 5.0e-324, "pow(2, -1074) subnormal")

	// sin, cos, tan non-finite & large finite
	assert(is_nan(sin(INF)) and is_nan(sin(-INF)) and is_nan(sin(NAN)), "sin non-finite is NaN")
	assert(is_nan(cos(INF)) and is_nan(cos(-INF)) and is_nan(cos(NAN)), "cos non-finite is NaN")
	assert(is_nan(tan(INF)) and is_nan(tan(-INF)) and is_nan(tan(NAN)), "tan non-finite is NaN")
	let s_big = sin(1.0e20)
	let c_big = cos(1.0e20)
	assert(not is_nan(s_big) and not is_inf(s_big) and s_big >= -1.0 and s_big <= 1.0, "sin(1e20) bounded")
	assert(not is_nan(c_big) and not is_inf(c_big) and c_big >= -1.0 and c_big <= 1.0, "cos(1e20) bounded")
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
