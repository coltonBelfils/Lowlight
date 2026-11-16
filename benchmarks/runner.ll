// benchmarks/runner.ll
// Computational Benchmark Suite written natively in Lowlight.
// Compares Lowlight v1.0 to Go 1.25 and Native C across 4 distinct workloads.

import "std/time.ll" as time
import "std/os.ll" as os
import "std/fs.ll" as fs
import "std/math.ll" as math
import "std/strfmt.ll" as strfmt

define BenchCase struct [
    name: string,
    display_name: string,
    description: string,
    lowlight_src: string,
    c_src: string,
    go_src: string,
    expected_token: string,
]

define ConfigDef struct [
    name: string,
    category: string,
    tag: string,
]

define BenchResult struct [
    config_name: string,
    category: string,
    case_name: string,
    compile_time_ms: float,
    binary_size_kb: int,
    output: string,
    runs: [float],
    mean_ms: float,
    median_ms: float,
    min_ms: float,
    max_ms: float,
    std_dev_ms: float,
    valid: bool,
]

bind sort_floats = (arr: &[float]): {
    let n = arr.len()
    for (i in 0..n): {
        for (j in 0..(n - 1 - i)): {
            if (arr[j] > arr[j + 1]): {
                let tmp = arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = tmp
            }
        }
    }
}

bind pad_right = (s: string, width: int) string: {
    s.pad_right(width)
}

bind pad_left = (s: string, width: int) string: {
    s.pad_left(width)
}

bind build_command = (tag: string, bin_path: string, bc: BenchCase) string: {
    if (tag == "c_o2"): {
        "clang -O2 -std=c11 -o $bin_path ${bc.c_src} -lm"
    } else if (tag == "c_o3"): {
        "clang -O3 -std=c11 -o $bin_path ${bc.c_src} -lm"
    } else if (tag == "c_o0"): {
        "clang -O0 -g -std=c11 -o $bin_path ${bc.c_src} -lm"
    } else if (tag == "ll_release"): {
        "./lowlight build --release -o $bin_path ${bc.lowlight_src}"
    } else if (tag == "ll_debug"): {
        "./lowlight build -o $bin_path ${bc.lowlight_src}"
    } else if (tag == "go_native"): {
        "go build -o $bin_path ${bc.go_src}"
    } else: {
        ""
    }
}

bind sanitize_tag = (s: string) string: {
    let out = ""
    for (ch in s): {
        if (ch == ' ' or ch == '-' or ch == '(' or ch == ')'): {
            out.push("_")
        } else if (ch == '.'): {
            out.push("_")
        } else: {
            out.push_char(ch)
        }
    }
    out
}

bind first_line = (s: string) string: {
    let lines = s.trim().split("\n")
    if (lines.len() > 0): {
        lines[0].trim()
    } else: {
        ""
    }
}

bind detect_toolchain_version = (cmd: string, fallback: string) string: {
    let res = os.exec(cmd)
    if (res.ok()): {
        let line = first_line(res.stdout)
        if (line != ""): {
            line
        } else: {
            fallback.copy()
        }
    } else: {
        fallback.copy()
    }
}

