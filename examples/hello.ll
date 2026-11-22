// examples/hello.ll
// A quick tour of Lowlight: functions, string interpolation, ranges, and match.

define Grade enum [
    excellent,
    solid,
    needs_work,
]

bind classify_score = (score: int) Grade: {
    match (score):
    when in 90..=100: Grade.excellent
    when in 70..90:   Grade.solid
    default:       Grade.needs_work
}

bind describe = (g: Grade) string: {
    match (g):
    when excellent: "excellent"
    when solid:     "solid"
    default needs_work: "needs work"
}

bind main = (): {
    let language = "Lowlight"
    let version = 1
    println("Hello from $language v$version.0!")

    let scores = [95, 82, 64, 100]
    let total = 0
    for (i, s in scores): {
        total += s
        let grade = classify_score(s)
        println("  student #${i + 1}: score=$s (${describe(grade)})")
    }

    let avg = total / scores.len()
    assert(avg == 85, "expected average 85")
    println("Class average: $avg")
}
