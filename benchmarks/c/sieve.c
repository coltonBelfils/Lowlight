// benchmarks/c/sieve.c
// Native C Prime Sieve of Eratosthenes computational benchmark.

#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

static int64_t count_primes(int64_t limit) {
    bool* is_prime = (bool*)malloc((size_t)(limit + 1) * sizeof(bool));
    if (!is_prime) return -1;
    memset(is_prime, 1, (size_t)(limit + 1) * sizeof(bool));
    is_prime[0] = false;
    is_prime[1] = false;

    for (int64_t p = 2; p * p <= limit; p++) {
        if (is_prime[p]) {
            for (int64_t mult = p * p; mult <= limit; mult += p) {
                is_prime[mult] = false;
            }
        }
    }

    int64_t count = 0;
    for (int64_t i = 2; i <= limit; i++) {
        if (is_prime[i]) {
            count++;
        }
    }
    free(is_prime);
    return count;
}

int main(void) {
    int64_t total = 0;
    for (int pass = 0; pass < 5; pass++) {
        total = count_primes(10000000);
    }
    printf("sieve(10000000) = %lld\n", (long long)total);
    return 0;
}