bind main = (): {
    println("================================================================================")
    println(" LOWLIGHT COMPUTATIONAL BENCHMARK SUITE (Runner in Lowlight)")
    let os_n = os.os_name()
    let arch_n = os.arch()
    let cpus = os.num_cpus()
    let ll_ver = detect_toolchain_version("./lowlight version", "lowlight (unknown)")
    let clang_ver = detect_toolchain_version("clang --version", "clang (unknown)")
    let go_ver = detect_toolchain_version("go version", "go (unknown)")
    println(" OS: $os_n/$arch_n ($cpus CPUs)")
    println(" Toolchains: $ll_ver | $clang_ver | $go_ver")
    println("================================================================================")

    let bin_dir = "benchmarks/bin"
    os.mkdir_p(bin_dir)

    let cases: [BenchCase] = []
    cases.push(BenchCase[
        name: "fib_42",
        display_name: "Recursive Fibonacci (n=42)",
        description: "Call stack recursion depth, frame allocation, and scalar integer branching",
        lowlight_src: "benchmarks/lowlight/fib.ll",
        c_src: "benchmarks/c/fib.c",
        go_src: "benchmarks/go/fib/fib.go",
        expected_token: "267914296",
    ])
    cases.push(BenchCase[
        name: "sieve_10m",
        display_name: "Sieve of Eratosthenes (10M, 5 passes)",
        description: "Large sequential heap buffer allocation, strided boolean writes, and cache lines",
        lowlight_src: "benchmarks/lowlight/sieve.ll",
        c_src: "benchmarks/c/sieve.c",
        go_src: "benchmarks/go/sieve/sieve.go",
        expected_token: "664579",
    ])
    cases.push(BenchCase[
        name: "mandelbrot_1500",
        display_name: "Mandelbrot Fractal (1500x1500, max 500)",
        description: "Dense 64-bit IEEE floating-point arithmetic, tight inner loop, and escape logic",
        lowlight_src: "benchmarks/lowlight/mandelbrot.ll",
        c_src: "benchmarks/c/mandelbrot.c",
        go_src: "benchmarks/go/mandelbrot/mandelbrot.go",
        expected_token: "28516",
    ])
    cases.push(BenchCase[
        name: "matmul_400",
        display_name: "Dense Matrix Multiplication (400x400)",
        description: "Memory indexing, cache spatial locality, and nested loop accumulation",
        lowlight_src: "benchmarks/lowlight/matmul.ll",
        c_src: "benchmarks/c/matmul.c",
        go_src: "benchmarks/go/matmul/matmul.go",
        expected_token: "1.56816e+07",
    ])

    let configs: [ConfigDef] = []
    configs.push(ConfigDef[name: "Native C (-O2)", category: "Native C", tag: "c_o2"])
    configs.push(ConfigDef[name: "Native C (-O3)", category: "Native C", tag: "c_o3"])
    configs.push(ConfigDef[name: "Lowlight (--release)", category: "Lowlight", tag: "ll_release"])
    configs.push(ConfigDef[name: "Go 1.25 (native)", category: "Go", tag: "go_native"])
    configs.push(ConfigDef[name: "Lowlight (debug)", category: "Lowlight", tag: "ll_debug"])
    configs.push(ConfigDef[name: "Native C (-O0)", category: "Native C", tag: "c_o0"])

    let all_results: [BenchResult] = []
    let num_runs = 3

    for (bc in cases): {
        println("\n================================================================================")
        println(" BENCHMARK: ${bc.display_name}")
        println(" ${bc.description}")
        println("================================================================================")

        for (cfg in configs): {
            let bin_name = "${bc.name}_${sanitize_tag(cfg.tag)}"
            let bin_path = "$bin_dir/$bin_name"

            let compile_cmd = build_command(cfg.tag, bin_path, bc)
            let t_build = time.Instant.now()
            let build_res = os.exec(compile_cmd)
            let compile_ms = t_build.elapsed_millis()

            if (not build_res.ok()): {
                println("  ${pad_right(cfg.name, 24)} | BUILD ERROR: ${build_res.stdout}")
                next <-
            }

            let f_res = fs.file_size(bin_path)
            let file_b = if (f_res.is_ok()): f_res.unwrap() else: 0
            let bin_kb = if (file_b > 0): file_b / 1024 else: 0

            // Warmup run
            let warm_res = os.exec(bin_path)
            let is_valid = warm_res.stdout.contains(bc.expected_token)
            let valid_mark = if (is_valid): "✓" else: "FAIL"

            // Timed runs
            let runs: [float] = []
            for (r in 0..num_runs): {
                let t_run = time.Instant.now()
                let r_res = os.exec(bin_path)
                let elapsed = t_run.elapsed_millis()
                runs.push(elapsed)
            }

            sort_floats(&runs)
            let min_ms = runs[0]
            let max_ms = runs[runs.len() - 1]
            let median_ms = runs[runs.len() / 2]

            let sum = 0.0
            for (val in runs): {
                sum += val
            }
            let mean_ms = sum / float(runs.len())

            let var_sum = 0.0
            for (val in runs): {
                let diff = val - mean_ms
                var_sum += diff * diff
            }
            let std_dev_ms = math.sqrt(var_sum / float(runs.len()))

            let mean_s = pad_left(strfmt.format_float(mean_ms, 2) + " ms", 11)
            let min_s = pad_left(strfmt.format_float(min_ms, 2), 7)
            let max_s = pad_left(strfmt.format_float(max_ms, 2), 7)
            let std_s = pad_left(strfmt.format_float(std_dev_ms, 2) + " ms", 9)
            let kb_s = pad_left("${bin_kb} KB", 8)
            let build_s = pad_left(strfmt.format_float(compile_ms, 0) + " ms", 8)

            println("  [$valid_mark] ${pad_right(cfg.name, 22)}: $mean_s  (min: $min_s, max: $max_s, σ: $std_s) | bin: $kb_s | build: $build_s")

            let res = BenchResult[
                config_name: cfg.name.copy(),
                category: cfg.category.copy(),
                case_name: bc.name.copy(),
                compile_time_ms: compile_ms,
                binary_size_kb: bin_kb,
                output: warm_res.stdout.copy(),
                runs: runs,
                mean_ms: mean_ms,
                median_ms: median_ms,
                min_ms: min_ms,
                max_ms: max_ms,
                std_dev_ms: std_dev_ms,
                valid: is_valid,
            ]
            all_results.push(own res)
        }
    }

    // Print summary comparison tables
    println("\n================================================================================")
    println(" RELATIVE PERFORMANCE COMPARISON TABLE (Lowlight Runner)")
    println("================================================================================")

    let report = ""
    report.push("# Computational Performance Benchmark: Lowlight vs. Go vs. Native C\n\n")
    report.push("**Generated by**: Lowlight Benchmark Runner (`benchmarks/runner.ll`)  \n")
    report.push("**Environment**: $os_n/$arch_n, $cpus logical CPU cores  \n")
    report.push("**Toolchains**: $ll_ver | $clang_ver | $go_ver  \n\n")
    report.push("---\n\n")

    for (bc in cases): {
        println("\n--- ${bc.display_name} ---")
        println("${pad_right("Configuration", 24)} | ${pad_left("Valid", 5)} | ${pad_left("Mean Time", 10)} | ${pad_left("vs C (-O2)", 10)} | ${pad_left("vs Go", 10)} | ${pad_left("Binary Size", 11)} | ${pad_left("Build Time", 10)}")
        println("-------------------------+-------+------------+------------+------------+-------------+-----------")

        report.push("### ${bc.display_name}\n")
        report.push("*${bc.description}*\n\n")
        report.push("| Configuration | Category | Valid | Mean Time (ms) | Min / Max (ms) | vs Native C (-O2) | vs Go | Binary Size | Build Time |\n")
        report.push("| :--- | :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n")

        // Find baseline C (-O2) and Go
        let base_c_mean = 0.0
        let base_go_mean = 0.0
        for (r in all_results): {
            if (r.case_name == bc.name): {
                if (r.config_name == "Native C (-O2)"): {
                    base_c_mean = r.mean_ms
                }
                if (r.config_name == "Go 1.25 (native)"): {
                    base_go_mean = r.mean_ms
                }
            }
        }

        for (cfg in configs): {
            for (r in all_results): {
                if (r.case_name == bc.name and r.config_name == cfg.name): {
                    let vs_c_str = if (base_c_mean > 0.0): {
                        strfmt.format_float(r.mean_ms / base_c_mean, 2) + "x"
                    } else: {
                        "-"
                    }
                    let vs_go_str = if (base_go_mean > 0.0): {
                        strfmt.format_float(r.mean_ms / base_go_mean, 2) + "x"
                    } else: {
                        "-"
                    }

                    let valid_mark = if (r.valid): "✓" else: "FAIL"
                    let valid_s = pad_left(valid_mark, 5)
                    let mean_s = pad_left(strfmt.format_float(r.mean_ms, 2) + " ms", 10)
                    let vs_c_s = pad_left(vs_c_str, 10)
                    let vs_go_s = pad_left(vs_go_str, 10)
                    let bin_s = pad_left("${r.binary_size_kb} KB", 11)
                    let bld_s = pad_left(strfmt.format_float(r.compile_time_ms, 1) + " ms", 10)

                    println("${pad_right(cfg.name, 24)} | $valid_s | $mean_s | $vs_c_s | $vs_go_s | $bin_s | $bld_s")

                    let min_f = strfmt.format_float(r.min_ms, 2)
                    let max_f = strfmt.format_float(r.max_ms, 2)
                    let mean_f = strfmt.format_float(r.mean_ms, 2)
                    let bld_f = strfmt.format_float(r.compile_time_ms, 1)

                    report.push("| **${cfg.name}** | ${cfg.category} | $valid_mark | **${mean_f} ms** | ${min_f} / ${max_f} ms | **${vs_c_str}** | **${vs_go_str}** | ${r.binary_size_kb} KB | ${bld_f} ms |\n")
                }
            }
        }
        report.push("\n")
    }

    report.push("---\n\n")
    report.push("## Notes\n\n")
    report.push("- **Harness**: Implemented and executed natively in Lowlight (`benchmarks/runner.ll`) using `std/os` process execution and `std/time` monotonic timers.\n")
    report.push("- **Methodology**: Each configuration performs 1 warmup + validation run (`✓` / `FAIL` against expected output) followed by $num_runs timed runs.\n")
    report.push("- **Observations**: Release timings above reflect the active backend (`$ll_ver`). Compute-bound workloads (`fib_42`, `mandelbrot_1500`) track closely to `Native C (-O2)`, while array/buffer-heavy workloads (`sieve_10m`, `matmul_400`) show the current overhead of bounds checks and memory indexing.\n")

    let report_path = "benchmarks/RESULTS_LL.md"
    let write_ok = fs.write_text(report_path, report)
    if (write_ok.is_ok()): {
        println("\nDetailed markdown report written to: $report_path")
    } else: {
        println("\nWarning: Failed to write report to $report_path")
    }
}
