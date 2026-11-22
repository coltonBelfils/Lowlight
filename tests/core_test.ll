// Tests for std/core.ll: interfaces, primitive .to_string(), and core utility primitives.

define Point struct [
    x: int,
    y: int,
]

bind Point.to_string = fn(self) string: {
    "(${self.x}, ${self.y})"
}

bind Point.copy = fn(self) Point: {
    Point[x: self.x, y: self.y]
}

define DroppableResource struct [
    id: int,
]

bind DroppableResource.drop = fn(&self): {
    // cleanup hook
}

// Function accepting the Printable interface with dynamic vtable dispatch
bind print_item = fn(item: Printable) string: {
    item.to_string()
}

bind main = fn(): {
    // 1. Primitive .to_string() (provided implicitly by std/core.ll)
    assert((0).to_string() == "0")
    assert((12345).to_string() == "12345")
    assert((-12345).to_string() == "-12345")
    assert((9223372036854775807).to_string() == "9223372036854775807")
    assert((-9223372036854775807 - 1).to_string() == "-9223372036854775808")

    assert(true.to_string() == "true")
    assert(false.to_string() == "false")

    let s = "hello world"
    assert(s.to_string() == "hello world")

    let ch = 'Z'
    assert(ch.to_string() == "Z")

    let f = 3.14159
    assert(f.to_string().starts_with("3.14"))

    // Fixed-width integer .to_string()
    let byte_val: u8 = 255
    assert(byte_val.to_string() == "255")

    let u16_val: u16 = 65535
    assert(u16_val.to_string() == "65535")

    let u32_val: u32 = 4294967295
    assert(u32_val.to_string() == "4294967295")

    let u64_val: u64 = 1000000000000
    assert(u64_val.to_string() == "1000000000000")

    let i8_val: i8 = -128
    assert(i8_val.to_string() == "-128")

    let i16_val: i16 = -32768
    assert(i16_val.to_string() == "-32768")

    let i32_val: i32 = -2147483648
    assert(i32_val.to_string() == "-2147483648")

    // 2. Core math & utility primitives
    assert(min(10, 20) == 10)
    assert(min(20, 10) == 10)
    assert(max(10, 20) == 20)
    assert(max(20, 10) == 20)

    assert(min("apple", "banana") == "apple")
    assert(max("apple", "banana") == "banana")

    assert(abs(-42) == 42)
    assert(abs(42) == 42)
    assert(abs(0) == 0)

    assert(clamp(5, 10, 20) == 10)
    assert(clamp(25, 10, 20) == 20)
    assert(clamp(15, 10, 20) == 15)

    // 3. User struct implementing Printable, Copyable, Drop
    let pt = Point[x: 10, y: 20]
    assert(pt.to_string() == "(10, 20)")

    let pt_copy = pt.copy()
    assert(pt_copy.x == 10 and pt_copy.y == 20)

    let res = DroppableResource[id: 1]
    // res drops cleanly at scope end

    // 4. Dynamic interface dispatch through Printable
    assert(print_item(pt) == "(10, 20)")

    println("All std/core.ll tests passed successfully!")
}
