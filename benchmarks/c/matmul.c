// benchmarks/c/matmul.c
// Native C Dense matrix multiplication (400x400) computational benchmark.

#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>

static double matmul(int64_t n) {
    int64_t total_elems = n * n;
    double* a = (double*)malloc((size_t)total_elems * sizeof(double));
    double* b = (double*)malloc((size_t)total_elems * sizeof(double));
    double* c = (double*)calloc((size_t)total_elems, sizeof(double));

    for (int64_t i = 0; i < total_elems; i++) {
        a[i] = (double)(i % 100) * 0.01;
        b[i] = (double)((i * 3) % 100) * 0.01;
    }

    for (int64_t i = 0; i < n; i++) {
        for (int64_t k = 0; k < n; k++) {
            double aik = a[i * n + k];
            for (int64_t j = 0; j < n; j++) {
                c[i * n + j] += aik * b[k * n + j];
            }
        }
    }

    double sum = 0.0;
    for (int64_t i = 0; i < total_elems; i++) {
        sum += c[i];
    }

    free(a);
    free(b);
    free(c);
    return sum;
}

int main(void) {
    double s = matmul(400);
    printf("matmul(400) = %g\n", s);
    return 0;
}
