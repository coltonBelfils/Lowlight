// examples/hello.ll
// A quick tour of Lowlight: functions, string interpolation, ranges, and match.

define Grade enum [
    Excellent,
    Solid,
    NeedsWork,
]

bind classify_score = (score: int): Grade {
    match (score)
    when 90..=100: Grade.Excellent
    when 70..90:   Grade.Solid
    else:          Grade.NeedsWork
}

bind describe = (g: Grade): string {
    match (g)
    when Grade.Excellent: "excellent"
    when Grade.Solid:     "solid"
    else Grade.NeedsWork: "needs work"
}

bind main = () {
    let language = "Lowlight"
    let version = 1
    println("Hello from $language v$version.0!")

    let scores = [95, 82, 64, 100]
    let total = 0
    for (i, s in scores) {
        total += s
        let grade = classify_score(s)
        println("  student #${i + 1}: score=$s (${describe(grade)})")
    }

    let avg = total / scores.len()
    assert(avg == 85, "expected average 85")
    println("Class average: $avg")
}
