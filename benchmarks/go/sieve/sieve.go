// benchmarks/go/sieve.go
// Go Prime Sieve of Eratosthenes computational benchmark.

package main

import "fmt"

func countPrimes(limit int64) int64 {
	isPrime := make([]bool, limit+1)
	for i := range isPrime {
		isPrime[i] = true
	}
	isPrime[0] = false
	isPrime[1] = false

	for p := int64(2); p*p <= limit; p++ {
		if isPrime[p] {
			for mult := p * p; mult <= limit; mult += p {
				isPrime[mult] = false
			}
		}
	}

	var count int64
	for i := int64(2); i <= limit; i++ {
		if isPrime[i] {
			count++
		}
	}
	return count
}

func main() {
	var total int64
	for pass := 0; pass < 5; pass++ {
		total = countPrimes(10000000)
	}
	fmt.Printf("sieve(10000000) = %d\n", total)
}
