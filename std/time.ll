// Lowlight Standard Library: std/time.ll
// Monotonic high-resolution timing, durations, and sleeping.

extern ll_time_monotonic_nanos: (): int
extern ll_time_sleep_ms: (ms: int)

define Duration struct [
    nanos: int,
]

bind Duration.from_nanos = (nanos: int): Duration {
    Duration[nanos: nanos]
}

bind Duration.from_micros = (micros: int): Duration {
    Duration[nanos: micros * 1000]
}

bind Duration.from_millis = (millis: float): Duration {
    Duration[nanos: int(millis * 1000000.0)]
}

bind Duration.from_secs = (secs: float): Duration {
    Duration[nanos: int(secs * 1000000000.0)]
}

bind Duration.to_nanos = (self): int {
    self.nanos
}

bind Duration.to_micros = (self): int {
    self.nanos / 1000
}

bind Duration.to_millis = (self): float {
    float(self.nanos) / 1000000.0
}

bind Duration.to_secs = (self): float {
    float(self.nanos) / 1000000000.0
}

define Instant struct [
    nanos: int,
]

bind Instant.now = (): Instant {
    Instant[nanos: unsafe ll_time_monotonic_nanos()]
}

bind Instant.elapsed = (self): Duration {
    let now = unsafe ll_time_monotonic_nanos()
    Duration[nanos: now - self.nanos]
}

bind Instant.elapsed_millis = (self): float {
    let d = self.elapsed()
    d.to_millis()
}

bind now_nanos = (): int {
    unsafe ll_time_monotonic_nanos()
}

bind now_millis = (): float {
    float(unsafe ll_time_monotonic_nanos()) / 1000000.0
}

bind sleep_ms = (ms: int) {
    unsafe ll_time_sleep_ms(ms)
}
