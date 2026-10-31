// benchmarks/go/matmul.go
// Go Dense matrix multiplication (400x400) computational benchmark.

package main

import "fmt"

func matmul(n int64) float64 {
	totalElems := n * n
	a := make([]float64, totalElems)
	b := make([]float64, totalElems)
	c := make([]float64, totalElems)

	for i := int64(0); i < totalElems; i++ {
		a[i] = float64(i%100) * 0.01
		b[i] = float64((i*3)%100) * 0.01
	}

	for i := int64(0); i < n; i++ {
		for k := int64(0); k < n; k++ {
			aik := a[i*n+k]
			for j := int64(0); j < n; j++ {
				c[i*n+j] += aik * b[k*n+j]
			}
		}
	}

	var sum float64
	for _, v := range c {
		sum += v
	}
	return sum
}

func main() {
	s := matmul(400)
	fmt.Printf("matmul(400) = %g\n", s)
}
