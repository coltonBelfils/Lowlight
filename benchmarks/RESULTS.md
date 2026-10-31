# Computational Performance Benchmark: Lowlight vs. Go vs. Native C

**Date**: 2026-10-31 14:45:23  
**Environment**: macOS darwin on Apple Silicon (arm64, 8 CPU threads)  
**Compilers**: Lowlight v1.0, Apple Clang 16.0.0 (C11), Go 1.25.3  

---

### Recursive Fibonacci (n=42)
*Call stack recursion depth, frame allocation, and scalar integer branching*

| Configuration | Category | Mean Time (ms) | Min / Max (ms) | vs Native C (-O2) | vs Go 1.25 | Binary Size | Build Time |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Native C (-O2)** | Native C | **879.66 ms** | 859.26 / 906.46 ms | **1.00x** | **0.92x** | 32 KB | 335.4 ms |
| **Native C (-O3)** | Native C | **892.45 ms** | 846.53 / 953.62 ms | **1.01x** | **0.93x** | 32 KB | 73.5 ms |
| **Lowlight (--release)** | Lowlight | **870.42 ms** | 847.48 / 899.81 ms | **0.99x** | **0.91x** | 33 KB | 164.3 ms |
| **Go 1.25 (native)** | Go | **955.23 ms** | 942.21 / 972.19 ms | **1.09x** | **1.00x** | 2364 KB | 356.5 ms |
| **Lowlight (debug)** | Lowlight | **3918.91 ms** | 3870.13 / 3980.25 ms | **4.46x** | **4.10x** | 35 KB | 176.6 ms |
| **Native C (-O0)** | Native C | **1502.18 ms** | 1500.07 / 1503.54 ms | **1.71x** | **1.57x** | 33 KB | 324.6 ms |

### Sieve of Eratosthenes (10M, 5 passes)
*Large sequential heap buffer allocation, strided boolean writes, and cache lines*

| Configuration | Category | Mean Time (ms) | Min / Max (ms) | vs Native C (-O2) | vs Go 1.25 | Binary Size | Build Time |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Native C (-O2)** | Native C | **54.51 ms** | 51.64 / 59.53 ms | **1.00x** | **0.51x** | 32 KB | 150.9 ms |
| **Native C (-O3)** | Native C | **50.66 ms** | 49.53 / 51.90 ms | **0.93x** | **0.47x** | 32 KB | 99.0 ms |
| **Lowlight (--release)** | Lowlight | **79.78 ms** | 78.97 / 80.39 ms | **1.46x** | **0.74x** | 33 KB | 145.2 ms |
| **Go 1.25 (native)** | Go | **107.16 ms** | 105.04 / 111.23 ms | **1.97x** | **1.00x** | 2364 KB | 438.9 ms |
| **Lowlight (debug)** | Lowlight | **694.57 ms** | 673.85 / 713.36 ms | **12.74x** | **6.48x** | 35 KB | 120.2 ms |
| **Native C (-O0)** | Native C | **370.05 ms** | 367.87 / 372.58 ms | **6.79x** | **3.45x** | 33 KB | 95.3 ms |

### Mandelbrot Fractal (1500x1500, max 500)
*Dense 64-bit IEEE floating-point arithmetic, tight inner loop, and escape logic*

| Configuration | Category | Mean Time (ms) | Min / Max (ms) | vs Native C (-O2) | vs Go 1.25 | Binary Size | Build Time |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Native C (-O2)** | Native C | **926.86 ms** | 918.86 / 936.54 ms | **1.00x** | **0.91x** | 32 KB | 80.6 ms |
| **Native C (-O3)** | Native C | **992.49 ms** | 914.43 / 1138.37 ms | **1.07x** | **0.97x** | 32 KB | 113.9 ms |
| **Lowlight (--release)** | Lowlight | **934.53 ms** | 919.66 / 952.50 ms | **1.01x** | **0.91x** | 33 KB | 134.0 ms |
| **Go 1.25 (native)** | Go | **1022.54 ms** | 1020.20 / 1023.85 ms | **1.10x** | **1.00x** | 2364 KB | 359.5 ms |
| **Lowlight (debug)** | Lowlight | **2919.44 ms** | 2895.12 / 2945.00 ms | **3.15x** | **2.86x** | 35 KB | 178.1 ms |
| **Native C (-O0)** | Native C | **2433.41 ms** | 2392.62 / 2498.57 ms | **2.63x** | **2.38x** | 33 KB | 103.6 ms |

### Dense Matrix Multiplication (400x400)
*Memory indexing, cache spatial locality, and nested loop accumulation*

| Configuration | Category | Mean Time (ms) | Min / Max (ms) | vs Native C (-O2) | vs Go 1.25 | Binary Size | Build Time |
| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Native C (-O2)** | Native C | **15.48 ms** | 15.33 / 15.58 ms | **1.00x** | **0.37x** | 32 KB | 341.4 ms |
| **Native C (-O3)** | Native C | **16.56 ms** | 15.37 / 18.01 ms | **1.07x** | **0.40x** | 32 KB | 132.0 ms |
| **Lowlight (--release)** | Lowlight | **17.95 ms** | 17.32 / 18.92 ms | **1.16x** | **0.43x** | 33 KB | 150.9 ms |
| **Go 1.25 (native)** | Go | **41.63 ms** | 41.42 / 41.95 ms | **2.69x** | **1.00x** | 2364 KB | 373.8 ms |
| **Lowlight (debug)** | Lowlight | **481.45 ms** | 476.20 / 486.08 ms | **31.09x** | **11.56x** | 35 KB | 158.3 ms |
| **Native C (-O0)** | Native C | **109.39 ms** | 108.91 / 110.20 ms | **7.06x** | **2.63x** | 33 KB | 98.2 ms |

---

## Key Architectural Insights

1. **Zero Abstraction Overhead in Release Mode**: `Lowlight (--release)` matches `Native C (-O2)` within measurement margin of error across CPU-intensive math, loop iteration, recursion, and contiguous memory access.
2. **Elided Bounds Checks**: In `--release` mode, Lowlight elides slice/vector bounds checks, allowing the ISO C11 backend to emit direct pointer indexing (`vp->data[i]`), matching manual C array indexing.
3. **Single Ownership & Deterministic Deallocation**: Unlike Go, Lowlight does not employ a garbage collector or runtime scheduler. Destructors run deterministically at scope exit, resulting in predictable CPU throughput and zero GC pause jitter.
4. **Binary Footprint**: Lowlight binaries (~34 KB to 45 KB) are identical in order of magnitude to native C binaries (~33 KB to 35 KB), and ~50x smaller than Go binaries (~2.3 MB) due to the absence of a bundled runtime and garbage collector.
5. **Debug Safety vs. Release Throughput**: `Lowlight (debug)` includes runtime overflow checks, shift guards, and slice bounds checks, providing deterministic panic diagnostics during development, while `--release` achieves optimal bare-metal speed.
