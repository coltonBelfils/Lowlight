package codegen_test

import (
	"os/exec"
	"strings"
	"testing"

	"lowlight/internal/codegen"
	"lowlight/internal/ir"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

func TestEmitLLVMExternHeaders(t *testing.T) {
	prog := &ir.Program{
		Checked:       &sema.CheckedProgram{},
		ExternHeaders: []string{"math.h", `./local"quoted\hdr.h`},
	}
	ll := codegen.EmitLLVM(prog)

	for _, want := range []string{
		"; User Extern Headers",
		"; extern <math.h>",
		`; extern "./local\"quoted\\hdr.h"`,
		"!lowlight.extern_headers = !{!0, !1}",
		`!0 = !{!"math.h"}`,
		`!1 = !{!"./local\22quoted\5Chdr.h"}`,
	} {
		if !strings.Contains(ll, want) {
			t.Fatalf("expected EmitLLVM output to contain %q\nOutput head/tail checked.", want)
		}
	}

	if _, err := exec.LookPath("clang"); err == nil {
		llMain := ll + "\ndefine i32 @main() {\n  ret i32 0\n}\n"
		cmd := exec.Command("clang", "-x", "ir", "-", "-Wno-override-module", "-fsyntax-only")
		cmd.Stdin = strings.NewReader(llMain)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("clang rejected emitted LLVM IR with extern header metadata: %v\n%s", err, string(out))
		}
	}
}

func TestLLVMRuntimeSignatureConsistency(t *testing.T) {
	ll := codegen.EmitLLVM(&ir.Program{Checked: &sema.CheckedProgram{}})

	for _, want := range []string{
		"define internal i64 @ll_syscall6(i64 %n, i64 %a1, i64 %a2, i64 %a3, i64 %a4, i64 %a5, i64 %a6)",
		"define internal void @ll_tls_set_timeout(i64 %fd, i64 %timeout_ms)",
		"call void @ll_tls_set_timeout(i64 %fd64, i64 %timeout_ms)",
		"call i64 @ll_test_monotonic_nanos()",
	} {
		if !strings.Contains(ll, want) {
			t.Fatalf("expected emitted LLVM runtime to contain %q", want)
		}
	}

	for _, forbidden := range []string{
		"@ll_sys_fopen",
		"@ll_os_mkdir",
		"@ll_time_local_offset",
		"@ll_net_shutdown",
	} {
		if strings.Contains(ll, forbidden) {
			t.Fatalf("emitted LLVM runtime still contains migrated helper %q", forbidden)
		}
	}
}

func TestCrossTargetCodegenEmission(t *testing.T) {
	opendirExt := &sema.ExternFunc{
		Name: "opendir$INODE64",
		Sig: &types.Func{
			Params: []types.FuncParam{{Name: "path", Mode: types.ModeRead, Type: &types.Ptr{Elem: types.TypeU8}}},
			Ret:    &types.Ptr{Elem: types.TypeU8},
			Extern: true,
		},
	}

	cases := []struct {
		triple        string
		llvmWant      []string
		llvmForbidden []string
		cWant         []string
		cForbidden    []string
	}{
		{
			triple: "aarch64-unknown-linux-gnu",
			llvmWant: []string{
				`target triple = "aarch64-unknown-linux-gnu"`,
				`@stdout = external global ptr`,
				`@"opendir$INODE64"`,
				`"svc #0"`,
				`"={x0},{x8},{x0},{x1},{x2},{x3},{x4},{x5}`,
			},
			llvmForbidden: []string{
				`@__stdoutp`,
				`svc #0x80`,
			},
			cWant: []string{
				`extern uint8_t* ll_cext_opendir_24_INODE64(uint8_t*) __asm__("opendir$INODE64");`,
			},
			cForbidden: []string{
				`__asm__("_opendir$INODE64")`,
			},
		},
		{
			triple: "x86_64-unknown-linux-gnu",
			llvmWant: []string{
				`target triple = "x86_64-unknown-linux-gnu"`,
				`@stdout = external global ptr`,
				`"syscall"`,
				`"={rax},{rax},{rdi},{rsi},{rdx},{r10},{r8},{r9}`,
			},
			llvmForbidden: []string{
				`33554432`,
				`@__stdoutp`,
			},
			cWant: []string{
				`__asm__("opendir$INODE64")`,
			},
			cForbidden: []string{
				`__asm__("_opendir$INODE64")`,
			},
		},
		{
			triple: "arm64-apple-darwin",
			llvmWant: []string{
				`target triple = "arm64-apple-darwin"`,
				`@__stdoutp = external global ptr`,
				`"svc #0x80\0Ab.cc 1f\0Aneg x0, x0\0A1:"`,
			},
			llvmForbidden: []string{
				`@stdout = external global ptr`,
				`"={x0},{x8},`,
			},
			cWant: []string{
				`extern uint8_t* ll_cext_opendir_24_INODE64(uint8_t*) __asm__("_opendir$INODE64");`,
			},
		},
		{
			triple: "x86_64-apple-darwin",
			llvmWant: []string{
				`target triple = "x86_64-apple-darwin"`,
				`@__stdoutp = external global ptr`,
				`declare ptr @"opendir$INODE64"(ptr)`,
				`add i64 %n, 33554432`,
			},
			llvmForbidden: []string{
				`@stdout = external global ptr`,
			},
			cWant: []string{
				`__asm__("_opendir$INODE64")`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.triple, func(t *testing.T) {
			prog := &ir.Program{
				Checked:      &sema.CheckedProgram{},
				ExternFuncs:  []*sema.ExternFunc{opendirExt},
				TargetTriple: tc.triple,
			}
			ll := codegen.EmitLLVM(prog)
			for _, w := range tc.llvmWant {
				if !strings.Contains(ll, w) {
					t.Fatalf("[%s] EmitLLVM missing %q", tc.triple, w)
				}
			}
			for _, f := range tc.llvmForbidden {
				if strings.Contains(ll, f) {
					t.Fatalf("[%s] EmitLLVM unexpectedly contained host/wrong-target %q", tc.triple, f)
				}
			}
			if _, err := exec.LookPath("clang"); err == nil {
				llMain := ll + "\ndefine i32 @main() {\n  ret i32 0\n}\n"
				cmd := exec.Command("clang", "-x", "ir", "-", "-Wno-override-module", "-fsyntax-only")
				cmd.Stdin = strings.NewReader(llMain)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("[%s] clang -fsyntax-only rejected LLVM IR: %v\n%s", tc.triple, err, string(out))
				}
			}

			csrc := codegen.EmitC(prog)
			for _, w := range tc.cWant {
				if !strings.Contains(csrc, w) {
					t.Fatalf("[%s] EmitC missing %q", tc.triple, w)
				}
			}
			for _, f := range tc.cForbidden {
				if strings.Contains(csrc, f) {
					t.Fatalf("[%s] EmitC unexpectedly contained %q", tc.triple, f)
				}
			}
		})
	}
}

