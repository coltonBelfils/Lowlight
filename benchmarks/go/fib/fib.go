// benchmarks/go/fib.go
// Go Recursive Fibonacci computational benchmark.

package main

import "fmt"

func fib(n int64) int64 {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	r := fib(42)
	fmt.Printf("fib(42) = %d\n", r)
}
