package codegen_test

import (
	"os/exec"
	"strings"
	"testing"

	"lowlight/internal/codegen"
	"lowlight/internal/ir"
	"lowlight/internal/sema"
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
		"declare i32 @mkdir(ptr, i32)",
		"%rc = call i32 @mkdir(ptr %path, i32 493)",
		"define internal i64 @ll_time_local_offset(i64 %epoch_sec)",
		"store i64 %epoch_sec, ptr %sec_slot, align 8",
		"define internal void @ll_net_shutdown(i64 %fd, i64 %how)",
		"%how32 = trunc i64 %how to i32",
		"call i32 @shutdown(i32 %fd32, i32 %how32)",
	} {
		if !strings.Contains(ll, want) {
			t.Fatalf("expected emitted LLVM runtime to contain %q", want)
		}
	}

	for _, forbidden := range []string{
		"call i32 @mkdir(ptr %path, i16 493)",
		"define internal i64 @ll_time_local_offset()",
		"define internal void @ll_net_shutdown(i64 %fd)",
	} {
		if strings.Contains(ll, forbidden) {
			t.Fatalf("emitted LLVM runtime still contains mismatched signature %q", forbidden)
		}
	}
}
