// =============================================================================
// Lowlight Core Result[T, E] Error Handling Primitive
// =============================================================================

define Result[T, E] struct [
    value?: ?T,
    err?: ?E,
    _ok_flag: bool,
]

bind Result[T, E].ok = (val: own T): Result[T, E] -> {
    Result[T, E][
        value?: own val,
        err?: none,
        _ok_flag: true,
    ]
}

bind Result[T, E].fail = (e: own E): Result[T, E] -> {
    Result[T, E][
        value?: none,
        err?: own e,
        _ok_flag: false,
    ]
}

bind Result[T, E].is_ok = (self): bool -> { self._ok_flag }

bind Result[T, E].is_err = (self): bool -> { not self._ok_flag }

bind Result[T, E].unwrap_or = (own self, fallback: own T): T -> {
    self.value? ?? own fallback
}

bind Result[T, E].unwrap = (own self): T -> {
    self.value? ?? panic("called Result.unwrap on an error")
}
