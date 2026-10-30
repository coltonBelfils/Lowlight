// Lowlight Standard Library: std/math.ll
// Safe mathematical constants, trigonometry, exponentials, rounding, and interpolation
// backed by ISO C11 <math.h>.

extern "math.h"

extern sin as c_sin: (x: float): float
extern cos as c_cos: (x: float): float
extern tan as c_tan: (x: float): float
extern asin as c_asin: (x: float): float
extern acos as c_acos: (x: float): float
extern atan as c_atan: (x: float): float
extern atan2 as c_atan2: (y: float, x: float): float
extern pow as c_pow: (base: float, exp: float): float
extern exp as c_exp: (x: float): float
extern log as c_log: (x: float): float
extern log10 as c_log10: (x: float): float
extern floor as c_floor: (x: float): float
extern ceil as c_ceil: (x: float): float
extern round as c_round: (x: float): float
extern fabs as c_fabs: (x: float): float
extern fmod as c_fmod: (x: float, y: float): float
extern hypot as c_hypot: (x: float, y: float): float

bind PI: float = 3.14159265358979323846
bind TAU: float = 6.28318530717958647692
bind E: float = 2.71828182845904523536

bind sin = (x: float): float unsafe { c_sin(x) }
bind cos = (x: float): float unsafe { c_cos(x) }
bind tan = (x: float): float unsafe { c_tan(x) }
bind asin = (x: float): float unsafe { c_asin(x) }
bind acos = (x: float): float unsafe { c_acos(x) }
bind atan = (x: float): float unsafe { c_atan(x) }
bind atan2 = (y: float, x: float): float unsafe { c_atan2(y, x) }

bind pow = (base: float, exponent: float): float unsafe { c_pow(base, exponent) }
bind exp = (x: float): float unsafe { c_exp(x) }
bind log = (x: float): float unsafe { c_log(x) }
bind log10 = (x: float): float unsafe { c_log10(x) }

bind floor = (x: float): float unsafe { c_floor(x) }
bind ceil = (x: float): float unsafe { c_ceil(x) }
bind round = (x: float): float unsafe { c_round(x) }
bind abs_float = (x: float): float unsafe { c_fabs(x) }
bind fmod = (x: float, y: float): float unsafe { c_fmod(x, y) }
bind hypot = (x: float, y: float): float unsafe { c_hypot(x, y) }

bind deg_to_rad = (deg: float): float { deg * (PI / 180.0) }
bind rad_to_deg = (rad: float): float { rad * (180.0 / PI) }

bind clamp_int = (val: int, lo: int, hi: int): int {
    if (val < lo) lo
    else if (val > hi) hi
    else val
}

bind clamp_float = (val: float, lo: float, hi: float): float {
    if (val < lo) lo
    else if (val > hi) hi
    else val
}

bind lerp = (a: float, b: float, t: float): float { a + (b - a) * t }
