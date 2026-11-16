// benchmarks/lowlight/sieve.ll
// Prime Sieve of Eratosthenes computational benchmark.

bind count_primes = (limit: int) int: {
    let is_prime: [bool] = []
    for (i in 0..=limit): {
        is_prime.push(true)
    }
    is_prime[0] = false
    is_prime[1] = false

    let p = 2
    for (p * p <= limit): {
        if (is_prime[p]): {
            let mult = p * p
            for (mult <= limit): {
                is_prime[mult] = false
                mult += p
            }
        }
        p += 1
    }

    let count = 0
    for (i in 2..=limit): {
        if (is_prime[i]): {
            count += 1
        }
    }
    count
}

bind main = (): {
    let total = 0
    for (pass in 0..5): {
        total = count_primes(10000000)
    }
    println("sieve(10000000) = $total")
}