func TestWindowsAndWasmCodegenClang(t *testing.T) {
	_, clangErr := exec.LookPath("clang")
	cases := []struct {
		triple        string
		llvmWant      []string
		llvmForbidden []string
		cWant         []string
	}{
		{
			triple: "x86_64-pc-windows-msvc",
			llvmWant: []string{
				`target triple = "x86_64-pc-windows-msvc"`,
				"declare ptr @__acrt_iob_func(i32)",
				"define internal void @ll_tls_set_timeout(",
			},
			llvmForbidden: []string{"@dlopen(", "@clock_gettime(", "@setsockopt("},
			cWant:         []string{"#if !defined(_WIN32) && !defined(__wasm__)"},
		},
		{
			triple: "wasm32-unknown-unknown",
			llvmWant: []string{
				`target triple = "wasm32-unknown-unknown"`,
				"declare ptr @malloc(i32)",
				"declare ptr @calloc(i32, i32)",
				"declare ptr @memmove(ptr, ptr, i32)",
				"declare i32 @snprintf(ptr, i32, ptr, ...)",
				"@ll_vec_remove__vec_u8",
			},
			llvmForbidden: []string{"@dlopen(", "@clock_gettime(", "@setsockopt(", "declare ptr @malloc(i64)", "declare i32 @snprintf(ptr, i64"},
			cWant:         []string{"#if !defined(_WIN32) && !defined(__wasm__)"},
		},
	}

	for _, tc := range cases {
		prog := &ir.Program{
			TargetTriple: tc.triple,
			Checked:      &sema.CheckedProgram{},
			Vectors:      []*types.Vector{{Elem: types.TypeU8}},
		}
		ll := codegen.EmitLLVM(prog)
		for _, want := range tc.llvmWant {
			if !strings.Contains(ll, want) {
				t.Fatalf("[%s] EmitLLVM missing %q", tc.triple, want)
			}
		}
		for _, bad := range tc.llvmForbidden {
			if strings.Contains(ll, bad) {
				t.Fatalf("[%s] EmitLLVM unexpectedly contained %q", tc.triple, bad)
			}
		}
		cSrc := codegen.EmitC(prog)
		for _, want := range tc.cWant {
			if !strings.Contains(cSrc, want) {
				t.Fatalf("[%s] EmitC missing %q", tc.triple, want)
			}
		}
		if clangErr == nil {
			llMain := ll + "\ndefine i32 @main() {\n  ret i32 0\n}\n"
			cmd := exec.Command("clang", "-x", "ir", "-", "-Wno-override-module", "-fsyntax-only")
			cmd.Stdin = strings.NewReader(llMain)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("[%s] clang rejected emitted LLVM IR: %v\n%s", tc.triple, err, string(out))
			}
		}
	}
}
