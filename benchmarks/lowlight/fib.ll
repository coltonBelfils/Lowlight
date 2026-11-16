// benchmarks/lowlight/fib.ll
// Recursive Fibonacci computational benchmark.

bind fib = (n: int) int: {
    if (n <= 1): n else: fib(n - 1) + fib(n - 2)
}

bind main = (): {
    let r = fib(42)
    println("fib(42) = $r")
}
