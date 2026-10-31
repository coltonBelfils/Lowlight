// benchmarks/c/mandelbrot.c
// Native C Mandelbrot set computational benchmark.

#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>

static int64_t mandelbrot_checksum(int64_t width, int64_t height, int64_t max_iter) {
    int64_t sum = 0;
    double w_float = (double)width;
    double h_float = (double)height;

    for (int64_t y = 0; y < height; y++) {
        double ci = -1.25 + ((double)y * 2.5) / h_float;
        for (int64_t x = 0; x < width; x++) {
            double cr = -2.0 + ((double)x * 2.5) / w_float;
            double zr = 0.0;
            double zi = 0.0;
            int64_t iter = 0;
            while (iter < max_iter) {
                double zr2 = zr * zr;
                double zi2 = zi * zi;
                if (zr2 + zi2 > 4.0) {
                    break;
                }
                zi = 2.0 * zr * zi + ci;
                zr = zr2 - zi2 + cr;
                iter++;
            }
            sum += iter;
        }
    }
    return sum;
}

int main(void) {
    int64_t s = mandelbrot_checksum(1500, 1500, 500);
    printf("mandelbrot(1500, 1500, 500) = %lld\n", (long long)s);
    return 0;
}
