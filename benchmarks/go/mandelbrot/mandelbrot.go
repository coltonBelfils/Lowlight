// benchmarks/go/mandelbrot.go
// Go Mandelbrot set computational benchmark.

package main

import "fmt"

func mandelbrotChecksum(width int64, height int64, maxIter int64) int64 {
	var sum int64
	wFloat := float64(width)
	hFloat := float64(height)

	for y := int64(0); y < height; y++ {
		ci := -1.25 + (float64(y)*2.5)/hFloat
		for x := int64(0); x < width; x++ {
			cr := -2.0 + (float64(x)*2.5)/wFloat
			zr := 0.0
			zi := 0.0
			iter := int64(0)
			for iter < maxIter {
				zr2 := zr * zr
				zi2 := zi * zi
				if zr2+zi2 > 4.0 {
					break
				}
				zi = 2.0*zr*zi + ci
				zr = zr2 - zi2 + cr
				iter++
			}
			sum += iter
		}
	}
	return sum
}

func main() {
	s := mandelbrotChecksum(1500, 1500, 500)
	fmt.Printf("mandelbrot(1500, 1500, 500) = %d\n", s)
}
