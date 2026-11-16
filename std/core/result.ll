// =============================================================================
// Lowlight Core Result[T] Error Handling Primitive
// =============================================================================

import "./error.ll"

@must_use
define Result[T] enum [
    Ok: T,
    Err: Error,
]

bind Result[T].ok = (val: own T) Result[T]: {
    Result[T].Ok(own val)
}

bind Result[T].fail = (e: own Error) Result[T]: {
    Result[T].Err(own e)
}

@track_caller
bind Result[T].fail_msg = (msg: string) Result[T]: {
    Result[T].Err(Error.new(msg))
}

bind Result[T].is_ok = (self) bool: {
    match (self):
        when Result.Ok(_): true
        default: false
}

bind Result[T].is_err = (self) bool: {
    match (self):
        when Result.Err(_): true
        default: false
}

bind Result[T].unwrap_or = (own self, fallback: own T) T: {
    match (own self):
        when Result.Ok(own val): val
        default Result.Err(_): own fallback
}

bind Result[T].unwrap = (own self) T: {
    match (own self):
        when Result.Ok(own val): val
        default Result.Err(err): panic("called Result.unwrap on an error:\n" + err.to_string())
}

@track_caller
bind Result[T].expect = (own self, msg: string) T: {
    match (own self):
        when Result.Ok(own val): val
        default Result.Err(err): panic("$msg (${caller_location()}):\n" + err.to_string())
}

bind Result[T].unwrap_err = (own self) Error: {
    match (own self):
        when Result.Err(own err): err
        default Result.Ok(_): panic("called Result.unwrap_err on an ok result")
}

bind Result[T].into_ok = (own self) ?T: {
    match (own self):
        when Result.Ok(own val): val
        default Result.Err(_): none
}

bind Result[T].into_err = (own self) ?Error: {
    match (own self):
        when Result.Err(own err): err
        default Result.Ok(_): none
}

bind Result[T].peek_err = (self) ?Error: {
    match (self):
        when Result.Err(err): err.copy()
        default Result.Ok(_): none
}

@track_caller
bind Result[T].wrap_err = (own self, msg: string) Result[T]: {
    match (own self):
        when Result.Ok(own val): Result[T].Ok(own val)
        default Result.Err(err): Result[T].Err(err.wrap(msg))
}

@track_caller
bind Result[T].trace_err = (own self) Result[T]: {
    match (own self):
        when Result.Ok(own val): Result[T].Ok(own val)
        default Result.Err(err): Result[T].Err(err.trace())
}
