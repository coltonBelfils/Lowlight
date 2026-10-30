// examples/interfaces_shapes.ll
// Showcases structural interfaces:
// - Zero-boilerplate structural satisfaction (no `implements` keyword needed)
// - Zero-allocation stack-borrowed interface parameters `(s: Shape)`
// - Owned boxed heterogeneous collections `[Shape]` via `Shape(val)`
// - Mutable `&self` interface method dispatch (`s.scale(factor)`)

import "std/math.ll"

define Shape interface [
    name: (self): string,
    area: (self): float,
    scale: (&self, factor: float),
]

define Circle struct [
    label: string,
    radius: float,
]

bind Circle.name = (self): string { self.label.copy() }
bind Circle.area = (self): float { PI * self.radius * self.radius }
bind Circle.scale = (&self, factor: float) { self.radius *= factor }

define Rect struct [
    w: float,
    h: float,
]

bind Rect.name = (self): string { "rect(${self.w}x${self.h})" }
bind Rect.area = (self): float { self.w * self.h }
bind Rect.scale = (&self, factor: float) {
    self.w *= factor
    self.h *= factor
}

// Zero-allocation read-only fat-pointer borrow!
bind describe_shape = (s: Shape): string {
    "${s.name()}: area=${round(s.area() * 100.0) / 100.0}"
}

bind main = () {
    let c = Circle[label: "unit-disc", radius: 1.0]
    let r = Rect[w: 3.0, h: 4.0]

    // Borrowed dispatch: zero heap boxing for `r` or `c`!
    println("Before boxing -> ${describe_shape(c)} | ${describe_shape(r)}")

    // Owned heterogeneous `[Shape]` vector!
    let shapes: [Shape] = [
        Shape(own c),
        Shape(r),
    ]

    for (&s in shapes) {
        s.scale(2.0)
    }

    let total_area = 0.0
    for (s in shapes) {
        println("  scaled -> ${describe_shape(s)}")
        total_area += s.area()
    }

    // Scaled circle radius=2 -> 4*PI (~12.566), scaled rect 6x8 -> 48 => ~60.566
    assert(total_area > 60.5 and total_area < 60.6)
    println("Total scaled area (rounded): ${round(total_area)}")
}
