// benchmarks/lowlight/mandelbrot.ll
// Mandelbrot set computational benchmark.

bind mandelbrot_checksum = (width: int, height: int, max_iter: int): int -> {
    let sum = 0
    let w_float = float(width)
    let h_float = float(height)

    for (y in 0..height) {
        let ci = -1.25 + (float(y) * 2.5) / h_float
        for (x in 0..width) {
            let cr = -2.0 + (float(x) * 2.5) / w_float
            let zr: float = 0.0
            let zi: float = 0.0
            let iter = 0
            for (iter < max_iter) {
                let zr2 = zr * zr
                let zi2 = zi * zi
                if (zr2 + zi2 > 4.0) {
                    for <- iter
                }
                zi = 2.0 * zr * zi + ci
                zr = zr2 - zi2 + cr
                iter += 1
            }
            sum += iter
        }
    }
    sum
}

bind main = () -> {
    let s = mandelbrot_checksum(1500, 1500, 500)
    println("mandelbrot(1500, 1500, 500) = $s")
}
