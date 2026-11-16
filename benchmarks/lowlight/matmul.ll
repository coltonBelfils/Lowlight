// benchmarks/lowlight/matmul.ll
// Dense matrix multiplication (400x400) computational benchmark.

bind matmul = (n: int) float: {
    let a: [float] = []
    let b: [float] = []
    let c: [float] = []
    let total_elems = n * n
    for (i in 0..total_elems): {
        a.push(float(i % 100) * 0.01)
        b.push(float((i * 3) % 100) * 0.01)
        c.push(0.0)
    }

    for (i in 0..n): {
        for (k in 0..n): {
            let aik = a[i * n + k]
            for (j in 0..n): {
                c[i * n + j] = c[i * n + j] + aik * b[k * n + j]
            }
        }
    }

    let sum = 0.0
    for (x in c): {
        sum += x
    }
    sum
}

bind main = (): {
    let s = matmul(400)
    println("matmul(400) = $s")
}
