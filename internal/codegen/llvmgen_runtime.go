package codegen

import (
	"fmt"
	"runtime"
	"strings"

	"lowlight/internal/ir"
	"lowlight/internal/sema"
	"lowlight/internal/types"
)

// CanEqType reports whether values of type t support equality comparison.
func CanEqType(prog *ir.Program, t types.Type) bool {
	return canEqVisited(prog, t, make(map[types.Type]bool))
}

func canEqVisited(prog *ir.Program, t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if prog.Checked != nil && (prog.Checked.LookupConcreteMethod(t, "eq") != nil || prog.Checked.LookupConcreteMethod(t, "cmp") != nil) {
		return true
	}
	if types.SupportsEq(t) {
		return true
	}
	switch v := t.(type) {
	case *types.Distinct:
		return canEqVisited(prog, v.Base, seen)
	case *types.Optional:
		return canEqVisited(prog, v.Elem, seen)
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !canEqVisited(prog, vr.Payload, seen) {
				return false
			}
		}
		return true
	case *types.Struct:
		if seen[v] {
			return true
		}
		seen[v] = true
		for _, f := range v.Fields {
			if !canEqVisited(prog, f.Type, seen) {
				return false
			}
		}
		return true
	case *types.Tuple:
		for _, el := range v.Elems {
			if !canEqVisited(prog, el, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// CanCmpType reports whether values of type t support 3-way ordering comparison.
func CanCmpType(prog *ir.Program, t types.Type) bool {
	if t == nil {
		return false
	}
	if prog.Checked != nil && prog.Checked.LookupConcreteMethod(t, "cmp") != nil {
		return true
	}
	if p, isPrim := t.(*types.Primitive); isPrim {
		return p.IsNumeric() || p.Kind == types.PrimBool || p.Kind == types.PrimChar || p.Kind == types.PrimString
	}
	switch v := t.(type) {
	case *types.Distinct:
		return CanCmpType(prog, v.Base)
	case *types.Enum:
		return !v.IsTagged
	case *types.Tuple:
		if len(v.Elems) == 0 {
			return false
		}
		for _, el := range v.Elems {
			if !CanCmpType(prog, el) {
				return false
			}
		}
		return true
	}
	return false
}

// CanHashType reports whether values of type t support hashing.
func CanHashType(prog *ir.Program, t types.Type) bool {
	if t == nil {
		return false
	}
	if prog.Checked != nil && prog.Checked.LookupConcreteMethod(t, "hash") != nil {
		return true
	}
	if types.IsHashable(t) {
		return true
	}
	if d, ok := t.(*types.Distinct); ok {
		return CanHashType(prog, d.Base)
	}
	if tup, ok := t.(*types.Tuple); ok {
		for _, el := range tup.Elems {
			if !CanHashType(prog, el) {
				return false
			}
		}
		return true
	}
	return false
}

// CanToStringType reports whether values of type t support .to_string() formatting.
func CanToStringType(prog *ir.Program, t types.Type) bool {
	return canToStringVisited(prog, t, make(map[types.Type]bool))
}

func canToStringVisited(prog *ir.Program, t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if prog.Checked != nil && prog.Checked.LookupConcreteMethod(t, "to_string") != nil {
		return true
	}
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind != types.PrimVoid && v.Kind != types.PrimArena
	case *types.Ptr, *types.Ref:
		return true
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !canToStringVisited(prog, vr.Payload, seen) {
				return false
			}
		}
		return true
	case *types.Distinct:
		return canToStringVisited(prog, v.Base, seen)
	case *types.Vector:
		return canToStringVisited(prog, v.Elem, seen)
	case *types.StackArray:
		return canToStringVisited(prog, v.Elem, seen)
	case *types.Tuple:
		for _, el := range v.Elems {
			if !canToStringVisited(prog, el, seen) {
				return false
			}
		}
		return true
	case *types.Set:
		return canToStringVisited(prog, v.Elem, seen)
	case *types.Map:
		return canToStringVisited(prog, v.Key, seen) && canToStringVisited(prog, v.Value, seen)
	case *types.Optional:
		return canToStringVisited(prog, v.Elem, seen)
	case *types.Interface:
		return prog.Checked != nil && v == prog.Checked.IfacePrintable
	default:
		return false
	}
}

func (g *llvmEmitter) canCopy(t types.Type) bool {
	return CanCopyType(g.prog, t)
}

func (g *llvmEmitter) canToString(t types.Type) bool {
	return CanToStringType(g.prog, t)
}

func (g *llvmEmitter) hasVectorType(v *types.Vector) bool {
	if v == nil || g.prog == nil {
		return false
	}
	target := v.Mangled()
	for _, vec := range g.prog.Vectors {
		if vec.Mangled() == target {
			return true
		}
	}
	return false
}

func (g *llvmEmitter) canEq(t types.Type) bool {
	return g.canEqVisited(t, make(map[types.Type]bool))
}

func (g *llvmEmitter) canEqVisited(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if types.SupportsEq(t) {
		return true
	}
	if g.prog.Checked != nil {
		if g.prog.Checked.LookupConcreteMethod(t, "eq") != nil || g.prog.Checked.LookupConcreteMethod(t, "cmp") != nil {
			return true
		}
	}
	switch v := t.(type) {
	case *types.Distinct:
		return g.canEqVisited(v.Base, seen)
	case *types.Optional:
		return g.canEqVisited(v.Elem, seen)
	case *types.Struct:
		if seen[v] {
			return true
		}
		seen[v] = true
		for _, f := range v.Fields {
			if !g.canEqVisited(f.Type, seen) {
				return false
			}
		}
		return true
	case *types.Enum:
		if !v.IsTagged || seen[v] {
			return true
		}
		seen[v] = true
		for _, vr := range v.Variants {
			if vr.Kind == types.VariantTuple && vr.Payload != nil && !g.canEqVisited(vr.Payload, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// -----------------------------------------------------------------------------
// 1. Standalone LLVM IR Runtime & Libc Declarations
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitRuntimeAndDeclarations() {
	g.line("; External Libc & Intrinsics Declarations")
	isDarwin := runtime.GOOS == "darwin"

	if isDarwin {
		g.line("@__stdoutp = external global ptr")
		g.line("@__stderrp = external global ptr")
		g.declareBuiltin("__error", "declare ptr @__error()")
	} else {
		g.line("@stdout = external global ptr")
		g.line("@stderr = external global ptr")
		g.declareBuiltin("__errno_location", "declare ptr @__errno_location()")
	}

	decls := []struct{ name, sig string }{
		{"calloc", "declare ptr @calloc(i64, i64)"},
		{"realloc", "declare ptr @realloc(ptr, i64)"},
		{"free", "declare void @free(ptr)"},
		{"abort", "declare void @abort()"},
		{"exit", "declare void @exit(i32)"},
		{"getenv", "declare ptr @getenv(ptr)"},
		{"strcmp", "declare i32 @strcmp(ptr, ptr)"},
		{"strncmp", "declare i32 @strncmp(ptr, ptr, i64)"},
		{"strlen", "declare i64 @strlen(ptr)"},
		{"memcmp", "declare i32 @memcmp(ptr, ptr, i64)"},
		{"memmove", "declare ptr @memmove(ptr, ptr, i64)"},
		{"strchr", "declare ptr @strchr(ptr, i32)"},
		{"printf", "declare i32 @printf(ptr, ...)"},
		{"fprintf", "declare i32 @fprintf(ptr, ptr, ...)"},
		{"snprintf", "declare i32 @snprintf(ptr, i64, ptr, ...)"},
		{"puts", "declare i32 @puts(ptr)"},
		{"fputc", "declare i32 @fputc(i32, ptr)"},
		{"fopen", "declare ptr @fopen(ptr, ptr)"},
		{"fclose", "declare i32 @fclose(ptr)"},
		{"fread", "declare i64 @fread(ptr, i64, i64, ptr)"},
		{"fwrite", "declare i64 @fwrite(ptr, i64, i64, ptr)"},
		{"fseek", "declare i32 @fseek(ptr, i64, i32)"},
		{"ftell", "declare i64 @ftell(ptr)"},
		{"fflush", "declare i32 @fflush(ptr)"},
		{"remove", "declare i32 @remove(ptr)"},
		{"rename", "declare i32 @rename(ptr, ptr)"},
		{"popen", "declare ptr @popen(ptr, ptr)"},
		{"pclose", "declare i32 @pclose(ptr)"},
		{"system", "declare i32 @system(ptr)"},
		{"access", "declare i32 @access(ptr, i32)"},
		{"mkdir", "declare i32 @mkdir(ptr, i32)"},
		{"unlink", "declare i32 @unlink(ptr)"},
		{"rmdir", "declare i32 @rmdir(ptr)"},
		{"opendir", "declare ptr @opendir(ptr)"},
		{"readdir", "declare ptr @readdir(ptr)"},
		{"closedir", "declare i32 @closedir(ptr)"},
		{"sysconf", "declare i64 @sysconf(i32)"},
		{"clock_gettime", "declare i32 @clock_gettime(i32, ptr)"},
		{"nanosleep", "declare i32 @nanosleep(ptr, ptr)"},
		{"time", "declare i64 @time(ptr)"},
		{"localtime_r", "declare ptr @localtime_r(ptr, ptr)"},
		{"getaddrinfo", "declare i32 @getaddrinfo(ptr, ptr, ptr, ptr)"},
		{"freeaddrinfo", "declare void @freeaddrinfo(ptr)"},
		{"socket", "declare i32 @socket(i32, i32, i32)"},
		{"setsockopt", "declare i32 @setsockopt(i32, i32, i32, ptr, i32)"},
		{"getsockopt", "declare i32 @getsockopt(i32, i32, i32, ptr, ptr)"},
		{"bind", "declare i32 @bind(i32, ptr, i32)"},
		{"listen", "declare i32 @listen(i32, i32)"},
		{"getsockname", "declare i32 @getsockname(i32, ptr, ptr)"},
		{"accept", "declare i32 @accept(i32, ptr, ptr)"},
		{"connect", "declare i32 @connect(i32, ptr, i32)"},
		{"poll", "declare i32 @poll(ptr, i32, i32)"},
		{"fcntl", "declare i32 @fcntl(i32, i32, ...)"},
		{"send", "declare i64 @send(i32, ptr, i64, i32)"},
		{"recv", "declare i64 @recv(i32, ptr, i64, i32)"},
		{"close", "declare i32 @close(i32)"},
		{"shutdown", "declare i32 @shutdown(i32, i32)"},
		{"socketpair", "declare i32 @socketpair(i32, i32, i32, ptr)"},
		{"dlopen", "declare ptr @dlopen(ptr, i32)"},
		{"dlsym", "declare ptr @dlsym(ptr, ptr)"},
		{"dlclose", "declare i32 @dlclose(ptr)"},
		{"setjmp", "declare i32 @setjmp(ptr) returns_twice"},
		{"longjmp", "declare void @longjmp(ptr, i32) noreturn"},
		{"llvm.memcpy.p0.p0.i64", "declare void @llvm.memcpy.p0.p0.i64(ptr, ptr, i64, i1)"},
		{"llvm.memset.p0.i64", "declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)"},
		{"llvm.bswap.i16", "declare i16 @llvm.bswap.i16(i16)"},
	}
	for _, d := range decls {
		g.declareBuiltin(d.name, d.sig)
	}
	g.line("")

	if len(g.prog.ExternFuncs) > 0 {
		g.line("; User Declared Extern Functions")
		for _, ef := range g.prog.ExternFuncs {
			var paramTypes []string
			for _, p := range ef.Sig.Params {
				paramTypes = append(paramTypes, g.llvmType(p.Type))
			}
			retTy := g.llvmType(ef.Sig.Ret)
			g.declareBuiltin(ef.Name, fmt.Sprintf("declare %s @%s(%s)", retTy, ef.Name, strings.Join(paramTypes, ", ")))
		}
		g.line("")
	}

	// Runtime Globals
	g.line("; Lowlight Standalone LLVM IR Runtime")
	g.line("@ll_rt_live_allocs = internal global i64 0, align 8")
	g.line("@ll_rt_argc = internal global i32 0, align 4")
	g.line("@ll_rt_argv = internal global ptr null, align 8")
	g.line("@ll_test_current_jmp = internal global ptr null, align 8")
	g.line("@ll_test_last_panic = internal global [8192 x i8] zeroinitializer, align 1")
	g.line("@ll_ssl_loaded = internal global i32 -1, align 4")
	g.line("@ll_ssl_d = internal global [19 x ptr] zeroinitializer, align 8")
	g.line("")

	// Platform-specific helpers: stdout, stderr, errno
	if isDarwin {
		g.raw(`define internal ptr @ll_rt_stdout() {
entry:
    %p = load ptr, ptr @__stdoutp, align 8
    ret ptr %p
}
define internal ptr @ll_rt_stderr() {
entry:
    %p = load ptr, ptr @__stderrp, align 8
    ret ptr %p
}
define internal i32 @ll_rt_errno() {
entry:
    %p = call ptr @__error()
    %v = load i32, ptr %p, align 4
    ret i32 %v
}`)
	} else {
		g.raw(`define internal ptr @ll_rt_stdout() {
entry:
    %p = load ptr, ptr @stdout, align 8
    ret ptr %p
}
define internal ptr @ll_rt_stderr() {
entry:
    %p = load ptr, ptr @stderr, align 8
    ret ptr %p
}
define internal i32 @ll_rt_errno() {
entry:
    %p = call ptr @__errno_location()
    %v = load i32, ptr %p, align 4
    ret i32 %v
}`)
	}
	g.line("")

	emptySym := g.internString("").symbol
	unkSym := g.internString("<unknown>").symbol
	oomSym := g.internString("lowlight panic: out of memory (requested %lld bytes)\n").symbol
	oomReallocSym := g.internString("lowlight panic: out of memory in realloc (%lld bytes)\n").symbol
	leakEnvSym := g.internString("LOWLIGHT_CHECK_LEAKS").symbol
	oneSym := g.internString("1").symbol
	leakMsgSym := g.internString("LOWLIGHT LEAK CHECK FAILED: %lld live heap allocation(s) remaining at exit\n").symbol
	panicCstrJmpFmt := g.internString("%s:%d: %s").symbol
	panicCstrErrFmt := g.internString("lowlight panic at %s:%d: %s\n").symbol
	boundsJmpFmt := g.internString("%s:%lld: index %lld out of bounds for length %lld").symbol
	boundsFmt := g.internString("lowlight panic at %s:%lld: index %lld out of bounds for length %lld\n").symbol

	g.funcSet["ll_alloc"] = true
	g.funcSet["ll_realloc"] = true
	g.funcSet["ll_free"] = true
	g.funcSet["ll_copy_bytes"] = true
	g.funcSet["ll_zero_bytes"] = true
	g.funcSet["ll_check_leaks_at_exit"] = true
	g.funcSet["ll_exit"] = true
	g.funcSet["ll_panic_cstr"] = true
	g.funcSet["ll_check_bounds"] = true
	g.funcSet["ll_hash_mix64"] = true
	g.funcSet["ll_hash_combine"] = true

	g.line(`define internal ptr @ll_alloc(i64 %%size) {
entry:
    %%pos = icmp sgt i64 %%size, 0
    %%n = select i1 %%pos, i64 %%size, i64 1
    %%p = call ptr @calloc(i64 1, i64 %%n)
    %%is_null = icmp eq ptr %%p, null
    br i1 %%is_null, label %%oom, label %%ok
oom:
    %%err = call ptr @ll_rt_stderr()
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, i64 %%size)
    call void @abort()
    unreachable
ok:
    %%cur = load i64, ptr @ll_rt_live_allocs, align 8
    %%nxt = add i64 %%cur, 1
    store i64 %%nxt, ptr @ll_rt_live_allocs, align 8
    ret ptr %%p
}

define internal ptr @ll_realloc(ptr %%ptr, i64 %%size) {
entry:
    %%is_null = icmp eq ptr %%ptr, null
    br i1 %%is_null, label %%do_alloc, label %%do_realloc
do_alloc:
    %%p0 = call ptr @ll_alloc(i64 %%size)
    ret ptr %%p0
do_realloc:
    %%pos = icmp sgt i64 %%size, 0
    %%n = select i1 %%pos, i64 %%size, i64 1
    %%p = call ptr @realloc(ptr %%ptr, i64 %%n)
    %%p_null = icmp eq ptr %%p, null
    br i1 %%p_null, label %%oom, label %%ok
oom:
    %%err = call ptr @ll_rt_stderr()
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, i64 %%size)
    call void @abort()
    unreachable
ok:
    ret ptr %%p
}

define internal void @ll_free(ptr %%ptr) {
entry:
    %%is_null = icmp eq ptr %%ptr, null
    br i1 %%is_null, label %%done, label %%do_free
do_free:
    %%cur = load i64, ptr @ll_rt_live_allocs, align 8
    %%nxt = sub i64 %%cur, 1
    store i64 %%nxt, ptr @ll_rt_live_allocs, align 8
    call void @free(ptr %%ptr)
    br label %%done
done:
    ret void
}

define internal void @ll_copy_bytes(ptr %%dst, ptr %%src, i64 %%count) {
entry:
    %%c1 = icmp sgt i64 %%count, 0
    %%c2 = icmp ne ptr %%dst, null
    %%c3 = icmp ne ptr %%src, null
    %%ok1 = and i1 %%c1, %%c2
    %%ok = and i1 %%ok1, %%c3
    br i1 %%ok, label %%do_move, label %%done
do_move:
    call ptr @memmove(ptr %%dst, ptr %%src, i64 %%count)
    br label %%done
done:
    ret void
}

define internal void @ll_zero_bytes(ptr %%dst, i64 %%count) {
entry:
    %%c1 = icmp sgt i64 %%count, 0
    %%c2 = icmp ne ptr %%dst, null
    %%ok = and i1 %%c1, %%c2
    br i1 %%ok, label %%do_zero, label %%done
do_zero:
    call void @llvm.memset.p0.i64(ptr %%dst, i8 0, i64 %%count, i1 false)
    br label %%done
done:
    ret void
}

define internal void @ll_check_leaks_at_exit() {
entry:
    %%env = call ptr @getenv(ptr %s)
    %%is_null = icmp eq ptr %%env, null
    br i1 %%is_null, label %%done, label %%chk_val
chk_val:
    %%cmp = call i32 @strcmp(ptr %%env, ptr %s)
    %%is_one = icmp eq i32 %%cmp, 0
    br i1 %%is_one, label %%chk_leaks, label %%done
chk_leaks:
    %%live = load i64, ptr @ll_rt_live_allocs, align 8
    %%has_leaks = icmp ne i64 %%live, 0
    br i1 %%has_leaks, label %%fail, label %%done
fail:
    %%err = call ptr @ll_rt_stderr()
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, i64 %%live)
    call void @exit(i32 99)
    unreachable
done:
    ret void
}

define internal void @ll_exit(i64 %%code) {
entry:
    %%c32 = trunc i64 %%code to i32
    call void @exit(i32 %%c32)
    unreachable
}

define internal void @ll_panic_cstr(ptr %%msg, ptr %%file, i32 %%line) {
entry:
    %%f_null = icmp eq ptr %%file, null
    %%f = select i1 %%f_null, ptr %s, ptr %%file
    %%m_null = icmp eq ptr %%msg, null
    %%m = select i1 %%m_null, ptr %s, ptr %%msg
    %%jmp = load ptr, ptr @ll_test_current_jmp, align 8
    %%in_test = icmp ne ptr %%jmp, null
    br i1 %%in_test, label %%test_jmp, label %%fatal
test_jmp:
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr @ll_test_last_panic, i64 8192, ptr %s, ptr %%f, i32 %%line, ptr %%m)
    call void @longjmp(ptr %%jmp, i32 1)
    unreachable
fatal:
    %%err = call ptr @ll_rt_stderr()
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, ptr %%f, i32 %%line, ptr %%m)
    call void @exit(i32 1)
    unreachable
}

define internal i64 @ll_check_bounds(i64 %%idx, i64 %%len, ptr %%file, i64 %%line) {
entry:
    %%c1 = icmp slt i64 %%idx, 0
    %%c2 = icmp sge i64 %%idx, %%len
    %%oob = or i1 %%c1, %%c2
    br i1 %%oob, label %%fail, label %%ok
fail:
    %%f_null = icmp eq ptr %%file, null
    %%f = select i1 %%f_null, ptr %s, ptr %%file
    %%jmp = load ptr, ptr @ll_test_current_jmp, align 8
    %%in_test = icmp ne ptr %%jmp, null
    br i1 %%in_test, label %%test_jmp, label %%fatal
test_jmp:
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr @ll_test_last_panic, i64 8192, ptr %s, ptr %%f, i64 %%line, i64 %%idx, i64 %%len)
    call void @longjmp(ptr %%jmp, i32 1)
    unreachable
fatal:
    %%err = call ptr @ll_rt_stderr()
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, ptr %%f, i64 %%line, i64 %%idx, i64 %%len)
    call void @exit(i32 1)
    unreachable
ok:
    ret i64 %%idx
}

define internal i64 @ll_hash_mix64(i64 %%x) {
entry:
    %%s1 = lshr i64 %%x, 30
    %%x1 = xor i64 %%x, %%s1
    %%m1 = mul i64 %%x1, -4658895280553007687
    %%s2 = lshr i64 %%m1, 27
    %%x2 = xor i64 %%m1, %%s2
    %%m2 = mul i64 %%x2, -7723592293110705685
    %%s3 = lshr i64 %%m2, 31
    %%x3 = xor i64 %%m2, %%s3
    ret i64 %%x3
}

define internal i64 @ll_hash_combine(i64 %%seed, i64 %%v) {
entry:
    %%t1 = add i64 %%v, -7046029254386353131
    %%s1 = shl i64 %%seed, 6
    %%s2 = lshr i64 %%seed, 2
    %%t2 = add i64 %%t1, %%s1
    %%t3 = add i64 %%t2, %%s2
    %%res = xor i64 %%seed, %%t3
    ret i64 %%res
}
`, oomSym, oomReallocSym, leakEnvSym, oneSym, leakMsgSym, unkSym, emptySym, panicCstrJmpFmt, panicCstrErrFmt, unkSym, boundsJmpFmt, boundsFmt)

	g.emitArithmeticAndShiftRuntime()
	g.emitStringAndAssertRuntime()
	g.emitArenaRuntime()
	g.emitSyscallAndOSRuntime()
	g.emitNetAndTLSRuntime()
	g.emitTestHarnessRuntime()
}
func (g *llvmEmitter) emitArithmeticAndShiftRuntime() {
	divZeroSym := g.internString("integer division by zero").symbol
	modZeroSym := g.internString("integer modulo by zero").symbol
	udivZeroSym := g.internString("unsigned division by zero").symbol
	umodZeroSym := g.internString("unsigned modulo by zero").symbol
	shiftOobSym := g.internString("shift count out of range").symbol

	bitWidths := []struct {
		bits   int
		minVal string
	}{
		{8, "-128"},
		{16, "-32768"},
		{32, "-2147483648"},
		{64, "-9223372036854775808"},
	}

	for _, bw := range bitWidths {
		b := bw.bits
		ty := fmt.Sprintf("i%d", b)
		g.funcSet[fmt.Sprintf("ll_div_i%d", b)] = true
		g.funcSet[fmt.Sprintf("ll_mod_i%d", b)] = true
		g.funcSet[fmt.Sprintf("ll_shl_u%d", b)] = true
		g.funcSet[fmt.Sprintf("ll_shr_u%d", b)] = true
		g.funcSet[fmt.Sprintf("ll_shr_i%d", b)] = true

		g.line(`define internal %s @ll_div_i%d(%s %%a, %s %%b, ptr %%f, i32 %%l) {
entry:
    %%z = icmp eq %s %%b, 0
    br i1 %%z, label %%panic, label %%chk_ovf
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
chk_ovf:
    %%c1 = icmp eq %s %%a, %s
    %%c2 = icmp eq %s %%b, -1
    %%ovf = and i1 %%c1, %%c2
    br i1 %%ovf, label %%ret_min, label %%do_div
ret_min:
    ret %s %s
do_div:
    %%r = sdiv %s %%a, %%b
    ret %s %%r
}

define internal %s @ll_mod_i%d(%s %%a, %s %%b, ptr %%f, i32 %%l) {
entry:
    %%z = icmp eq %s %%b, 0
    br i1 %%z, label %%panic, label %%chk_ovf
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
chk_ovf:
    %%c1 = icmp eq %s %%a, %s
    %%c2 = icmp eq %s %%b, -1
    %%ovf = and i1 %%c1, %%c2
    br i1 %%ovf, label %%ret_zero, label %%do_mod
ret_zero:
    ret %s 0
do_mod:
    %%r = srem %s %%a, %%b
    ret %s %%r
}
`, ty, b, ty, ty, ty, divZeroSym, ty, bw.minVal, ty, ty, bw.minVal, ty, ty,
			ty, b, ty, ty, ty, modZeroSym, ty, bw.minVal, ty, ty, ty, ty)

		truncShift := "    %sc = trunc i64 %masked to " + ty
		if b == 64 {
			truncShift = "    %sc = add i64 %masked, 0"
		}
		g.line(`define internal %s @ll_shl_u%d(%s %%a, i64 %%cnt, i32 %%check, ptr %%f, i32 %%l) {
entry:
    %%do_chk = icmp ne i32 %%check, 0
    %%c1 = icmp slt i64 %%cnt, 0
    %%c2 = icmp sge i64 %%cnt, %d
    %%oob = or i1 %%c1, %%c2
    %%bad = and i1 %%do_chk, %%oob
    br i1 %%bad, label %%panic, label %%ok
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
ok:
    %%masked = and i64 %%cnt, %d
%s
    %%r = shl %s %%a, %%sc
    ret %s %%r
}

define internal %s @ll_shr_u%d(%s %%a, i64 %%cnt, i32 %%check, ptr %%f, i32 %%l) {
entry:
    %%do_chk = icmp ne i32 %%check, 0
    %%c1 = icmp slt i64 %%cnt, 0
    %%c2 = icmp sge i64 %%cnt, %d
    %%oob = or i1 %%c1, %%c2
    %%bad = and i1 %%do_chk, %%oob
    br i1 %%bad, label %%panic, label %%ok
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
ok:
    %%masked = and i64 %%cnt, %d
%s
    %%r = lshr %s %%a, %%sc
    ret %s %%r
}

define internal %s @ll_shr_i%d(%s %%a, i64 %%cnt, i32 %%check, ptr %%f, i32 %%l) {
entry:
    %%do_chk = icmp ne i32 %%check, 0
    %%c1 = icmp slt i64 %%cnt, 0
    %%c2 = icmp sge i64 %%cnt, %d
    %%oob = or i1 %%c1, %%c2
    %%bad = and i1 %%do_chk, %%oob
    br i1 %%bad, label %%panic, label %%ok
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
ok:
    %%masked = and i64 %%cnt, %d
%s
    %%r = ashr %s %%a, %%sc
    ret %s %%r
}
`, ty, b, ty, b, shiftOobSym, b-1, truncShift, ty, ty,
			ty, b, ty, b, shiftOobSym, b-1, truncShift, ty, ty,
			ty, b, ty, b, shiftOobSym, b-1, truncShift, ty, ty)
	}

	g.funcSet["ll_div_u64"] = true
	g.funcSet["ll_mod_u64"] = true
	g.line(`define internal i64 @ll_div_u64(i64 %%a, i64 %%b, ptr %%f, i32 %%l) {
entry:
    %%z = icmp eq i64 %%b, 0
    br i1 %%z, label %%panic, label %%ok
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
ok:
    %%r = udiv i64 %%a, %%b
    ret i64 %%r
}

define internal i64 @ll_mod_u64(i64 %%a, i64 %%b, ptr %%f, i32 %%l) {
entry:
    %%z = icmp eq i64 %%b, 0
    br i1 %%z, label %%panic, label %%ok
panic:
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l)
    unreachable
ok:
    %%r = urem i64 %%a, %%b
    ret i64 %%r
}
`, udivZeroSym, umodZeroSym)
}

func (g *llvmEmitter) emitStringAndAssertRuntime() {
	emptySym := g.internString("").symbol
	unkSym := g.internString("<unknown>").symbol
	sliceOobSym := g.internString("string slice bounds out of range").symbol
	panicColJmpFmt := g.internString("%.*s:%lld:%lld: %.*s").symbol
	panicNoColJmpFmt := g.internString("%.*s:%lld: %.*s").symbol
	panicColErrFmt := g.internString("lowlight panic at %.*s:%lld:%lld: %.*s\n").symbol
	panicNoColErrFmt := g.internString("lowlight panic at %.*s:%lld: %.*s\n").symbol
	locColFmt := g.internString("%.*s:%lld:%lld").symbol
	locNoColFmt := g.internString("%.*s:%lld").symbol

	fns := []string{
		"ll_drop__string", "ll_str_free_p", "ll_copy__string", "ll_copy_p__string",
		"ll_str_concat_p", "ll_str_ensure_owned_cap", "ll_str_push_p", "ll_str_push_byte",
		"ll_str_push_bytes", "ll_str_push_char", "ll_str_clear", "ll_str_slice_view_p",
		"ll_str_slice_owned_p", "ll_str_eq_p", "ll_str_cmp_p", "ll_str_hash_p",
		"ll_print_str_p", "ll_panic_str_p", "ll_assert_p", "ll_format_loc",
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	g.line(`define internal void @ll_drop__string(ptr %%s) {
entry:
    %%is_null = icmp eq ptr %%s, null
    br i1 %%is_null, label %%done, label %%chk
chk:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_cap = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%data = load ptr, ptr %%p_data, align 8
    %%c1 = icmp sgt i64 %%cap, 0
    %%c2 = icmp ne ptr %%data, null
    %%owned = and i1 %%c1, %%c2
    br i1 %%owned, label %%do_free, label %%zero
do_free:
    call void @ll_free(ptr %%data)
    br label %%zero
zero:
    store %%struct.ll_string zeroinitializer, ptr %%s, align 8
    br label %%done
done:
    ret void
}

define internal void @ll_str_free_p(ptr %%s) {
entry:
    call void @ll_drop__string(ptr %%s)
    ret void
}

define internal void @ll_copy_p__string(ptr %%dest, ptr %%src) {
entry:
    %%c1 = icmp eq ptr %%dest, null
    %%c2 = icmp eq ptr %%src, null
    %%bad = or i1 %%c1, %%c2
    br i1 %%bad, label %%done, label %%load_src
load_src:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%src, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%src, i32 0, i32 1
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%empty = icmp sle i64 %%len, 0
    br i1 %%empty, label %%store_empty, label %%alloc_copy
store_empty:
    %%d0 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 0
    %%d1 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 1
    %%d2 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 2
    store ptr %s, ptr %%d0, align 8
    store i64 0, ptr %%d1, align 8
    store i64 0, ptr %%d2, align 8
    br label %%done
alloc_copy:
    %%cap = add i64 %%len, 1
    %%buf = call ptr @ll_alloc(i64 %%cap)
    call void @llvm.memcpy.p0.p0.i64(ptr %%buf, ptr %%data, i64 %%len, i1 false)
    %%term = getelementptr inbounds i8, ptr %%buf, i64 %%len
    store i8 0, ptr %%term, align 1
    %%od0 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 0
    %%od1 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 1
    %%od2 = getelementptr inbounds %%struct.ll_string, ptr %%dest, i32 0, i32 2
    store ptr %%buf, ptr %%od0, align 8
    store i64 %%len, ptr %%od1, align 8
    store i64 %%cap, ptr %%od2, align 8
    br label %%done
done:
    ret void
}

define internal %%struct.ll_string @ll_copy__string(%%struct.ll_string %%s) {
entry:
    %%src = alloca %%struct.ll_string, align 8
    %%dst = alloca %%struct.ll_string, align 8
    store %%struct.ll_string %%s, ptr %%src, align 8
    call void @ll_copy_p__string(ptr %%dst, ptr %%src)
    %%r = load %%struct.ll_string, ptr %%dst, align 8
    ret %%struct.ll_string %%r
}

define internal void @ll_str_concat_p(ptr %%res, ptr %%a, ptr %%b) {
entry:
    %%pa_data = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 0
    %%pa_len = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 1
    %%pb_data = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 0
    %%pb_len = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 1
    %%da = load ptr, ptr %%pa_data, align 8
    %%la = load i64, ptr %%pa_len, align 8
    %%db = load ptr, ptr %%pb_data, align 8
    %%lb = load i64, ptr %%pb_len, align 8
    %%tot = add i64 %%la, %%lb
    %%is_zero = icmp sle i64 %%tot, 0
    br i1 %%is_zero, label %%empty, label %%alloc
empty:
    %%e0 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 0
    %%e1 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 1
    %%e2 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 2
    store ptr %s, ptr %%e0, align 8
    store i64 0, ptr %%e1, align 8
    store i64 0, ptr %%e2, align 8
    ret void
alloc:
    %%cap = add i64 %%tot, 1
    %%buf = call ptr @ll_alloc(i64 %%cap)
    %%has_a = icmp sgt i64 %%la, 0
    br i1 %%has_a, label %%copy_a, label %%chk_b
copy_a:
    call void @llvm.memcpy.p0.p0.i64(ptr %%buf, ptr %%da, i64 %%la, i1 false)
    br label %%chk_b
chk_b:
    %%has_b = icmp sgt i64 %%lb, 0
    br i1 %%has_b, label %%copy_b, label %%finish
copy_b:
    %%dst_b = getelementptr inbounds i8, ptr %%buf, i64 %%la
    call void @llvm.memcpy.p0.p0.i64(ptr %%dst_b, ptr %%db, i64 %%lb, i1 false)
    br label %%finish
finish:
    %%term = getelementptr inbounds i8, ptr %%buf, i64 %%tot
    store i8 0, ptr %%term, align 1
    %%r0 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 0
    %%r1 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 1
    %%r2 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 2
    store ptr %%buf, ptr %%r0, align 8
    store i64 %%tot, ptr %%r1, align 8
    store i64 %%cap, ptr %%r2, align 8
    ret void
}

define internal void @ll_str_ensure_owned_cap(ptr %%s, i64 %%needed) {
entry:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%p_cap = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%ok = icmp sge i64 %%cap, %%needed
    br i1 %%ok, label %%done, label %%grow
grow:
    %%has_cap = icmp sgt i64 %%cap, 0
    %%init_cap = select i1 %%has_cap, i64 %%cap, i64 16
    %%ncap_slot = alloca i64, align 8
    store i64 %%init_cap, ptr %%ncap_slot, align 8
    br label %%loop_cond
loop_cond:
    %%cur_cap = load i64, ptr %%ncap_slot, align 8
    %%more = icmp slt i64 %%cur_cap, %%needed
    br i1 %%more, label %%loop_body, label %%loop_end
loop_body:
    %%dbl = mul i64 %%cur_cap, 2
    store i64 %%dbl, ptr %%ncap_slot, align 8
    br label %%loop_cond
loop_end:
    %%new_cap = load i64, ptr %%ncap_slot, align 8
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%is_view = icmp eq i64 %%cap, 0
    br i1 %%is_view, label %%from_view, label %%from_owned
from_view:
    %%buf = call ptr @ll_alloc(i64 %%new_cap)
    %%c1 = icmp sgt i64 %%len, 0
    %%c2 = icmp ne ptr %%data, null
    %%do_cp = and i1 %%c1, %%c2
    br i1 %%do_cp, label %%cp_view, label %%fin_view
cp_view:
    call void @llvm.memcpy.p0.p0.i64(ptr %%buf, ptr %%data, i64 %%len, i1 false)
    br label %%fin_view
fin_view:
    %%term = getelementptr inbounds i8, ptr %%buf, i64 %%len
    store i8 0, ptr %%term, align 1
    store ptr %%buf, ptr %%p_data, align 8
    store i64 %%new_cap, ptr %%p_cap, align 8
    br label %%done
from_owned:
    %%nbuf = call ptr @ll_realloc(ptr %%data, i64 %%new_cap)
    store ptr %%nbuf, ptr %%p_data, align 8
    store i64 %%new_cap, ptr %%p_cap, align 8
    br label %%done
done:
    ret void
}

define internal void @ll_str_push_p(ptr %%s, ptr %%suffix) {
entry:
    %%ps_data = getelementptr inbounds %%struct.ll_string, ptr %%suffix, i32 0, i32 0
    %%ps_len = getelementptr inbounds %%struct.ll_string, ptr %%suffix, i32 0, i32 1
    %%sdata = load ptr, ptr %%ps_data, align 8
    %%slen = load i64, ptr %%ps_len, align 8
    %%empty = icmp sle i64 %%slen, 0
    br i1 %%empty, label %%done, label %%do_push
do_push:
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%new_len = add i64 %%len, %%slen
    %%needed = add i64 %%new_len, 1
    call void @ll_str_ensure_owned_cap(ptr %%s, i64 %%needed)
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%dst = getelementptr inbounds i8, ptr %%data, i64 %%len
    call ptr @memmove(ptr %%dst, ptr %%sdata, i64 %%slen)
    store i64 %%new_len, ptr %%p_len, align 8
    %%term = getelementptr inbounds i8, ptr %%data, i64 %%new_len
    store i8 0, ptr %%term, align 1
    br label %%done
done:
    ret void
}

define internal void @ll_str_push_byte(ptr %%s, i8 %%b) {
entry:
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%needed = add i64 %%len, 2
    call void @ll_str_ensure_owned_cap(ptr %%s, i64 %%needed)
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%dst = getelementptr inbounds i8, ptr %%data, i64 %%len
    store i8 %%b, ptr %%dst, align 1
    %%new_len = add i64 %%len, 1
    store i64 %%new_len, ptr %%p_len, align 8
    %%term = getelementptr inbounds i8, ptr %%data, i64 %%new_len
    store i8 0, ptr %%term, align 1
    ret void
}

define internal void @ll_str_push_bytes(ptr %%s, ptr %%bytes, i64 %%count) {
entry:
    %%c1 = icmp eq ptr %%bytes, null
    %%c2 = icmp sle i64 %%count, 0
    %%skip = or i1 %%c1, %%c2
    br i1 %%skip, label %%done, label %%do_push
do_push:
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%new_len = add i64 %%len, %%count
    %%needed = add i64 %%new_len, 1
    call void @ll_str_ensure_owned_cap(ptr %%s, i64 %%needed)
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%dst = getelementptr inbounds i8, ptr %%data, i64 %%len
    call ptr @memmove(ptr %%dst, ptr %%bytes, i64 %%count)
    store i64 %%new_len, ptr %%p_len, align 8
    %%term = getelementptr inbounds i8, ptr %%data, i64 %%new_len
    store i8 0, ptr %%term, align 1
    br label %%done
done:
    ret void
}

define internal void @ll_str_push_char(ptr %%s, i32 %%ch) {
entry:
    %%c1 = icmp ule i32 %%ch, 127
    br i1 %%c1, label %%b1, label %%chk2
b1:
    %%b1_0 = trunc i32 %%ch to i8
    call void @ll_str_push_byte(ptr %%s, i8 %%b1_0)
    ret void
chk2:
    %%c2 = icmp ule i32 %%ch, 2047
    br i1 %%c2, label %%b2, label %%chk3
b2:
    %%s2_0 = lshr i32 %%ch, 6
    %%o2_0 = or i32 %%s2_0, 192
    %%b2_0 = trunc i32 %%o2_0 to i8
    %%m2_1 = and i32 %%ch, 63
    %%o2_1 = or i32 %%m2_1, 128
    %%b2_1 = trunc i32 %%o2_1 to i8
    call void @ll_str_push_byte(ptr %%s, i8 %%b2_0)
    call void @ll_str_push_byte(ptr %%s, i8 %%b2_1)
    ret void
chk3:
    %%c3 = icmp ule i32 %%ch, 65535
    br i1 %%c3, label %%b3, label %%b4
b3:
    %%s3_0 = lshr i32 %%ch, 12
    %%o3_0 = or i32 %%s3_0, 224
    %%b3_0 = trunc i32 %%o3_0 to i8
    %%s3_1 = lshr i32 %%ch, 6
    %%m3_1 = and i32 %%s3_1, 63
    %%o3_1 = or i32 %%m3_1, 128
    %%b3_1 = trunc i32 %%o3_1 to i8
    %%m3_2 = and i32 %%ch, 63
    %%o3_2 = or i32 %%m3_2, 128
    %%b3_2 = trunc i32 %%o3_2 to i8
    call void @ll_str_push_byte(ptr %%s, i8 %%b3_0)
    call void @ll_str_push_byte(ptr %%s, i8 %%b3_1)
    call void @ll_str_push_byte(ptr %%s, i8 %%b3_2)
    ret void
b4:
    %%s4_0 = lshr i32 %%ch, 18
    %%o4_0 = or i32 %%s4_0, 240
    %%b4_0 = trunc i32 %%o4_0 to i8
    %%s4_1 = lshr i32 %%ch, 12
    %%m4_1 = and i32 %%s4_1, 63
    %%o4_1 = or i32 %%m4_1, 128
    %%b4_1 = trunc i32 %%o4_1 to i8
    %%s4_2 = lshr i32 %%ch, 6
    %%m4_2 = and i32 %%s4_2, 63
    %%o4_2 = or i32 %%m4_2, 128
    %%b4_2 = trunc i32 %%o4_2 to i8
    %%m4_3 = and i32 %%ch, 63
    %%o4_3 = or i32 %%m4_3, 128
    %%b4_3 = trunc i32 %%o4_3 to i8
    call void @ll_str_push_byte(ptr %%s, i8 %%b4_0)
    call void @ll_str_push_byte(ptr %%s, i8 %%b4_1)
    call void @ll_str_push_byte(ptr %%s, i8 %%b4_2)
    call void @ll_str_push_byte(ptr %%s, i8 %%b4_3)
    ret void
}

define internal void @ll_str_clear(ptr %%s) {
entry:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%p_cap = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%data = load ptr, ptr %%p_data, align 8
    %%c1 = icmp sgt i64 %%cap, 0
    %%c2 = icmp ne ptr %%data, null
    %%owned = and i1 %%c1, %%c2
    br i1 %%owned, label %%clr_owned, label %%clr_view
clr_owned:
    store i64 0, ptr %%p_len, align 8
    store i8 0, ptr %%data, align 1
    ret void
clr_view:
    store ptr %s, ptr %%p_data, align 8
    store i64 0, ptr %%p_len, align 8
    store i64 0, ptr %%p_cap, align 8
    ret void
}

define internal void @ll_str_slice_view_p(ptr %%res, ptr %%s, i64 %%lo, i64 %%hi, i32 %%check, ptr %%f, i64 %%l) {
entry:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%do_chk = icmp ne i32 %%check, 0
    %%c1 = icmp slt i64 %%lo, 0
    %%c2 = icmp slt i64 %%hi, %%lo
    %%c3 = icmp sgt i64 %%hi, %%len
    %%oob1 = or i1 %%c1, %%c2
    %%oob2 = or i1 %%oob1, %%c3
    %%bad = and i1 %%do_chk, %%oob2
    br i1 %%bad, label %%panic, label %%ok
panic:
    %%l32 = trunc i64 %%l to i32
    call void @ll_panic_cstr(ptr %s, ptr %%f, i32 %%l32)
    unreachable
ok:
    %%has_d = icmp ne ptr %%data, null
    %%off_d = getelementptr inbounds i8, ptr %%data, i64 %%lo
    %%res_d = select i1 %%has_d, ptr %%off_d, ptr null
    %%res_len = sub i64 %%hi, %%lo
    %%r0 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 0
    %%r1 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 1
    %%r2 = getelementptr inbounds %%struct.ll_string, ptr %%res, i32 0, i32 2
    store ptr %%res_d, ptr %%r0, align 8
    store i64 %%res_len, ptr %%r1, align 8
    store i64 0, ptr %%r2, align 8
    ret void
}

define internal void @ll_str_slice_owned_p(ptr %%res, ptr %%s, i64 %%lo, i64 %%hi, i32 %%check, ptr %%f, i64 %%l) {
entry:
    %%view = alloca %%struct.ll_string, align 8
    call void @ll_str_slice_view_p(ptr %%view, ptr %%s, i64 %%lo, i64 %%hi, i32 %%check, ptr %%f, i64 %%l)
    call void @ll_copy_p__string(ptr %%res, ptr %%view)
    ret void
}

define internal i1 @ll_str_eq_p(ptr %%a, ptr %%b) {
entry:
    %%pa_len = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 1
    %%pb_len = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 1
    %%la = load i64, ptr %%pa_len, align 8
    %%lb = load i64, ptr %%pb_len, align 8
    %%same_len = icmp eq i64 %%la, %%lb
    br i1 %%same_len, label %%chk_zero, label %%ret_false
chk_zero:
    %%is_zero = icmp sle i64 %%la, 0
    br i1 %%is_zero, label %%ret_true, label %%do_cmp
do_cmp:
    %%pa_data = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 0
    %%pb_data = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 0
    %%da = load ptr, ptr %%pa_data, align 8
    %%db = load ptr, ptr %%pb_data, align 8
    %%c = call i32 @memcmp(ptr %%da, ptr %%db, i64 %%la)
    %%eq = icmp eq i32 %%c, 0
    ret i1 %%eq
ret_true:
    ret i1 true
ret_false:
    ret i1 false
}

define internal i64 @ll_str_cmp_p(ptr %%a, ptr %%b) {
entry:
    %%pa_data = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 0
    %%pa_len = getelementptr inbounds %%struct.ll_string, ptr %%a, i32 0, i32 1
    %%pb_data = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 0
    %%pb_len = getelementptr inbounds %%struct.ll_string, ptr %%b, i32 0, i32 1
    %%da = load ptr, ptr %%pa_data, align 8
    %%la = load i64, ptr %%pa_len, align 8
    %%db = load ptr, ptr %%pb_data, align 8
    %%lb = load i64, ptr %%pb_len, align 8
    %%lt = icmp slt i64 %%la, %%lb
    %%min = select i1 %%lt, i64 %%la, i64 %%lb
    %%has_min = icmp sgt i64 %%min, 0
    br i1 %%has_min, label %%do_cmp, label %%chk_len
do_cmp:
    %%c = call i32 @memcmp(ptr %%da, ptr %%db, i64 %%min)
    %%nz = icmp ne i32 %%c, 0
    br i1 %%nz, label %%ret_cmp, label %%chk_len
ret_cmp:
    %%c_lt = icmp slt i32 %%c, 0
    %%res_c = select i1 %%c_lt, i64 -1, i64 1
    ret i64 %%res_c
chk_len:
    %%eq_len = icmp eq i64 %%la, %%lb
    %%res_l = select i1 %%lt, i64 -1, i64 1
    %%res = select i1 %%eq_len, i64 0, i64 %%res_l
    ret i64 %%res
}

define internal i64 @ll_str_hash_p(ptr %%s) {
entry:
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%h_slot = alloca i64, align 8
    %%i_slot = alloca i64, align 8
    store i64 -3750763034362895579, ptr %%h_slot, align 8
    store i64 0, ptr %%i_slot, align 8
    br label %%cond
cond:
    %%i = load i64, ptr %%i_slot, align 8
    %%more = icmp slt i64 %%i, %%len
    br i1 %%more, label %%body, label %%done
body:
    %%bp = getelementptr inbounds i8, ptr %%data, i64 %%i
    %%b8 = load i8, ptr %%bp, align 1
    %%b64 = zext i8 %%b8 to i64
    %%h = load i64, ptr %%h_slot, align 8
    %%hx = xor i64 %%h, %%b64
    %%hm = mul i64 %%hx, 1099511628211
    store i64 %%hm, ptr %%h_slot, align 8
    %%ni = add i64 %%i, 1
    store i64 %%ni, ptr %%i_slot, align 8
    br label %%cond
done:
    %%res = load i64, ptr %%h_slot, align 8
    ret i64 %%res
}

define internal void @ll_print_str_p(ptr %%s, i32 %%to_err, i32 %%newline) {
entry:
    %%is_err = icmp ne i32 %%to_err, 0
    br i1 %%is_err, label %%use_err, label %%use_out
use_err:
    %%fe = call ptr @ll_rt_stderr()
    br label %%write
use_out:
    %%fo = call ptr @ll_rt_stdout()
    br label %%write
write:
    %%out = phi ptr [ %%fe, %%use_err ], [ %%fo, %%use_out ]
    %%p_data = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 0
    %%p_len = getelementptr inbounds %%struct.ll_string, ptr %%s, i32 0, i32 1
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%c1 = icmp sgt i64 %%len, 0
    %%c2 = icmp ne ptr %%data, null
    %%do_w = and i1 %%c1, %%c2
    br i1 %%do_w, label %%do_fwrite, label %%chk_nl
do_fwrite:
    call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%out)
    br label %%chk_nl
chk_nl:
    %%do_nl = icmp ne i32 %%newline, 0
    br i1 %%do_nl, label %%put_nl, label %%done
put_nl:
    call i32 @fputc(i32 10, ptr %%out)
    br label %%done
done:
    ret void
}

define internal void @ll_panic_str_p(ptr %%msg, ptr %%file, i64 %%line, i64 %%col) {
entry:
    %%m_has = icmp ne ptr %%msg, null
    br i1 %%m_has, label %%load_m, label %%def_m
load_m:
    %%pm_d = getelementptr inbounds %%struct.ll_string, ptr %%msg, i32 0, i32 0
    %%pm_l = getelementptr inbounds %%struct.ll_string, ptr %%msg, i32 0, i32 1
    %%md0 = load ptr, ptr %%pm_d, align 8
    %%ml0 = load i64, ptr %%pm_l, align 8
    %%md_null = icmp eq ptr %%md0, null
    %%md1 = select i1 %%md_null, ptr %s, ptr %%md0
    %%ml1 = trunc i64 %%ml0 to i32
    br label %%chk_f
def_m:
    br label %%chk_f
chk_f:
    %%md = phi ptr [ %%md1, %%load_m ], [ %s, %%def_m ]
    %%ml = phi i32 [ %%ml1, %%load_m ], [ 0, %%def_m ]
    %%f_has = icmp ne ptr %%file, null
    br i1 %%f_has, label %%load_f, label %%def_f
load_f:
    %%pf_d = getelementptr inbounds %%struct.ll_string, ptr %%file, i32 0, i32 0
    %%pf_l = getelementptr inbounds %%struct.ll_string, ptr %%file, i32 0, i32 1
    %%fd0 = load ptr, ptr %%pf_d, align 8
    %%fl0 = load i64, ptr %%pf_l, align 8
    %%fd_null = icmp eq ptr %%fd0, null
    %%fd1 = select i1 %%fd_null, ptr %s, ptr %%fd0
    %%fl1 = trunc i64 %%fl0 to i32
    br label %%dispatch
def_f:
    br label %%dispatch
dispatch:
    %%fd = phi ptr [ %%fd1, %%load_f ], [ %s, %%def_f ]
    %%fl = phi i32 [ %%fl1, %%load_f ], [ 9, %%def_f ]
    %%has_col = icmp sgt i64 %%col, 0
    %%jmp = load ptr, ptr @ll_test_current_jmp, align 8
    %%in_test = icmp ne ptr %%jmp, null
    br i1 %%in_test, label %%test_jmp, label %%fatal
test_jmp:
    br i1 %%has_col, label %%tj_col, label %%tj_nocol
tj_col:
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr @ll_test_last_panic, i64 8192, ptr %s, i32 %%fl, ptr %%fd, i64 %%line, i64 %%col, i32 %%ml, ptr %%md)
    call void @longjmp(ptr %%jmp, i32 1)
    unreachable
tj_nocol:
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr @ll_test_last_panic, i64 8192, ptr %s, i32 %%fl, ptr %%fd, i64 %%line, i32 %%ml, ptr %%md)
    call void @longjmp(ptr %%jmp, i32 1)
    unreachable
fatal:
    %%err = call ptr @ll_rt_stderr()
    br i1 %%has_col, label %%fa_col, label %%fa_nocol
fa_col:
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, i32 %%fl, ptr %%fd, i64 %%line, i64 %%col, i32 %%ml, ptr %%md)
    call void @exit(i32 1)
    unreachable
fa_nocol:
    call i32 (ptr, ptr, ...) @fprintf(ptr %%err, ptr %s, i32 %%fl, ptr %%fd, i64 %%line, i32 %%ml, ptr %%md)
    call void @exit(i32 1)
    unreachable
}

define internal void @ll_assert_p(i1 %%cond, ptr %%msg, ptr %%file, i64 %%line, i64 %%col) {
entry:
    br i1 %%cond, label %%ok, label %%fail
fail:
    call void @ll_panic_str_p(ptr %%msg, ptr %%file, i64 %%line, i64 %%col)
    unreachable
ok:
    ret void
}

define internal %%struct.ll_string @ll_format_loc(%%struct.ll_string %%file, i64 %%line, i64 %%col) {
entry:
    %%buf = alloca [512 x i8], align 1
    %%fslot = alloca %%struct.ll_string, align 8
    store %%struct.ll_string %%file, ptr %%fslot, align 8
    %%pf_d = getelementptr inbounds %%struct.ll_string, ptr %%fslot, i32 0, i32 0
    %%pf_l = getelementptr inbounds %%struct.ll_string, ptr %%fslot, i32 0, i32 1
    %%fd0 = load ptr, ptr %%pf_d, align 8
    %%fl0 = load i64, ptr %%pf_l, align 8
    %%fd_null = icmp eq ptr %%fd0, null
    %%fd = select i1 %%fd_null, ptr %s, ptr %%fd0
    %%fl = trunc i64 %%fl0 to i32
    %%has_col = icmp sgt i64 %%col, 0
    br i1 %%has_col, label %%fmt_col, label %%fmt_nocol
fmt_col:
    %%n1 = call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%buf, i64 512, ptr %s, i32 %%fl, ptr %%fd, i64 %%line, i64 %%col)
    br label %%finish
fmt_nocol:
    %%n2 = call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%buf, i64 512, ptr %s, i32 %%fl, ptr %%fd, i64 %%line)
    br label %%finish
finish:
    %%n32 = phi i32 [ %%n1, %%fmt_col ], [ %%n2, %%fmt_nocol ]
    %%pos = icmp sgt i32 %%n32, 0
    %%n64 = sext i32 %%n32 to i64
    %%len0 = select i1 %%pos, i64 %%n64, i64 0
    %%over = icmp sgt i64 %%len0, 511
    %%len = select i1 %%over, i64 511, i64 %%len0
    %%tmp = alloca %%struct.ll_string, align 8
    %%p0 = getelementptr inbounds %%struct.ll_string, ptr %%tmp, i32 0, i32 0
    %%p1 = getelementptr inbounds %%struct.ll_string, ptr %%tmp, i32 0, i32 1
    %%p2 = getelementptr inbounds %%struct.ll_string, ptr %%tmp, i32 0, i32 2
    store ptr %%buf, ptr %%p0, align 8
    store i64 %%len, ptr %%p1, align 8
    store i64 0, ptr %%p2, align 8
    %%res = alloca %%struct.ll_string, align 8
    call void @ll_copy_p__string(ptr %%res, ptr %%tmp)
    %%out = load %%struct.ll_string, ptr %%res, align 8
    ret %%struct.ll_string %%out
}
`, emptySym, emptySym, emptySym, sliceOobSym, emptySym, emptySym, unkSym, unkSym,
		panicColJmpFmt, panicNoColJmpFmt, panicColErrFmt, panicNoColErrFmt,
		unkSym, locColFmt, locNoColFmt)
}

func (g *llvmEmitter) emitArenaRuntime() {
	fns := []string{
		"ll_arena_new_p", "ll_arena_bump_raw", "ll_arena_alloc",
		"ll_arena_run_dtors", "ll_arena_reset", "ll_arena_bytes_used", "ll_drop__Arena",
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	// ll_arena_chunk: { ptr next (0), i64 cap (8), i64 used (16), [0 x i8] data (24) } -> header size 24
	// ll_arena_dtor:  { ptr dtor (0), ptr ptr (8), ptr next (16) } -> size 24, align 8
	// ll_arena:       { ptr head (0), ptr dtors (8), i64 default_cap (16) }
	g.raw(`define internal void @ll_arena_new_p(ptr %res, i64 %initial_cap) {
entry:
    %lt = icmp slt i64 %initial_cap, 256
    %cap = select i1 %lt, i64 256, i64 %initial_cap
    %tot = add i64 %cap, 24
    %ch = call ptr @ll_alloc(i64 %tot)
    %p_next = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 0
    %p_cap = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 1
    %p_used = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 2
    store ptr null, ptr %p_next, align 8
    store i64 %cap, ptr %p_cap, align 8
    store i64 0, ptr %p_used, align 8
    %a_head = getelementptr inbounds %struct.ll_arena, ptr %res, i32 0, i32 0
    %a_dtors = getelementptr inbounds %struct.ll_arena, ptr %res, i32 0, i32 1
    %a_def = getelementptr inbounds %struct.ll_arena, ptr %res, i32 0, i32 2
    store ptr %ch, ptr %a_head, align 8
    store ptr null, ptr %a_dtors, align 8
    store i64 %cap, ptr %a_def, align 8
    ret void
}

define internal ptr @ll_arena_bump_raw(ptr %a, i64 %size_in, i64 %align_in) {
entry:
    %al_lt = icmp slt i64 %align_in, 1
    %align = select i1 %al_lt, i64 1, i64 %align_in
    %sz_lt = icmp slt i64 %size_in, 1
    %size = select i1 %sz_lt, i64 1, i64 %size_in
    %a_head = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 0
    %head0 = load ptr, ptr %a_head, align 8
    %no_head = icmp eq ptr %head0, null
    br i1 %no_head, label %init_arena, label %have_head
init_arena:
    %a_def = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 2
    %def0 = load i64, ptr %a_def, align 8
    %def_ok = icmp sge i64 %def0, 256
    %def = select i1 %def_ok, i64 %def0, i64 4096
    %sz2 = mul i64 %size, 2
    %use_def = icmp sgt i64 %def, %sz2
    %ic = select i1 %use_def, i64 %def, i64 %sz2
    call void @ll_arena_new_p(ptr %a, i64 %ic)
    br label %have_head
have_head:
    %ch = load ptr, ptr %a_head, align 8
    %p_cap = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 1
    %p_used = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 2
    %cap = load i64, ptr %p_cap, align 8
    %used = load i64, ptr %p_used, align 8
    %data_base = getelementptr inbounds i8, ptr %ch, i64 24
    %cur_ptr = getelementptr inbounds i8, ptr %data_base, i64 %used
    %base_int = ptrtoint ptr %cur_ptr to i64
    %mask = sub i64 %align, 1
    %not_mask = xor i64 %mask, -1
    %add_mask = add i64 %base_int, %mask
    %aligned_int = and i64 %add_mask, %not_mask
    %pad = sub i64 %aligned_int, %base_int
    %new_used = add i64 %used, %pad
    %end_used = add i64 %new_used, %size
    %fits = icmp sle i64 %end_used, %cap
    br i1 %fits, label %fast_ok, label %new_chunk
fast_ok:
    store i64 %end_used, ptr %p_used, align 8
    %aligned_ptr = inttoptr i64 %aligned_int to ptr
    ret ptr %aligned_ptr
new_chunk:
    %cap2 = mul i64 %cap, 2
    %min_need = add i64 %size, %align
    %min_cap = mul i64 %min_need, 2
    %need_more = icmp slt i64 %cap2, %min_need
    %next_cap = select i1 %need_more, i64 %min_cap, i64 %cap2
    %tot = add i64 %next_cap, 24
    %nch = call ptr @ll_alloc(i64 %tot)
    %np_next = getelementptr inbounds { ptr, i64, i64 }, ptr %nch, i32 0, i32 0
    %np_cap = getelementptr inbounds { ptr, i64, i64 }, ptr %nch, i32 0, i32 1
    %np_used = getelementptr inbounds { ptr, i64, i64 }, ptr %nch, i32 0, i32 2
    store ptr %ch, ptr %np_next, align 8
    store i64 %next_cap, ptr %np_cap, align 8
    %ndata = getelementptr inbounds i8, ptr %nch, i64 24
    %nbase = ptrtoint ptr %ndata to i64
    %nadd = add i64 %nbase, %mask
    %naligned = and i64 %nadd, %not_mask
    %npad = sub i64 %naligned, %nbase
    %nused = add i64 %npad, %size
    store i64 %nused, ptr %np_used, align 8
    store ptr %nch, ptr %a_head, align 8
    %nptr = inttoptr i64 %naligned to ptr
    ret ptr %nptr
}

define internal ptr @ll_arena_alloc(ptr %a, i64 %size, i64 %align, ptr %dtor) {
entry:
    %slot = call ptr @ll_arena_bump_raw(ptr %a, i64 %size, i64 %align)
    %has_dtor = icmp ne ptr %dtor, null
    br i1 %has_dtor, label %reg_dtor, label %done
reg_dtor:
    %dn = call ptr @ll_arena_bump_raw(ptr %a, i64 24, i64 8)
    %a_dtors = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 1
    %old_head = load ptr, ptr %a_dtors, align 8
    %p_dtor = getelementptr inbounds { ptr, ptr, ptr }, ptr %dn, i32 0, i32 0
    %p_ptr = getelementptr inbounds { ptr, ptr, ptr }, ptr %dn, i32 0, i32 1
    %p_next = getelementptr inbounds { ptr, ptr, ptr }, ptr %dn, i32 0, i32 2
    store ptr %dtor, ptr %p_dtor, align 8
    store ptr %slot, ptr %p_ptr, align 8
    store ptr %old_head, ptr %p_next, align 8
    store ptr %dn, ptr %a_dtors, align 8
    br label %done
done:
    ret ptr %slot
}

define internal void @ll_arena_run_dtors(ptr %a) {
entry:
    %a_dtors = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 1
    %d0 = load ptr, ptr %a_dtors, align 8
    store ptr null, ptr %a_dtors, align 8
    %cur = alloca ptr, align 8
    store ptr %d0, ptr %cur, align 8
    br label %cond
cond:
    %d = load ptr, ptr %cur, align 8
    %more = icmp ne ptr %d, null
    br i1 %more, label %body, label %done
body:
    %p_dtor = getelementptr inbounds { ptr, ptr, ptr }, ptr %d, i32 0, i32 0
    %p_ptr = getelementptr inbounds { ptr, ptr, ptr }, ptr %d, i32 0, i32 1
    %p_next = getelementptr inbounds { ptr, ptr, ptr }, ptr %d, i32 0, i32 2
    %fn = load ptr, ptr %p_dtor, align 8
    %arg = load ptr, ptr %p_ptr, align 8
    %nxt = load ptr, ptr %p_next, align 8
    store ptr %nxt, ptr %cur, align 8
    call void %fn(ptr %arg)
    br label %cond
done:
    ret void
}

define internal void @ll_arena_reset(ptr %a) {
entry:
    %is_null = icmp eq ptr %a, null
    br i1 %is_null, label %done, label %chk_head
chk_head:
    %a_head = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 0
    %head = load ptr, ptr %a_head, align 8
    %h_null = icmp eq ptr %head, null
    br i1 %h_null, label %done, label %do_reset
do_reset:
    call void @ll_arena_run_dtors(ptr %a)
    %p_next = getelementptr inbounds { ptr, i64, i64 }, ptr %head, i32 0, i32 0
    %p_used = getelementptr inbounds { ptr, i64, i64 }, ptr %head, i32 0, i32 2
    %extra0 = load ptr, ptr %p_next, align 8
    store ptr null, ptr %p_next, align 8
    store i64 0, ptr %p_used, align 8
    %cur = alloca ptr, align 8
    store ptr %extra0, ptr %cur, align 8
    br label %cond
cond:
    %ch = load ptr, ptr %cur, align 8
    %more = icmp ne ptr %ch, null
    br i1 %more, label %body, label %done
body:
    %cp_next = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 0
    %nxt = load ptr, ptr %cp_next, align 8
    store ptr %nxt, ptr %cur, align 8
    call void @ll_free(ptr %ch)
    br label %cond
done:
    ret void
}

define internal i64 @ll_arena_bytes_used(ptr %a) {
entry:
    %is_null = icmp eq ptr %a, null
    br i1 %is_null, label %ret_zero, label %start
ret_zero:
    ret i64 0
start:
    %a_head = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 0
    %head = load ptr, ptr %a_head, align 8
    %tot = alloca i64, align 8
    %cur = alloca ptr, align 8
    store i64 0, ptr %tot, align 8
    store ptr %head, ptr %cur, align 8
    br label %cond
cond:
    %ch = load ptr, ptr %cur, align 8
    %more = icmp ne ptr %ch, null
    br i1 %more, label %body, label %done
body:
    %p_next = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 0
    %p_used = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 2
    %u = load i64, ptr %p_used, align 8
    %nxt = load ptr, ptr %p_next, align 8
    %t0 = load i64, ptr %tot, align 8
    %t1 = add i64 %t0, %u
    store i64 %t1, ptr %tot, align 8
    store ptr %nxt, ptr %cur, align 8
    br label %cond
done:
    %res = load i64, ptr %tot, align 8
    ret i64 %res
}

define internal void @ll_drop__Arena(ptr %a) {
entry:
    %is_null = icmp eq ptr %a, null
    br i1 %is_null, label %done, label %start
start:
    call void @ll_arena_run_dtors(ptr %a)
    %a_head = getelementptr inbounds %struct.ll_arena, ptr %a, i32 0, i32 0
    %head = load ptr, ptr %a_head, align 8
    %cur = alloca ptr, align 8
    store ptr %head, ptr %cur, align 8
    br label %cond
cond:
    %ch = load ptr, ptr %cur, align 8
    %more = icmp ne ptr %ch, null
    br i1 %more, label %body, label %finish
body:
    %p_next = getelementptr inbounds { ptr, i64, i64 }, ptr %ch, i32 0, i32 0
    %nxt = load ptr, ptr %p_next, align 8
    store ptr %nxt, ptr %cur, align 8
    call void @ll_free(ptr %ch)
    br label %cond
finish:
    store %struct.ll_arena zeroinitializer, ptr %a, align 8
    br label %done
done:
    ret void
}
`)
}

func (g *llvmEmitter) emitSyscallAndOSRuntime() {
	emptySym := g.internString("").symbol
	rSym := g.internString("r").symbol
	rbSym := g.internString("rb").symbol
	rmFmtSym := g.internString("rm -rf \"%s\"").symbol
	slashSym := g.internString("/").symbol

	osStr := runtime.GOOS
	if osStr == "darwin" {
		osStr = "macos"
	}
	archStr := runtime.GOARCH
	if archStr == "amd64" {
		archStr = "x86_64"
	}
	osSym := g.internString(osStr).symbol
	archSym := g.internString(archStr).symbol

	scNproc := 84 // Linux _SC_NPROCESSORS_ONLN
	clockMono := 1 // Linux CLOCK_MONOTONIC
	if runtime.GOOS == "darwin" {
		scNproc = 58 // macOS _SC_NPROCESSORS_ONLN
		clockMono = 6 // macOS CLOCK_MONOTONIC
	}

	fns := []string{
		"ll_sys_fopen", "ll_sys_fclose", "ll_sys_fread", "ll_sys_fwrite",
		"ll_sys_fseek", "ll_sys_ftell", "ll_sys_fflush", "ll_sys_remove", "ll_sys_rename",
		"ll_os_popen", "ll_os_pclose", "ll_os_pread", "ll_os_system",
		"ll_os_file_size", "ll_os_file_exists", "ll_os_mkdir", "ll_os_remove_all",
		"ll_os_num_cpus", "ll_os_name_cstr", "ll_os_arch_cstr", "ll_os_argc", "ll_os_argv_cstr",
		"ll_time_monotonic_nanos", "ll_time_epoch_nanos", "ll_time_sleep_ms", "ll_time_local_offset",
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	g.line(`define internal ptr @ll_sys_fopen(ptr %%path, ptr %%mode) {
entry:
    %%c1 = icmp eq ptr %%path, null
    %%c2 = icmp eq ptr %%mode, null
    %%bad = or i1 %%c1, %%c2
    br i1 %%bad, label %%ret_null, label %%do_open
ret_null:
    ret ptr null
do_open:
    %%fp = call ptr @fopen(ptr %%path, ptr %%mode)
    ret ptr %%fp
}

define internal i64 @ll_sys_fclose(ptr %%f) {
entry:
    %%is_null = icmp eq ptr %%f, null
    br i1 %%is_null, label %%ret_err, label %%do_close
ret_err:
    ret i64 -1
do_close:
    %%rc = call i32 @fclose(ptr %%f)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal i64 @ll_sys_fread(ptr %%buf, i64 %%elem_size, i64 %%count, ptr %%f) {
entry:
    %%c1 = icmp eq ptr %%f, null
    %%c2 = icmp eq ptr %%buf, null
    %%c3 = icmp sle i64 %%elem_size, 0
    %%c4 = icmp sle i64 %%count, 0
    %%b1 = or i1 %%c1, %%c2
    %%b2 = or i1 %%c3, %%c4
    %%bad = or i1 %%b1, %%b2
    br i1 %%bad, label %%ret_zero, label %%do_read
ret_zero:
    ret i64 0
do_read:
    %%n = call i64 @fread(ptr %%buf, i64 %%elem_size, i64 %%count, ptr %%f)
    ret i64 %%n
}

define internal i64 @ll_sys_fwrite(ptr %%buf, i64 %%elem_size, i64 %%count, ptr %%f) {
entry:
    %%c1 = icmp eq ptr %%f, null
    %%c2 = icmp eq ptr %%buf, null
    %%c3 = icmp sle i64 %%elem_size, 0
    %%c4 = icmp sle i64 %%count, 0
    %%b1 = or i1 %%c1, %%c2
    %%b2 = or i1 %%c3, %%c4
    %%bad = or i1 %%b1, %%b2
    br i1 %%bad, label %%ret_zero, label %%do_write
ret_zero:
    ret i64 0
do_write:
    %%n = call i64 @fwrite(ptr %%buf, i64 %%elem_size, i64 %%count, ptr %%f)
    ret i64 %%n
}

define internal i64 @ll_sys_fseek(ptr %%f, i64 %%offset, i32 %%whence) {
entry:
    %%is_null = icmp eq ptr %%f, null
    br i1 %%is_null, label %%ret_err, label %%do_seek
ret_err:
    ret i64 -1
do_seek:
    %%rc = call i32 @fseek(ptr %%f, i64 %%offset, i32 %%whence)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal i64 @ll_sys_ftell(ptr %%f) {
entry:
    %%is_null = icmp eq ptr %%f, null
    br i1 %%is_null, label %%ret_err, label %%do_tell
ret_err:
    ret i64 -1
do_tell:
    %%pos = call i64 @ftell(ptr %%f)
    ret i64 %%pos
}

define internal i64 @ll_sys_fflush(ptr %%f) {
entry:
    %%is_null = icmp eq ptr %%f, null
    br i1 %%is_null, label %%ret_err, label %%do_flush
ret_err:
    ret i64 -1
do_flush:
    %%rc = call i32 @fflush(ptr %%f)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal i64 @ll_sys_remove(ptr %%path) {
entry:
    %%is_null = icmp eq ptr %%path, null
    br i1 %%is_null, label %%ret_err, label %%do_rm
ret_err:
    ret i64 -1
do_rm:
    %%rc = call i32 @remove(ptr %%path)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal i64 @ll_sys_rename(ptr %%old_p, ptr %%new_p) {
entry:
    %%c1 = icmp eq ptr %%old_p, null
    %%c2 = icmp eq ptr %%new_p, null
    %%bad = or i1 %%c1, %%c2
    br i1 %%bad, label %%ret_err, label %%do_ren
ret_err:
    ret i64 -1
do_ren:
    %%rc = call i32 @rename(ptr %%old_p, ptr %%new_p)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal ptr @ll_os_popen(ptr %%cmd) {
entry:
    %%is_null = icmp eq ptr %%cmd, null
    br i1 %%is_null, label %%ret_null, label %%do_popen
ret_null:
    ret ptr null
do_popen:
    %%fp = call ptr @popen(ptr %%cmd, ptr %s)
    ret ptr %%fp
}

define internal i64 @ll_os_pclose(ptr %%f) {
entry:
    %%is_null = icmp eq ptr %%f, null
    br i1 %%is_null, label %%ret_err, label %%do_pclose
ret_err:
    ret i64 -1
do_pclose:
    %%st = call i32 @pclose(ptr %%f)
    %%is_neg1 = icmp eq i32 %%st, -1
    br i1 %%is_neg1, label %%ret_err, label %%decode
decode:
    %%sig = and i32 %%st, 127
    %%exited = icmp eq i32 %%sig, 0
    %%code8 = lshr i32 %%st, 8
    %%code = and i32 %%code8, 255
    %%sel = select i1 %%exited, i32 %%code, i32 %%st
    %%res = sext i32 %%sel to i64
    ret i64 %%res
}

define internal i64 @ll_os_pread(ptr %%f, ptr %%buf, i64 %%cap) {
entry:
    %%c1 = icmp eq ptr %%f, null
    %%c2 = icmp eq ptr %%buf, null
    %%c3 = icmp sle i64 %%cap, 0
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%ret_zero, label %%do_read
ret_zero:
    ret i64 0
do_read:
    %%n = call i64 @fread(ptr %%buf, i64 1, i64 %%cap, ptr %%f)
    ret i64 %%n
}

define internal i64 @ll_os_system(ptr %%cmd) {
entry:
    %%is_null = icmp eq ptr %%cmd, null
    br i1 %%is_null, label %%ret_err, label %%do_sys
ret_err:
    ret i64 -1
do_sys:
    %%st = call i32 @system(ptr %%cmd)
    %%is_neg1 = icmp eq i32 %%st, -1
    br i1 %%is_neg1, label %%ret_err, label %%decode
decode:
    %%sig = and i32 %%st, 127
    %%exited = icmp eq i32 %%sig, 0
    %%code8 = lshr i32 %%st, 8
    %%code = and i32 %%code8, 255
    %%sel = select i1 %%exited, i32 %%code, i32 %%st
    %%res = sext i32 %%sel to i64
    ret i64 %%res
}

define internal i64 @ll_os_file_size(ptr %%path) {
entry:
    %%is_null = icmp eq ptr %%path, null
    br i1 %%is_null, label %%ret_err, label %%open_f
ret_err:
    ret i64 -1
open_f:
    %%fp = call ptr @fopen(ptr %%path, ptr %s)
    %%fnull = icmp eq ptr %%fp, null
    br i1 %%fnull, label %%ret_err, label %%seek_end
seek_end:
    %%sk = call i32 @fseek(ptr %%fp, i64 0, i32 2)
    %%sk_bad = icmp ne i32 %%sk, 0
    br i1 %%sk_bad, label %%close_err, label %%tell_sz
close_err:
    call i32 @fclose(ptr %%fp)
    ret i64 -1
tell_sz:
    %%sz = call i64 @ftell(ptr %%fp)
    call i32 @fclose(ptr %%fp)
    ret i64 %%sz
}

define internal i1 @ll_os_file_exists(ptr %%path) {
entry:
    %%is_null = icmp eq ptr %%path, null
    br i1 %%is_null, label %%ret_false, label %%chk
ret_false:
    ret i1 false
chk:
    %%rc = call i32 @access(ptr %%path, i32 0)
    %%ok = icmp eq i32 %%rc, 0
    ret i1 %%ok
}

define internal i64 @ll_os_mkdir(ptr %%path) {
entry:
    %%is_null = icmp eq ptr %%path, null
    br i1 %%is_null, label %%ret_err, label %%do_mkdir
ret_err:
    ret i64 -1
do_mkdir:
    %%rc = call i32 @mkdir(ptr %%path, i16 493)
    %%res = sext i32 %%rc to i64
    ret i64 %%res
}

define internal i64 @ll_os_remove_all(ptr %%path) {
entry:
    %%is_null = icmp eq ptr %%path, null
    br i1 %%is_null, label %%ret_err, label %%chk_empty
ret_err:
    ret i64 -1
chk_empty:
    %%c0 = load i8, ptr %%path, align 1
    %%is_empty = icmp eq i8 %%c0, 0
    br i1 %%is_empty, label %%ret_err, label %%chk_root
chk_root:
    %%cmp_root = call i32 @strcmp(ptr %%path, ptr %s)
    %%is_root = icmp eq i32 %%cmp_root, 0
    br i1 %%is_root, label %%ret_err, label %%run_rm
run_rm:
    %%cmd = alloca [4096 x i8], align 1
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%cmd, i64 4096, ptr %s, ptr %%path)
    %%res = call i64 @ll_os_system(ptr %%cmd)
    ret i64 %%res
}

define internal i64 @ll_os_num_cpus() {
entry:
    %%n = call i64 @sysconf(i32 %d)
    %%ok = icmp sge i64 %%n, 1
    %%res = select i1 %%ok, i64 %%n, i64 1
    ret i64 %%res
}

define internal ptr @ll_os_name_cstr() {
entry:
    ret ptr %s
}

define internal ptr @ll_os_arch_cstr() {
entry:
    ret ptr %s
}

define internal i64 @ll_os_argc() {
entry:
    %%c = load i32, ptr @ll_rt_argc, align 4
    %%res = sext i32 %%c to i64
    ret i64 %%res
}

define internal ptr @ll_os_argv_cstr(i64 %%idx) {
entry:
    %%c32 = load i32, ptr @ll_rt_argc, align 4
    %%argc = sext i32 %%c32 to i64
    %%c1 = icmp slt i64 %%idx, 0
    %%c2 = icmp sge i64 %%idx, %%argc
    %%bad = or i1 %%c1, %%c2
    br i1 %%bad, label %%ret_empty, label %%load_arg
ret_empty:
    ret ptr %s
load_arg:
    %%argv = load ptr, ptr @ll_rt_argv, align 8
    %%av_null = icmp eq ptr %%argv, null
    br i1 %%av_null, label %%ret_empty, label %%gep_arg
gep_arg:
    %%p = getelementptr inbounds ptr, ptr %%argv, i64 %%idx
    %%s = load ptr, ptr %%p, align 8
    %%s_null = icmp eq ptr %%s, null
    %%res = select i1 %%s_null, ptr %s, ptr %%s
    ret ptr %%res
}

define internal i64 @ll_time_monotonic_nanos() {
entry:
    %%ts = alloca { i64, i64 }, align 8
    store { i64, i64 } zeroinitializer, ptr %%ts, align 8
    call i32 @clock_gettime(i32 %d, ptr %%ts)
    %%p_sec = getelementptr inbounds { i64, i64 }, ptr %%ts, i32 0, i32 0
    %%p_nsec = getelementptr inbounds { i64, i64 }, ptr %%ts, i32 0, i32 1
    %%sec = load i64, ptr %%p_sec, align 8
    %%nsec = load i64, ptr %%p_nsec, align 8
    %%sec_ns = mul i64 %%sec, 1000000000
    %%tot = add i64 %%sec_ns, %%nsec
    ret i64 %%tot
}

define internal i64 @ll_time_epoch_nanos() {
entry:
    %%ts = alloca { i64, i64 }, align 8
    store { i64, i64 } zeroinitializer, ptr %%ts, align 8
    call i32 @clock_gettime(i32 0, ptr %%ts)
    %%p_sec = getelementptr inbounds { i64, i64 }, ptr %%ts, i32 0, i32 0
    %%p_nsec = getelementptr inbounds { i64, i64 }, ptr %%ts, i32 0, i32 1
    %%sec = load i64, ptr %%p_sec, align 8
    %%nsec = load i64, ptr %%p_nsec, align 8
    %%sec_ns = mul i64 %%sec, 1000000000
    %%tot = add i64 %%sec_ns, %%nsec
    ret i64 %%tot
}

define internal void @ll_time_sleep_ms(i64 %%ms) {
entry:
    %%pos = icmp sgt i64 %%ms, 0
    br i1 %%pos, label %%do_sleep, label %%done
do_sleep:
    %%req = alloca { i64, i64 }, align 8
    %%sec = sdiv i64 %%ms, 1000
    %%rem = srem i64 %%ms, 1000
    %%nsec = mul i64 %%rem, 1000000
    %%p_sec = getelementptr inbounds { i64, i64 }, ptr %%req, i32 0, i32 0
    %%p_nsec = getelementptr inbounds { i64, i64 }, ptr %%req, i32 0, i32 1
    store i64 %%sec, ptr %%p_sec, align 8
    store i64 %%nsec, ptr %%p_nsec, align 8
    call i32 @nanosleep(ptr %%req, ptr null)
    br label %%done
done:
    ret void
}

define internal i64 @ll_time_local_offset() {
entry:
    %%now_slot = alloca i64, align 8
    %%tm_buf = alloca [64 x i8], align 8
    call void @llvm.memset.p0.i64(ptr %%tm_buf, i8 0, i64 64, i1 false)
    %%now = call i64 @time(ptr null)
    store i64 %%now, ptr %%now_slot, align 8
    call ptr @localtime_r(ptr %%now_slot, ptr %%tm_buf)
    %%p_gmtoff = getelementptr inbounds i8, ptr %%tm_buf, i64 40
    %%off = load i64, ptr %%p_gmtoff, align 8
    ret i64 %%off
}
`, rSym, rbSym, slashSym, rmFmtSym, scNproc, osSym, archSym, emptySym, emptySym, clockMono)
}

func (g *llvmEmitter) emitNetAndTLSRuntime() {
	portFmt := g.internString("%lld").symbol
	zeroIpSym := g.internString("0.0.0.0").symbol
	starSym := g.internString("*").symbol

	solSocket := 1
	soReuseAddr := 2
	soSndTimeo := 21
	soRcvTimeo := 20
	msgNoSignal := 16384 // Linux MSG_NOSIGNAL (0x4000)
	aiAddrOffset := 24   // Linux offsetof(struct addrinfo, ai_addr)
	isDarwin := 0
	if runtime.GOOS == "darwin" {
		solSocket = 65535 // 0xffff
		soReuseAddr = 4
		soSndTimeo = 4101 // 0x1005
		soRcvTimeo = 4102 // 0x1006
		msgNoSignal = 0
		aiAddrOffset = 32 // macOS offsetof(struct addrinfo, ai_addr)
		isDarwin = 1
	}

	sLib1 := g.internString("libssl.dylib").symbol
	sLib2 := g.internString("/opt/homebrew/opt/openssl/lib/libssl.dylib").symbol
	sLib3 := g.internString("/usr/local/opt/openssl/lib/libssl.dylib").symbol
	sLib4 := g.internString("libssl.so.3").symbol
	sLib5 := g.internString("libssl.so.1.1").symbol
	sLib6 := g.internString("libssl.so").symbol

	symInitSsl := g.internString("OPENSSL_init_ssl").symbol
	symTlsClient := g.internString("TLS_client_method").symbol
	symSsl23Client := g.internString("SSLv23_client_method").symbol
	symTlsServer := g.internString("TLS_server_method").symbol
	symSsl23Server := g.internString("SSLv23_server_method").symbol
	symCtxNew := g.internString("SSL_CTX_new").symbol
	symCtxFree := g.internString("SSL_CTX_free").symbol
	symCtxDefVerify := g.internString("SSL_CTX_set_default_verify_paths").symbol
	symCtxCert := g.internString("SSL_CTX_use_certificate_file").symbol
	symCtxKey := g.internString("SSL_CTX_use_PrivateKey_file").symbol
	symSslNew := g.internString("SSL_new").symbol
	symSslFree := g.internString("SSL_free").symbol
	symSslSetFd := g.internString("SSL_set_fd").symbol
	symSslCtrl := g.internString("SSL_ctrl").symbol
	symSslConnect := g.internString("SSL_connect").symbol
	symSslAccept := g.internString("SSL_accept").symbol
	symSslRead := g.internString("SSL_read").symbol
	symSslWrite := g.internString("SSL_write").symbol
	symSslShutdown := g.internString("SSL_shutdown").symbol

	fns := []string{
		"ll_net_set_timeout", "ll_net_tcp_listen", "ll_net_tcp_port",
		"ll_net_tcp_accept", "ll_net_tcp_connect", "ll_net_send_all",
		"ll_net_recv", "ll_net_close", "ll_net_shutdown", "ll_net_socketpair",
		"ll_tls_init", "ll_tls_available", "ll_tls_client_handshake",
		"ll_tls_send_all", "ll_tls_recv", "ll_tls_close",
		"ll_tls_server_ctx_new", "ll_tls_server_ctx_free", "ll_tls_server_handshake",
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	g.line(`@ll_tls_init_done = internal global i32 0, align 4
@ll_tls_is_avail = internal global i32 0, align 4
@ll_fn_OPENSSL_init_ssl = internal global ptr null, align 8
@ll_fn_TLS_client_method = internal global ptr null, align 8
@ll_fn_TLS_server_method = internal global ptr null, align 8
@ll_fn_SSL_CTX_new = internal global ptr null, align 8
@ll_fn_SSL_CTX_free = internal global ptr null, align 8
@ll_fn_SSL_CTX_set_default_verify_paths = internal global ptr null, align 8
@ll_fn_SSL_CTX_use_certificate_file = internal global ptr null, align 8
@ll_fn_SSL_CTX_use_PrivateKey_file = internal global ptr null, align 8
@ll_fn_SSL_new = internal global ptr null, align 8
@ll_fn_SSL_free = internal global ptr null, align 8
@ll_fn_SSL_set_fd = internal global ptr null, align 8
@ll_fn_SSL_ctrl = internal global ptr null, align 8
@ll_fn_SSL_connect = internal global ptr null, align 8
@ll_fn_SSL_accept = internal global ptr null, align 8
@ll_fn_SSL_read = internal global ptr null, align 8
@ll_fn_SSL_write = internal global ptr null, align 8
@ll_fn_SSL_shutdown = internal global ptr null, align 8

define internal void @ll_net_set_timeout(i64 %%fd, i64 %%timeout_ms) {
entry:
    %%c1 = icmp slt i64 %%fd, 0
    %%c2 = icmp sle i64 %%timeout_ms, 0
    %%skip = or i1 %%c1, %%c2
    br i1 %%skip, label %%done, label %%apply
apply:
    %%tv = alloca { i64, i64 }, align 8
    %%sec = sdiv i64 %%timeout_ms, 1000
    %%rem = srem i64 %%timeout_ms, 1000
    %%usec = mul i64 %%rem, 1000
    %%p_sec = getelementptr inbounds { i64, i64 }, ptr %%tv, i32 0, i32 0
    %%p_usec = getelementptr inbounds { i64, i64 }, ptr %%tv, i32 0, i32 1
    store i64 %%sec, ptr %%p_sec, align 8
    store i64 %%usec, ptr %%p_usec, align 8
    %%fd32 = trunc i64 %%fd to i32
    call i32 @setsockopt(i32 %%fd32, i32 %d, i32 %d, ptr %%tv, i32 16)
    call i32 @setsockopt(i32 %%fd32, i32 %d, i32 %d, ptr %%tv, i32 16)
    br label %%done
done:
    ret void
}

define internal i64 @ll_net_tcp_listen(ptr %%host, i64 %%port, i64 %%backlog) {
entry:
    %%c1 = icmp slt i64 %%port, 0
    %%c2 = icmp sgt i64 %%port, 65535
    %%bad_port = or i1 %%c1, %%c2
    br i1 %%bad_port, label %%ret_err, label %%prep
ret_err:
    ret i64 -1
prep:
    %%port_str = alloca [16 x i8], align 1
    %%hints = alloca [48 x i8], align 8
    %%res_slot = alloca ptr, align 8
    store ptr null, ptr %%res_slot, align 8
    call void @llvm.memset.p0.i64(ptr %%hints, i8 0, i64 48, i1 false)
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%port_str, i64 16, ptr %s, i64 %%port)
    %%p_flags = getelementptr inbounds i32, ptr %%hints, i64 0
    %%p_fam = getelementptr inbounds i32, ptr %%hints, i64 1
    %%p_stype = getelementptr inbounds i32, ptr %%hints, i64 2
    store i32 1, ptr %%p_flags, align 4
    store i32 2, ptr %%p_fam, align 4
    store i32 1, ptr %%p_stype, align 4
    %%h_null = icmp eq ptr %%host, null
    br i1 %%h_null, label %%use_null_host, label %%chk_h0
chk_h0:
    %%h0 = load i8, ptr %%host, align 1
    %%h_empty = icmp eq i8 %%h0, 0
    br i1 %%h_empty, label %%use_null_host, label %%chk_any
chk_any:
    %%cmp0 = call i32 @strcmp(ptr %%host, ptr %s)
    %%is_any0 = icmp eq i32 %%cmp0, 0
    br i1 %%is_any0, label %%use_null_host, label %%chk_star
chk_star:
    %%cmp1 = call i32 @strcmp(ptr %%host, ptr %s)
    %%is_star = icmp eq i32 %%cmp1, 0
    br i1 %%is_star, label %%use_null_host, label %%use_given_host
use_null_host:
    br label %%do_gai
use_given_host:
    br label %%do_gai
do_gai:
    %%bind_host = phi ptr [ null, %%use_null_host ], [ %%host, %%use_given_host ]
    %%gai_rc = call i32 @getaddrinfo(ptr %%bind_host, ptr %%port_str, ptr %%hints, ptr %%res_slot)
    %%res0 = load ptr, ptr %%res_slot, align 8
    %%g1 = icmp ne i32 %%gai_rc, 0
    %%g2 = icmp eq ptr %%res0, null
    %%g_bad = or i1 %%g1, %%g2
    br i1 %%g_bad, label %%ret_err, label %%loop_init
loop_init:
    %%rp_slot = alloca ptr, align 8
    %%sfd_slot = alloca i32, align 4
    %%opt_slot = alloca i32, align 4
    store ptr %%res0, ptr %%rp_slot, align 8
    store i32 -1, ptr %%sfd_slot, align 4
    store i32 1, ptr %%opt_slot, align 4
    br label %%loop_cond
loop_cond:
    %%rp = load ptr, ptr %%rp_slot, align 8
    %%has_rp = icmp ne ptr %%rp, null
    br i1 %%has_rp, label %%loop_body, label %%loop_done
loop_body:
    %%rp_fam_p = getelementptr inbounds i32, ptr %%rp, i64 1
    %%rp_st_p = getelementptr inbounds i32, ptr %%rp, i64 2
    %%rp_pr_p = getelementptr inbounds i32, ptr %%rp, i64 3
    %%rp_len_p = getelementptr inbounds i32, ptr %%rp, i64 4
    %%rp_addr_p = getelementptr inbounds i8, ptr %%rp, i64 %d
    %%rp_next_p = getelementptr inbounds i8, ptr %%rp, i64 40
    %%af = load i32, ptr %%rp_fam_p, align 4
    %%st = load i32, ptr %%rp_st_p, align 4
    %%pr = load i32, ptr %%rp_pr_p, align 4
    %%alen = load i32, ptr %%rp_len_p, align 4
    %%addr = load ptr, ptr %%rp_addr_p, align 8
    %%next_rp = load ptr, ptr %%rp_next_p, align 8
    %%fd = call i32 @socket(i32 %%af, i32 %%st, i32 %%pr)
    %%fd_bad = icmp slt i32 %%fd, 0
    br i1 %%fd_bad, label %%next_iter, label %%cfg_sock
cfg_sock:
    call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%opt_slot, i32 4)
    %%is_mac = icmp ne i32 %d, 0
    br i1 %%is_mac, label %%set_nosig, label %%do_bind
set_nosig:
    call i32 @setsockopt(i32 %%fd, i32 65535, i32 4130, ptr %%opt_slot, i32 4)
    br label %%do_bind
do_bind:
    %%b_rc = call i32 @bind(i32 %%fd, ptr %%addr, i32 %%alen)
    %%b_ok = icmp eq i32 %%b_rc, 0
    br i1 %%b_ok, label %%do_listen, label %%close_next
do_listen:
    %%bl_pos = icmp sgt i64 %%backlog, 0
    %%bl32_raw = trunc i64 %%backlog to i32
    %%bl32 = select i1 %%bl_pos, i32 %%bl32_raw, i32 128
    %%l_rc = call i32 @listen(i32 %%fd, i32 %%bl32)
    %%l_ok = icmp eq i32 %%l_rc, 0
    br i1 %%l_ok, label %%found_sock, label %%close_next
found_sock:
    store i32 %%fd, ptr %%sfd_slot, align 4
    br label %%loop_done
close_next:
    call i32 @close(i32 %%fd)
    br label %%next_iter
next_iter:
    store ptr %%next_rp, ptr %%rp_slot, align 8
    br label %%loop_cond
loop_done:
    call void @freeaddrinfo(ptr %%res0)
    %%final_fd = load i32, ptr %%sfd_slot, align 4
    %%res64 = sext i32 %%final_fd to i64
    ret i64 %%res64
}

define internal i64 @ll_net_tcp_port(i64 %%fd) {
entry:
    %%bad_fd = icmp slt i64 %%fd, 0
    br i1 %%bad_fd, label %%ret_err, label %%query
ret_err:
    ret i64 -1
query:
    %%sa = alloca [32 x i8], align 4
    %%len_slot = alloca i32, align 4
    call void @llvm.memset.p0.i64(ptr %%sa, i8 0, i64 32, i1 false)
    store i32 32, ptr %%len_slot, align 4
    %%fd32 = trunc i64 %%fd to i32
    %%rc = call i32 @getsockname(i32 %%fd32, ptr %%sa, ptr %%len_slot)
    %%ok = icmp eq i32 %%rc, 0
    br i1 %%ok, label %%extract, label %%ret_err
extract:
    %%p_hi = getelementptr inbounds i8, ptr %%sa, i64 2
    %%p_lo = getelementptr inbounds i8, ptr %%sa, i64 3
    %%hi8 = load i8, ptr %%p_hi, align 1
    %%lo8 = load i8, ptr %%p_lo, align 1
    %%hi64 = zext i8 %%hi8 to i64
    %%lo64 = zext i8 %%lo8 to i64
    %%hi_sh = shl i64 %%hi64, 8
    %%port = or i64 %%hi_sh, %%lo64
    ret i64 %%port
}

define internal i64 @ll_net_tcp_accept(i64 %%listener_fd, i64 %%timeout_ms) {
entry:
    %%bad_fd = icmp slt i64 %%listener_fd, 0
    br i1 %%bad_fd, label %%ret_err, label %%chk_tmo
ret_err:
    ret i64 -1
chk_tmo:
    %%lfd32 = trunc i64 %%listener_fd to i32
    %%has_tmo = icmp sge i64 %%timeout_ms, 0
    br i1 %%has_tmo, label %%do_poll, label %%do_accept
do_poll:
    %%pfd = alloca { i32, i16, i16 }, align 4
    %%p_fd = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 0
    %%p_ev = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 1
    %%p_rev = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 2
    store i32 %%lfd32, ptr %%p_fd, align 4
    store i16 1, ptr %%p_ev, align 2
    store i16 0, ptr %%p_rev, align 2
    %%tmo32 = trunc i64 %%timeout_ms to i32
    %%pr = call i32 @poll(ptr %%pfd, i32 1, i32 %%tmo32)
    %%is_tmo = icmp eq i32 %%pr, 0
    br i1 %%is_tmo, label %%ret_tmo, label %%chk_perr
ret_tmo:
    ret i64 -2
chk_perr:
    %%p_bad = icmp slt i32 %%pr, 0
    br i1 %%p_bad, label %%ret_err, label %%do_accept
do_accept:
    %%cfd = call i32 @accept(i32 %%lfd32, ptr null, ptr null)
    %%c_bad = icmp slt i32 %%cfd, 0
    br i1 %%c_bad, label %%ret_err, label %%cfg_client
cfg_client:
    %%is_mac = icmp ne i32 %d, 0
    br i1 %%is_mac, label %%set_nosig, label %%done
set_nosig:
    %%opt = alloca i32, align 4
    store i32 1, ptr %%opt, align 4
    call i32 @setsockopt(i32 %%cfd, i32 65535, i32 4130, ptr %%opt, i32 4)
    br label %%done
done:
    %%res = sext i32 %%cfd to i64
    ret i64 %%res
}

define internal i64 @ll_net_tcp_connect(ptr %%host, i64 %%port, i64 %%timeout_ms) {
entry:
    %%c1 = icmp eq ptr %%host, null
    %%c2 = icmp sle i64 %%port, 0
    %%c3 = icmp sgt i64 %%port, 65535
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%ret_resolve_err, label %%prep
ret_resolve_err:
    ret i64 -1
prep:
    %%port_str = alloca [16 x i8], align 1
    %%hints = alloca [48 x i8], align 8
    %%res_slot = alloca ptr, align 8
    store ptr null, ptr %%res_slot, align 8
    call void @llvm.memset.p0.i64(ptr %%hints, i8 0, i64 48, i1 false)
    call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%port_str, i64 16, ptr %s, i64 %%port)
    %%p_stype = getelementptr inbounds i32, ptr %%hints, i64 2
    store i32 1, ptr %%p_stype, align 4
    %%gai_rc = call i32 @getaddrinfo(ptr %%host, ptr %%port_str, ptr %%hints, ptr %%res_slot)
    %%res0 = load ptr, ptr %%res_slot, align 8
    %%g1 = icmp ne i32 %%gai_rc, 0
    %%g2 = icmp eq ptr %%res0, null
    %%g_bad = or i1 %%g1, %%g2
    br i1 %%g_bad, label %%ret_resolve_err, label %%loop_init
loop_init:
    %%rp_slot = alloca ptr, align 8
    %%sfd_slot = alloca i32, align 4
    %%opt_slot = alloca i32, align 4
    store ptr %%res0, ptr %%rp_slot, align 8
    store i32 -1, ptr %%sfd_slot, align 4
    store i32 1, ptr %%opt_slot, align 4
    br label %%loop_cond
loop_cond:
    %%rp = load ptr, ptr %%rp_slot, align 8
    %%has_rp = icmp ne ptr %%rp, null
    br i1 %%has_rp, label %%loop_body, label %%loop_done
loop_body:
    %%rp_fam_p = getelementptr inbounds i32, ptr %%rp, i64 1
    %%rp_st_p = getelementptr inbounds i32, ptr %%rp, i64 2
    %%rp_pr_p = getelementptr inbounds i32, ptr %%rp, i64 3
    %%rp_len_p = getelementptr inbounds i32, ptr %%rp, i64 4
    %%rp_addr_p = getelementptr inbounds i8, ptr %%rp, i64 %d
    %%rp_next_p = getelementptr inbounds i8, ptr %%rp, i64 40
    %%af = load i32, ptr %%rp_fam_p, align 4
    %%st = load i32, ptr %%rp_st_p, align 4
    %%pr = load i32, ptr %%rp_pr_p, align 4
    %%alen = load i32, ptr %%rp_len_p, align 4
    %%addr = load ptr, ptr %%rp_addr_p, align 8
    %%next_rp = load ptr, ptr %%rp_next_p, align 8
    %%fd = call i32 @socket(i32 %%af, i32 %%st, i32 %%pr)
    %%fd_bad = icmp slt i32 %%fd, 0
    br i1 %%fd_bad, label %%next_iter, label %%cfg_sock
cfg_sock:
    %%is_mac = icmp ne i32 %d, 0
    br i1 %%is_mac, label %%set_nosig, label %%set_tmo
set_nosig:
    call i32 @setsockopt(i32 %%fd, i32 65535, i32 4130, ptr %%opt_slot, i32 4)
    br label %%set_tmo
set_tmo:
    %%fd64 = sext i32 %%fd to i64
    call void @ll_net_set_timeout(i64 %%fd64, i64 %%timeout_ms)
    %%c_rc = call i32 @connect(i32 %%fd, ptr %%addr, i32 %%alen)
    %%c_ok = icmp eq i32 %%c_rc, 0
    br i1 %%c_ok, label %%connected, label %%close_next
connected:
    store i32 %%fd, ptr %%sfd_slot, align 4
    br label %%loop_done
close_next:
    call i32 @close(i32 %%fd)
    br label %%next_iter
next_iter:
    store ptr %%next_rp, ptr %%rp_slot, align 8
    br label %%loop_cond
loop_done:
    call void @freeaddrinfo(ptr %%res0)
    %%final_fd = load i32, ptr %%sfd_slot, align 4
    %%ok_conn = icmp sge i32 %%final_fd, 0
    %%fd_ext = sext i32 %%final_fd to i64
    %%res = select i1 %%ok_conn, i64 %%fd_ext, i64 -2
    ret i64 %%res
}

define internal i64 @ll_net_send_all(i64 %%fd, ptr %%buf, i64 %%len) {
entry:
    %%bad_fd = icmp slt i64 %%fd, 0
    br i1 %%bad_fd, label %%ret_err, label %%chk_len
ret_err:
    ret i64 -1
chk_len:
    %%empty = icmp sle i64 %%len, 0
    br i1 %%empty, label %%ret_ok, label %%chk_buf
ret_ok:
    ret i64 0
chk_buf:
    %%b_null = icmp eq ptr %%buf, null
    br i1 %%b_null, label %%ret_err, label %%loop_init
loop_init:
    %%sent = alloca i64, align 8
    %%opt = alloca i32, align 4
    store i64 0, ptr %%sent, align 8
    %%fd32 = trunc i64 %%fd to i32
    %%is_mac = icmp ne i32 %d, 0
    br i1 %%is_mac, label %%set_nosig, label %%loop_cond_prep
set_nosig:
    store i32 1, ptr %%opt, align 4
    %%so_rc = call i32 @setsockopt(i32 %%fd32, i32 65535, i32 4130, ptr %%opt, i32 4)
    %%so_failed = icmp ne i32 %%so_rc, 0
    %%mac_flags = select i1 %%so_failed, i32 524288, i32 0
    br label %%loop_cond_prep
loop_cond_prep:
    %%send_flags = phi i32 [ %d, %%loop_init ], [ %%mac_flags, %%set_nosig ]
    br label %%loop_cond
loop_cond:
    %%s = load i64, ptr %%sent, align 8
    %%more = icmp slt i64 %%s, %%len
    br i1 %%more, label %%loop_body, label %%ret_ok
loop_body:
    %%ptr_cur = getelementptr inbounds i8, ptr %%buf, i64 %%s
    %%rem = sub i64 %%len, %%s
    %%n = call i64 @send(i32 %%fd32, ptr %%ptr_cur, i64 %%rem, i32 %%send_flags)
    %%ok = icmp sgt i64 %%n, 0
    br i1 %%ok, label %%advance, label %%ret_err
advance:
    %%ns = add i64 %%s, %%n
    store i64 %%ns, ptr %%sent, align 8
    br label %%loop_cond
}

define internal i64 @ll_net_recv(i64 %%fd, ptr %%buf, i64 %%max_len, i64 %%timeout_ms) {
entry:
    %%c1 = icmp slt i64 %%fd, 0
    %%c2 = icmp eq ptr %%buf, null
    %%c3 = icmp sle i64 %%max_len, 0
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%ret_err, label %%chk_tmo
ret_err:
    ret i64 -1
chk_tmo:
    %%fd32 = trunc i64 %%fd to i32
    %%has_tmo = icmp sge i64 %%timeout_ms, 0
    br i1 %%has_tmo, label %%do_poll, label %%do_recv
do_poll:
    %%pfd = alloca { i32, i16, i16 }, align 4
    %%p_fd = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 0
    %%p_ev = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 1
    %%p_rev = getelementptr inbounds { i32, i16, i16 }, ptr %%pfd, i32 0, i32 2
    store i32 %%fd32, ptr %%p_fd, align 4
    store i16 1, ptr %%p_ev, align 2
    store i16 0, ptr %%p_rev, align 2
    %%tmo32 = trunc i64 %%timeout_ms to i32
    %%pr = call i32 @poll(ptr %%pfd, i32 1, i32 %%tmo32)
    %%is_tmo = icmp eq i32 %%pr, 0
    br i1 %%is_tmo, label %%ret_tmo, label %%chk_perr
ret_tmo:
    ret i64 -2
chk_perr:
    %%p_bad = icmp slt i32 %%pr, 0
    br i1 %%p_bad, label %%ret_err, label %%do_recv
do_recv:
    %%n = call i64 @recv(i32 %%fd32, ptr %%buf, i64 %%max_len, i32 0)
    %%n_bad = icmp slt i64 %%n, 0
    %%res = select i1 %%n_bad, i64 -1, i64 %%n
    ret i64 %%res
}

define internal void @ll_net_close(i64 %%fd) {
entry:
    %%ok = icmp sge i64 %%fd, 0
    br i1 %%ok, label %%do_close, label %%done
do_close:
    %%fd32 = trunc i64 %%fd to i32
    call i32 @close(i32 %%fd32)
    br label %%done
done:
    ret void
}

define internal void @ll_net_shutdown(i64 %%fd) {
entry:
    %%ok = icmp sge i64 %%fd, 0
    br i1 %%ok, label %%do_shut, label %%done
do_shut:
    %%fd32 = trunc i64 %%fd to i32
    call i32 @shutdown(i32 %%fd32, i32 2)
    br label %%done
done:
    ret void
}

define internal i64 @ll_net_socketpair(ptr %%out_fds) {
entry:
    %%is_null = icmp eq ptr %%out_fds, null
    br i1 %%is_null, label %%ret_err, label %%do_pair
ret_err:
    ret i64 -1
do_pair:
    %%sv = alloca [2 x i32], align 4
    %%rc = call i32 @socketpair(i32 1, i32 1, i32 0, ptr %%sv)
    %%ok = icmp eq i32 %%rc, 0
    br i1 %%ok, label %%ok_store, label %%err_store
err_store:
    %%e0 = getelementptr inbounds i64, ptr %%out_fds, i64 0
    %%e1 = getelementptr inbounds i64, ptr %%out_fds, i64 1
    store i64 -1, ptr %%e0, align 8
    store i64 -1, ptr %%e1, align 8
    ret i64 -1
ok_store:
    %%p0 = getelementptr inbounds i32, ptr %%sv, i64 0
    %%p1 = getelementptr inbounds i32, ptr %%sv, i64 1
    %%fd0_32 = load i32, ptr %%p0, align 4
    %%fd1_32 = load i32, ptr %%p1, align 4
    %%is_mac = icmp ne i32 %d, 0
    br i1 %%is_mac, label %%set_nosig, label %%store_fds
set_nosig:
    %%opt = alloca i32, align 4
    store i32 1, ptr %%opt, align 4
    call i32 @setsockopt(i32 %%fd0_32, i32 65535, i32 4130, ptr %%opt, i32 4)
    call i32 @setsockopt(i32 %%fd1_32, i32 65535, i32 4130, ptr %%opt, i32 4)
    br label %%store_fds
store_fds:
    %%fd0 = sext i32 %%fd0_32 to i64
    %%fd1 = sext i32 %%fd1_32 to i64
    %%o0 = getelementptr inbounds i64, ptr %%out_fds, i64 0
    %%o1 = getelementptr inbounds i64, ptr %%out_fds, i64 1
    store i64 %%fd0, ptr %%o0, align 8
    store i64 %%fd1, ptr %%o1, align 8
    ret i64 0
}

define internal void @ll_tls_init() {
entry:
    %%done = load i32, ptr @ll_tls_init_done, align 4
    %%already = icmp ne i32 %%done, 0
    br i1 %%already, label %%ret, label %%start
ret:
    ret void
start:
    store i32 1, ptr @ll_tls_init_done, align 4
    %%cands = alloca [6 x ptr], align 8
    %%p0 = getelementptr inbounds ptr, ptr %%cands, i64 0
    %%p1 = getelementptr inbounds ptr, ptr %%cands, i64 1
    %%p2 = getelementptr inbounds ptr, ptr %%cands, i64 2
    %%p3 = getelementptr inbounds ptr, ptr %%cands, i64 3
    %%p4 = getelementptr inbounds ptr, ptr %%cands, i64 4
    %%p5 = getelementptr inbounds ptr, ptr %%cands, i64 5
    store ptr %s, ptr %%p0, align 8
    store ptr %s, ptr %%p1, align 8
    store ptr %s, ptr %%p2, align 8
    store ptr %s, ptr %%p3, align 8
    store ptr %s, ptr %%p4, align 8
    store ptr %s, ptr %%p5, align 8
    %%idx = alloca i64, align 8
    %%ssl_h = alloca ptr, align 8
    store i64 0, ptr %%idx, align 8
    store ptr null, ptr %%ssl_h, align 8
    br label %%loop
loop:
    %%i = load i64, ptr %%idx, align 8
    %%h_cur = load ptr, ptr %%ssl_h, align 8
    %%c1 = icmp slt i64 %%i, 6
    %%c2 = icmp eq ptr %%h_cur, null
    %%cont = and i1 %%c1, %%c2
    br i1 %%cont, label %%try_dl, label %%after_loop
try_dl:
    %%cp = getelementptr inbounds ptr, ptr %%cands, i64 %%i
    %%cname = load ptr, ptr %%cp, align 8
    %%h = call ptr @dlopen(ptr %%cname, i32 2)
    store ptr %%h, ptr %%ssl_h, align 8
    %%ni = add i64 %%i, 1
    store i64 %%ni, ptr %%idx, align 8
    br label %%loop
after_loop:
    %%lib = load ptr, ptr %%ssl_h, align 8
    %%lib_null = icmp eq ptr %%lib, null
    br i1 %%lib_null, label %%ret, label %%load_syms
load_syms:
    %%f_init = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_cm1 = call ptr @dlsym(ptr %%lib, ptr %s)
    %%cm1_null = icmp eq ptr %%f_cm1, null
    %%f_cm2 = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_cm = select i1 %%cm1_null, ptr %%f_cm2, ptr %%f_cm1
    %%f_sm1 = call ptr @dlsym(ptr %%lib, ptr %s)
    %%sm1_null = icmp eq ptr %%f_sm1, null
    %%f_sm2 = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sm = select i1 %%sm1_null, ptr %%f_sm2, ptr %%f_sm1
    %%f_cn = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_cf = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_cv = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_cc = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_ck = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sn = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sf = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sfd = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sc = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_conn = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_acc = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_rd = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_wr = call ptr @dlsym(ptr %%lib, ptr %s)
    %%f_sh = call ptr @dlsym(ptr %%lib, ptr %s)
    %%has_cm = icmp ne ptr %%f_cm, null
    %%has_cn = icmp ne ptr %%f_cn, null
    %%has_cf = icmp ne ptr %%f_cf, null
    %%has_sn = icmp ne ptr %%f_sn, null
    %%has_sf = icmp ne ptr %%f_sf, null
    %%has_sfd = icmp ne ptr %%f_sfd, null
    %%has_conn = icmp ne ptr %%f_conn, null
    %%has_rd = icmp ne ptr %%f_rd, null
    %%has_wr = icmp ne ptr %%f_wr, null
    %%a1 = and i1 %%has_cm, %%has_cn
    %%a2 = and i1 %%has_cf, %%has_sn
    %%a3 = and i1 %%has_sf, %%has_sfd
    %%a4 = and i1 %%has_conn, %%has_rd
    %%a5 = and i1 %%a1, %%a2
    %%a6 = and i1 %%a3, %%a4
    %%a7 = and i1 %%a5, %%a6
    %%all_ok = and i1 %%a7, %%has_wr
    br i1 %%all_ok, label %%mark_avail, label %%fail_close
fail_close:
    call i32 @dlclose(ptr %%lib)
    br label %%ret
mark_avail:
    store ptr %%f_init, ptr @ll_fn_OPENSSL_init_ssl, align 8
    store ptr %%f_cm, ptr @ll_fn_TLS_client_method, align 8
    store ptr %%f_sm, ptr @ll_fn_TLS_server_method, align 8
    store ptr %%f_cn, ptr @ll_fn_SSL_CTX_new, align 8
    store ptr %%f_cf, ptr @ll_fn_SSL_CTX_free, align 8
    store ptr %%f_cv, ptr @ll_fn_SSL_CTX_set_default_verify_paths, align 8
    store ptr %%f_cc, ptr @ll_fn_SSL_CTX_use_certificate_file, align 8
    store ptr %%f_ck, ptr @ll_fn_SSL_CTX_use_PrivateKey_file, align 8
    store ptr %%f_sn, ptr @ll_fn_SSL_new, align 8
    store ptr %%f_sf, ptr @ll_fn_SSL_free, align 8
    store ptr %%f_sfd, ptr @ll_fn_SSL_set_fd, align 8
    store ptr %%f_sc, ptr @ll_fn_SSL_ctrl, align 8
    store ptr %%f_conn, ptr @ll_fn_SSL_connect, align 8
    store ptr %%f_acc, ptr @ll_fn_SSL_accept, align 8
    store ptr %%f_rd, ptr @ll_fn_SSL_read, align 8
    store ptr %%f_wr, ptr @ll_fn_SSL_write, align 8
    store ptr %%f_sh, ptr @ll_fn_SSL_shutdown, align 8
    %%has_init = icmp ne ptr %%f_init, null
    br i1 %%has_init, label %%call_init, label %%set_flag
call_init:
    call i32 %%f_init(i64 0, ptr null)
    br label %%set_flag
set_flag:
    store i32 1, ptr @ll_tls_is_avail, align 4
    br label %%ret
}

define internal i64 @ll_tls_available() {
entry:
    call void @ll_tls_init()
    %%av = load i32, ptr @ll_tls_is_avail, align 4
    %%res = sext i32 %%av to i64
    ret i64 %%res
}

define internal ptr @ll_tls_client_handshake(i64 %%fd, ptr %%hostname) {
entry:
    call void @ll_tls_init()
    %%av = load i32, ptr @ll_tls_is_avail, align 4
    %%c1 = icmp eq i32 %%av, 0
    %%c2 = icmp slt i64 %%fd, 0
    %%bad = or i1 %%c1, %%c2
    br i1 %%bad, label %%ret_null, label %%create_ctx
ret_null:
    ret ptr null
create_ctx:
    %%fn_cm = load ptr, ptr @ll_fn_TLS_client_method, align 8
    %%meth = call ptr %%fn_cm()
    %%m_null = icmp eq ptr %%meth, null
    br i1 %%m_null, label %%ret_null, label %%new_ctx
new_ctx:
    %%fn_cn = load ptr, ptr @ll_fn_SSL_CTX_new, align 8
    %%ctx = call ptr %%fn_cn(ptr %%meth)
    %%ctx_null = icmp eq ptr %%ctx, null
    br i1 %%ctx_null, label %%ret_null, label %%verify_paths
verify_paths:
    %%fn_cv = load ptr, ptr @ll_fn_SSL_CTX_set_default_verify_paths, align 8
    %%has_cv = icmp ne ptr %%fn_cv, null
    br i1 %%has_cv, label %%call_cv, label %%new_ssl
call_cv:
    call i32 %%fn_cv(ptr %%ctx)
    br label %%new_ssl
new_ssl:
    %%fn_sn = load ptr, ptr @ll_fn_SSL_new, align 8
    %%ssl = call ptr %%fn_sn(ptr %%ctx)
    %%ssl_null = icmp eq ptr %%ssl, null
    br i1 %%ssl_null, label %%free_ctx_err, label %%set_fd
free_ctx_err:
    %%fn_cf0 = load ptr, ptr @ll_fn_SSL_CTX_free, align 8
    call void %%fn_cf0(ptr %%ctx)
    ret ptr null
set_fd:
    %%fd32 = trunc i64 %%fd to i32
    %%fn_sfd = load ptr, ptr @ll_fn_SSL_set_fd, align 8
    call i32 %%fn_sfd(ptr %%ssl, i32 %%fd32)
    %%fn_sc = load ptr, ptr @ll_fn_SSL_ctrl, align 8
    %%has_sc = icmp ne ptr %%fn_sc, null
    %%has_host = icmp ne ptr %%hostname, null
    %%do_sni = and i1 %%has_sc, %%has_host
    br i1 %%do_sni, label %%chk_host_nonempty, label %%do_conn
chk_host_nonempty:
    %%hc0 = load i8, ptr %%hostname, align 1
    %%h_nonempty = icmp ne i8 %%hc0, 0
    br i1 %%h_nonempty, label %%call_sni, label %%do_conn
call_sni:
    call i64 %%fn_sc(ptr %%ssl, i32 55, i64 0, ptr %%hostname)
    br label %%do_conn
do_conn:
    %%fn_conn = load ptr, ptr @ll_fn_SSL_connect, align 8
    %%rc = call i32 %%fn_conn(ptr %%ssl)
    %%ok = icmp eq i32 %%rc, 1
    br i1 %%ok, label %%alloc_sess, label %%conn_err
conn_err:
    %%fn_sf0 = load ptr, ptr @ll_fn_SSL_free, align 8
    %%fn_cf1 = load ptr, ptr @ll_fn_SSL_CTX_free, align 8
    call void %%fn_sf0(ptr %%ssl)
    call void %%fn_cf1(ptr %%ctx)
    ret ptr null
alloc_sess:
    %%sess = call ptr @ll_alloc(i64 24)
    %%p_ssl = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 0
    %%p_ctx = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 1
    %%p_fd = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 2
    store ptr %%ssl, ptr %%p_ssl, align 8
    store ptr %%ctx, ptr %%p_ctx, align 8
    store i32 %%fd32, ptr %%p_fd, align 8
    ret ptr %%sess
}

define internal i64 @ll_tls_send_all(ptr %%raw_sess, ptr %%buf, i64 %%len) {
entry:
    %%is_null = icmp eq ptr %%raw_sess, null
    br i1 %%is_null, label %%ret_err, label %%chk_ssl
ret_err:
    ret i64 -1
chk_ssl:
    %%p_ssl = getelementptr inbounds { ptr, ptr, i32 }, ptr %%raw_sess, i32 0, i32 0
    %%ssl = load ptr, ptr %%p_ssl, align 8
    %%ssl_null = icmp eq ptr %%ssl, null
    br i1 %%ssl_null, label %%ret_err, label %%chk_len
chk_len:
    %%empty = icmp sle i64 %%len, 0
    br i1 %%empty, label %%ret_ok, label %%chk_buf
ret_ok:
    ret i64 0
chk_buf:
    %%b_null = icmp eq ptr %%buf, null
    br i1 %%b_null, label %%ret_err, label %%loop_init
loop_init:
    %%sent = alloca i64, align 8
    store i64 0, ptr %%sent, align 8
    %%fn_wr = load ptr, ptr @ll_fn_SSL_write, align 8
    br label %%loop_cond
loop_cond:
    %%s = load i64, ptr %%sent, align 8
    %%more = icmp slt i64 %%s, %%len
    br i1 %%more, label %%loop_body, label %%ret_ok
loop_body:
    %%rem = sub i64 %%len, %%s
    %%gt_max = icmp sgt i64 %%rem, 1073741824
    %%chunk64 = select i1 %%gt_max, i64 1073741824, i64 %%rem
    %%chunk32 = trunc i64 %%chunk64 to i32
    %%ptr_cur = getelementptr inbounds i8, ptr %%buf, i64 %%s
    %%n32 = call i32 %%fn_wr(ptr %%ssl, ptr %%ptr_cur, i32 %%chunk32)
    %%ok = icmp sgt i32 %%n32, 0
    br i1 %%ok, label %%advance, label %%ret_err
advance:
    %%n64 = sext i32 %%n32 to i64
    %%ns = add i64 %%s, %%n64
    store i64 %%ns, ptr %%sent, align 8
    br label %%loop_cond
}

define internal i64 @ll_tls_recv(ptr %%raw_sess, ptr %%buf, i64 %%max_len, i64 %%timeout_ms) {
entry:
    %%c1 = icmp eq ptr %%raw_sess, null
    %%c2 = icmp eq ptr %%buf, null
    %%c3 = icmp sle i64 %%max_len, 0
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%ret_err, label %%load_sess
ret_err:
    ret i64 -1
load_sess:
    %%p_ssl = getelementptr inbounds { ptr, ptr, i32 }, ptr %%raw_sess, i32 0, i32 0
    %%p_fd = getelementptr inbounds { ptr, ptr, i32 }, ptr %%raw_sess, i32 0, i32 2
    %%ssl = load ptr, ptr %%p_ssl, align 8
    %%fd32 = load i32, ptr %%p_fd, align 8
    %%ssl_null = icmp eq ptr %%ssl, null
    br i1 %%ssl_null, label %%ret_err, label %%chk_tmo
chk_tmo:
    %%has_tmo = icmp sgt i64 %%timeout_ms, 0
    br i1 %%has_tmo, label %%set_tmo, label %%do_read
set_tmo:
    %%fd64 = sext i32 %%fd32 to i64
    call void @ll_net_set_timeout(i64 %%fd64, i64 %%timeout_ms)
    br label %%do_read
do_read:
    %%gt_max = icmp sgt i64 %%max_len, 1073741824
    %%lim64 = select i1 %%gt_max, i64 1073741824, i64 %%max_len
    %%lim32 = trunc i64 %%lim64 to i32
    %%fn_rd = load ptr, ptr @ll_fn_SSL_read, align 8
    %%n32 = call i32 %%fn_rd(ptr %%ssl, ptr %%buf, i32 %%lim32)
    %%pos = icmp sgt i32 %%n32, 0
    br i1 %%pos, label %%ret_n, label %%chk_eof
ret_n:
    %%n64 = sext i32 %%n32 to i64
    ret i64 %%n64
chk_eof:
    %%is_zero = icmp eq i32 %%n32, 0
    %%res = select i1 %%is_zero, i64 0, i64 -1
    ret i64 %%res
}

define internal void @ll_tls_close(ptr %%raw_sess) {
entry:
    %%is_null = icmp eq ptr %%raw_sess, null
    br i1 %%is_null, label %%done, label %%cleanup
cleanup:
    %%p_ssl = getelementptr inbounds { ptr, ptr, i32 }, ptr %%raw_sess, i32 0, i32 0
    %%p_ctx = getelementptr inbounds { ptr, ptr, i32 }, ptr %%raw_sess, i32 0, i32 1
    %%ssl = load ptr, ptr %%p_ssl, align 8
    %%ctx = load ptr, ptr %%p_ctx, align 8
    %%has_ssl = icmp ne ptr %%ssl, null
    br i1 %%has_ssl, label %%free_ssl, label %%chk_ctx
free_ssl:
    %%fn_sh = load ptr, ptr @ll_fn_SSL_shutdown, align 8
    %%has_sh = icmp ne ptr %%fn_sh, null
    br i1 %%has_sh, label %%call_sh, label %%do_sf
call_sh:
    call i32 %%fn_sh(ptr %%ssl)
    br label %%do_sf
do_sf:
    %%fn_sf = load ptr, ptr @ll_fn_SSL_free, align 8
    call void %%fn_sf(ptr %%ssl)
    store ptr null, ptr %%p_ssl, align 8
    br label %%chk_ctx
chk_ctx:
    %%has_ctx = icmp ne ptr %%ctx, null
    br i1 %%has_ctx, label %%free_ctx, label %%free_struct
free_ctx:
    %%fn_cf = load ptr, ptr @ll_fn_SSL_CTX_free, align 8
    call void %%fn_cf(ptr %%ctx)
    store ptr null, ptr %%p_ctx, align 8
    br label %%free_struct
free_struct:
    call void @ll_free(ptr %%raw_sess)
    br label %%done
done:
    ret void
}

define internal ptr @ll_tls_server_ctx_new(ptr %%cert_pem, ptr %%key_pem) {
entry:
    call void @ll_tls_init()
    %%av = load i32, ptr @ll_tls_is_avail, align 4
    %%c1 = icmp eq i32 %%av, 0
    %%c2 = icmp eq ptr %%cert_pem, null
    %%c3 = icmp eq ptr %%key_pem, null
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%ret_null, label %%chk_fns
ret_null:
    ret ptr null
chk_fns:
    %%fn_sm = load ptr, ptr @ll_fn_TLS_server_method, align 8
    %%fn_cc = load ptr, ptr @ll_fn_SSL_CTX_use_certificate_file, align 8
    %%fn_ck = load ptr, ptr @ll_fn_SSL_CTX_use_PrivateKey_file, align 8
    %%h1 = icmp eq ptr %%fn_sm, null
    %%h2 = icmp eq ptr %%fn_cc, null
    %%h3 = icmp eq ptr %%fn_ck, null
    %%hb1 = or i1 %%h1, %%h2
    %%hbad = or i1 %%hb1, %%h3
    br i1 %%hbad, label %%ret_null, label %%create_ctx
create_ctx:
    %%meth = call ptr %%fn_sm()
    %%m_null = icmp eq ptr %%meth, null
    br i1 %%m_null, label %%ret_null, label %%new_ctx
new_ctx:
    %%fn_cn = load ptr, ptr @ll_fn_SSL_CTX_new, align 8
    %%ctx = call ptr %%fn_cn(ptr %%meth)
    %%ctx_null = icmp eq ptr %%ctx, null
    br i1 %%ctx_null, label %%ret_null, label %%load_cert
load_cert:
    %%rc1 = call i32 %%fn_cc(ptr %%ctx, ptr %%cert_pem, i32 1)
    %%ok1 = icmp eq i32 %%rc1, 1
    br i1 %%ok1, label %%load_key, label %%err_ctx
load_key:
    %%rc2 = call i32 %%fn_ck(ptr %%ctx, ptr %%key_pem, i32 1)
    %%ok2 = icmp eq i32 %%rc2, 1
    br i1 %%ok2, label %%done, label %%err_ctx
err_ctx:
    %%fn_cf = load ptr, ptr @ll_fn_SSL_CTX_free, align 8
    call void %%fn_cf(ptr %%ctx)
    ret ptr null
done:
    ret ptr %%ctx
}

define internal void @ll_tls_server_ctx_free(ptr %%raw_ctx) {
entry:
    %%is_null = icmp eq ptr %%raw_ctx, null
    br i1 %%is_null, label %%done, label %%do_free
do_free:
    %%fn_cf = load ptr, ptr @ll_fn_SSL_CTX_free, align 8
    %%has_cf = icmp ne ptr %%fn_cf, null
    br i1 %%has_cf, label %%call_cf, label %%done
call_cf:
    call void %%fn_cf(ptr %%raw_ctx)
    br label %%done
done:
    ret void
}

define internal ptr @ll_tls_server_handshake(ptr %%raw_ctx, i64 %%client_fd) {
entry:
    call void @ll_tls_init()
    %%av = load i32, ptr @ll_tls_is_avail, align 4
    %%fn_acc = load ptr, ptr @ll_fn_SSL_accept, align 8
    %%c1 = icmp eq i32 %%av, 0
    %%c2 = icmp eq ptr %%raw_ctx, null
    %%c3 = icmp slt i64 %%client_fd, 0
    %%c4 = icmp eq ptr %%fn_acc, null
    %%b1 = or i1 %%c1, %%c2
    %%b2 = or i1 %%c3, %%c4
    %%bad = or i1 %%b1, %%b2
    br i1 %%bad, label %%ret_null, label %%new_ssl
ret_null:
    ret ptr null
new_ssl:
    %%fn_sn = load ptr, ptr @ll_fn_SSL_new, align 8
    %%ssl = call ptr %%fn_sn(ptr %%raw_ctx)
    %%ssl_null = icmp eq ptr %%ssl, null
    br i1 %%ssl_null, label %%ret_null, label %%do_acc
do_acc:
    %%fd32 = trunc i64 %%client_fd to i32
    %%fn_sfd = load ptr, ptr @ll_fn_SSL_set_fd, align 8
    call i32 %%fn_sfd(ptr %%ssl, i32 %%fd32)
    %%rc = call i32 %%fn_acc(ptr %%ssl)
    %%ok = icmp eq i32 %%rc, 1
    br i1 %%ok, label %%alloc_sess, label %%err_ssl
err_ssl:
    %%fn_sf = load ptr, ptr @ll_fn_SSL_free, align 8
    call void %%fn_sf(ptr %%ssl)
    ret ptr null
alloc_sess:
    %%sess = call ptr @ll_alloc(i64 24)
    %%p_ssl = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 0
    %%p_ctx = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 1
    %%p_fd = getelementptr inbounds { ptr, ptr, i32 }, ptr %%sess, i32 0, i32 2
    store ptr %%ssl, ptr %%p_ssl, align 8
    store ptr null, ptr %%p_ctx, align 8
    store i32 %%fd32, ptr %%p_fd, align 8
    ret ptr %%sess
}
`, solSocket, soRcvTimeo, solSocket, soSndTimeo,
		portFmt, zeroIpSym, starSym, aiAddrOffset, solSocket, soReuseAddr, isDarwin,
		isDarwin,
		portFmt, aiAddrOffset, isDarwin,
		isDarwin, msgNoSignal,
		isDarwin,
		sLib1, sLib2, sLib3, sLib4, sLib5, sLib6,
		symInitSsl, symTlsClient, symSsl23Client, symTlsServer, symSsl23Server,
		symCtxNew, symCtxFree, symCtxDefVerify, symCtxCert, symCtxKey,
		symSslNew, symSslFree, symSslSetFd, symSslCtrl, symSslConnect,
		symSslAccept, symSslRead, symSslWrite, symSslShutdown)
}

func (g *llvmEmitter) emitTestHarnessRuntime() {
	runFmt := g.internString("=== RUN   %s\n").symbol
	passFmt := g.internString("--- PASS: %s (%lld\xc2\xb5s, 0 leaks)\n").symbol
	leakFmt := g.internString("--- FAIL: %s (leaked %lld heap allocation(s))\n").symbol
	failMultiFmt := g.internString("--- FAIL: %s\n    %s\n").symbol
	failSingleFmt := g.internString("--- FAIL: %s (%s)\n").symbol
	summaryFmt := g.internString("\nTest results: %d passed, %d failed (total %d)\n").symbol

	g.funcSet["ll_run_test_case"] = true
	g.funcSet["ll_report_test_summary"] = true

	g.line(`define internal i32 @ll_run_test_case(ptr %%test_fn, ptr %%desc) {
entry:
    %%jmp_buf = alloca [512 x i8], align 16
    call void @llvm.memset.p0.i64(ptr %%jmp_buf, i8 0, i64 512, i1 false)
    call i32 (ptr, ...) @printf(ptr %s, ptr %%desc)
    %%out_f = call ptr @ll_rt_stdout()
    call i32 @fflush(ptr %%out_f)
    %%allocs_before = load i64, ptr @ll_rt_live_allocs, align 8
    %%t0 = call i64 @ll_time_monotonic_nanos()
    store ptr %%jmp_buf, ptr @ll_test_current_jmp, align 8
    store i8 0, ptr @ll_test_last_panic, align 1
    %%jb = call i32 @setjmp(ptr %%jmp_buf) returns_twice
    %%is_zero = icmp eq i32 %%jb, 0
    br i1 %%is_zero, label %%run_test, label %%on_panic
run_test:
    call void %%test_fn()
    store ptr null, ptr @ll_test_current_jmp, align 8
    %%t1 = call i64 @ll_time_monotonic_nanos()
    %%dt_ns = sub i64 %%t1, %%t0
    %%elapsed_us = sdiv i64 %%dt_ns, 1000
    %%allocs_after = load i64, ptr @ll_rt_live_allocs, align 8
    %%leaked = sub i64 %%allocs_after, %%allocs_before
    %%has_leak = icmp sgt i64 %%leaked, 0
    br i1 %%has_leak, label %%on_leak, label %%on_pass
on_leak:
    call i32 (ptr, ...) @printf(ptr %s, ptr %%desc, i64 %%leaked)
    %%out_f2 = call ptr @ll_rt_stdout()
    call i32 @fflush(ptr %%out_f2)
    ret i32 1
on_pass:
    call i32 (ptr, ...) @printf(ptr %s, ptr %%desc, i64 %%elapsed_us)
    %%out_f3 = call ptr @ll_rt_stdout()
    call i32 @fflush(ptr %%out_f3)
    ret i32 0
on_panic:
    store ptr null, ptr @ll_test_current_jmp, align 8
    %%nl = call ptr @strchr(ptr @ll_test_last_panic, i32 10)
    %%has_nl = icmp ne ptr %%nl, null
    br i1 %%has_nl, label %%print_multi, label %%print_single
print_multi:
    call i32 (ptr, ...) @printf(ptr %s, ptr %%desc, ptr @ll_test_last_panic)
    br label %%ret_fail
print_single:
    call i32 (ptr, ...) @printf(ptr %s, ptr %%desc, ptr @ll_test_last_panic)
    br label %%ret_fail
ret_fail:
    %%out_f4 = call ptr @ll_rt_stdout()
    call i32 @fflush(ptr %%out_f4)
    ret i32 1
}

define internal void @ll_report_test_summary(i32 %%pass_count, i32 %%fail_count, i32 %%total_count) {
entry:
    call i32 (ptr, ...) @printf(ptr %s, i32 %%pass_count, i32 %%fail_count, i32 %%total_count)
    ret void
}
`, runFmt, leakFmt, passFmt, failMultiFmt, failSingleFmt, summaryFmt)
}

// -----------------------------------------------------------------------------
// Synthesized Composite & Collection Helpers in Pure LLVM IR
// -----------------------------------------------------------------------------

func (g *llvmEmitter) emitSynthesizedHelpersLLVM() {
	g.line("; Synthesized Runtime Helpers for Primitives, Composites, and Collections")
	g.emitPrimitiveHelpersLLVM()

	for _, en := range g.prog.Enums {
		if !en.IsTagged {
			g.emitSimpleEnumHelpersLLVM(en)
		}
	}

	for _, t := range g.prog.OrderedTypes {
		switch v := t.(type) {
		case *types.Distinct:
			g.emitDistinctHelpersLLVM(v)
		case *types.Struct:
			g.emitStructHelpersLLVM(v)
			if v.BoxedOptional {
				g.emitOptionalHelpersLLVM(types.NewOptional(v))
			}
		case *types.Enum:
			if v.IsTagged {
				g.emitTaggedEnumHelpersLLVM(v)
			}
		case *types.Optional:
			g.emitOptionalHelpersLLVM(v)
		case *types.Vector:
			g.emitVectorHelpersLLVM(v)
		case *types.StackArray:
			g.emitStackArrayHelpersLLVM(v)
		case *types.Tuple:
			g.emitTupleHelpersLLVM(v)
		case *types.Set:
			g.emitSetHelpersLLVM(v)
		case *types.Map:
			g.emitMapHelpersLLVM(v)
		case *types.Func:
			g.emitClosureHelpersLLVM(v)
		case *types.Interface:
			g.emitInterfaceHelpersLLVM(v)
		}
	}

	for _, vec := range g.prog.Vectors {
		g.emitVectorMethodsLLVM(vec)
	}
	for _, arr := range g.prog.Arrays {
		g.emitStackArrayMethodsLLVM(arr)
	}
	for _, set := range g.prog.Sets {
		g.emitSetMethodsLLVM(set)
	}
	for _, mp := range g.prog.Maps {
		g.emitMapMethodsLLVM(mp)
	}

	if g.prog.Checked != nil {
		for _, li := range g.prog.Checked.Lambdas {
			if len(li.Captures) > 0 {
				g.emitLambdaEnvHelpersLLVM(li)
			}
		}
	}

	g.emitPointerHelpersFromDeclsLLVM()
}

func (g *llvmEmitter) emitValueDropInline(ptrReg string, t types.Type) {
	if t == nil || t.IsPlain() {
		return
	}
	if d, ok := t.(*types.Distinct); ok {
		g.emitValueDropInline(ptrReg, d.Base)
		return
	}
	fn := ir.DropFnName(t)
	g.indentLine(1, "call void @%s(ptr %s)", fn, ptrReg)
}

func (g *llvmEmitter) emitValueCopyInline(dstPtr, srcPtr string, t types.Type) {
	if t == nil {
		return
	}
	if _, isPtr := t.(*types.Ptr); isPtr {
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", r, srcPtr)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", r, dstPtr)
		return
	}
	if _, isRef := t.(*types.Ref); isRef {
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", r, srcPtr)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", r, dstPtr)
		return
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "copy"); custom != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		v := g.newReg()
		res := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", v, tyStr, srcPtr, al)
		g.indentLine(1, "%s = call %s @%s(%s %s)", res, tyStr, custom.CName, tyStr, v)
		g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, res, dstPtr, al)
		return
	}
	if t.IsPlain() {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", r, tyStr, srcPtr, al)
		g.indentLine(1, "store %s %s, ptr %s, align %d", tyStr, r, dstPtr, al)
		return
	}
	fn := "ll_copy_p__" + t.Mangled()
	g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, dstPtr, srcPtr)
}

func (g *llvmEmitter) emitValueCmpInline(ptrA, ptrB string, t types.Type) string {
	if customCmp := g.prog.Checked.LookupConcreteMethod(t, "cmp"); customCmp != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		a := g.newReg()
		b := g.newReg()
		c := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", a, tyStr, ptrA, al)
		g.indentLine(1, "%s = load %s, ptr %s, align %d", b, tyStr, ptrB, al)
		g.indentLine(1, "%s = call i64 @%s(%s %s, %s %s)", c, customCmp.CName, tyStr, a, tyStr, b)
		return c
	}
	if p, isPrim := t.(*types.Primitive); isPrim {
		if p.Kind == types.PrimString {
			g.declareHelper("ll_str_cmp_p", "i64", "ptr, ptr")
			r := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_str_cmp_p(ptr %s, ptr %s)", r, ptrA, ptrB)
			return r
		}
		tyStr := g.llvmType(p)
		al := g.typeAlign(p)
		a := g.newReg()
		b := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", a, tyStr, ptrA, al)
		g.indentLine(1, "%s = load %s, ptr %s, align %d", b, tyStr, ptrB, al)
		lt := g.newReg()
		gt := g.newReg()
		extLt := g.newReg()
		extGt := g.newReg()
		diff := g.newReg()
		if p.IsFloat() {
			g.indentLine(1, "%s = fcmp olt %s %s, %s", lt, tyStr, a, b)
			g.indentLine(1, "%s = fcmp ogt %s %s, %s", gt, tyStr, a, b)
		} else if p.IsUnsignedInt() {
			g.indentLine(1, "%s = icmp ult %s %s, %s", lt, tyStr, a, b)
			g.indentLine(1, "%s = icmp ugt %s %s, %s", gt, tyStr, a, b)
		} else {
			g.indentLine(1, "%s = icmp slt %s %s, %s", lt, tyStr, a, b)
			g.indentLine(1, "%s = icmp sgt %s %s, %s", gt, tyStr, a, b)
		}
		g.indentLine(1, "%s = zext i1 %s to i64", extLt, lt)
		g.indentLine(1, "%s = zext i1 %s to i64", extGt, gt)
		g.indentLine(1, "%s = sub i64 %s, %s", diff, extGt, extLt)
		return diff
	}
	if d, isDist := t.(*types.Distinct); isDist {
		return g.emitValueCmpInline(ptrA, ptrB, d.Base)
	}
	fn := "ll_cmp_p__" + t.Mangled()
	g.declareHelper(fn, "i64", "ptr, ptr")
	r := g.newReg()
	g.indentLine(1, "%s = call i64 @%s(ptr %s, ptr %s)", r, fn, ptrA, ptrB)
	return r
}

func (g *llvmEmitter) emitValueEqInline(ptrA, ptrB string, t types.Type) string {
	if _, isPtr := t.(*types.Ptr); isPtr {
		a := g.newReg()
		b := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", a, ptrA)
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", b, ptrB)
		g.indentLine(1, "%s = icmp eq ptr %s, %s", r, a, b)
		return r
	}
	if _, isRef := t.(*types.Ref); isRef {
		a := g.newReg()
		b := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", a, ptrA)
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", b, ptrB)
		g.indentLine(1, "%s = icmp eq ptr %s, %s", r, a, b)
		return r
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "eq"); custom != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		a := g.newReg()
		b := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", a, tyStr, ptrA, al)
		g.indentLine(1, "%s = load %s, ptr %s, align %d", b, tyStr, ptrB, al)
		g.indentLine(1, "%s = call i1 @%s(%s %s, %s %s)", r, custom.CName, tyStr, a, tyStr, b)
		return r
	}
	if customCmp := g.prog.Checked.LookupConcreteMethod(t, "cmp"); customCmp != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		a := g.newReg()
		b := g.newReg()
		c := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", a, tyStr, ptrA, al)
		g.indentLine(1, "%s = load %s, ptr %s, align %d", b, tyStr, ptrB, al)
		g.indentLine(1, "%s = call i64 @%s(%s %s, %s %s)", c, customCmp.CName, tyStr, a, tyStr, b)
		g.indentLine(1, "%s = icmp eq i64 %s, 0", r, c)
		return r
	}
	fn := "ll_eq_p__" + t.Mangled()
	r := g.newReg()
	g.indentLine(1, "%s = call i1 @%s(ptr %s, ptr %s)", r, fn, ptrA, ptrB)
	return r
}

func (g *llvmEmitter) emitValueHashInline(ptrVal string, t types.Type) string {
	if _, isPtr := t.(*types.Ptr); isPtr {
		v := g.newReg()
		i := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", v, ptrVal)
		g.indentLine(1, "%s = ptrtoint ptr %s to i64", i, v)
		g.indentLine(1, "%s = call i64 @ll_hash_mix64(i64 %s)", r, i)
		return r
	}
	if _, isRef := t.(*types.Ref); isRef {
		v := g.newReg()
		i := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", v, ptrVal)
		g.indentLine(1, "%s = ptrtoint ptr %s to i64", i, v)
		g.indentLine(1, "%s = call i64 @ll_hash_mix64(i64 %s)", r, i)
		return r
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "hash"); custom != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		v := g.newReg()
		r := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", v, tyStr, ptrVal, al)
		g.indentLine(1, "%s = call i64 @%s(%s %s)", r, custom.CName, tyStr, v)
		return r
	}
	fn := "ll_hash_p__" + t.Mangled()
	r := g.newReg()
	g.indentLine(1, "%s = call i64 @%s(ptr %s)", r, fn, ptrVal)
	return r
}

func (g *llvmEmitter) emitValueToStrInline(outStrPtr, valPtr string, t types.Type) {
	if _, isPtr := t.(*types.Ptr); isPtr {
		v := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", v, valPtr)
		g.indentLine(1, "call void @ll_tostr_ptr_p(ptr %s, ptr %s)", outStrPtr, v)
		return
	}
	if _, isRef := t.(*types.Ref); isRef {
		v := g.newReg()
		g.indentLine(1, "%s = load ptr, ptr %s, align 8", v, valPtr)
		g.indentLine(1, "call void @ll_tostr_ptr_p(ptr %s, ptr %s)", outStrPtr, v)
		return
	}
	if custom := g.prog.Checked.LookupConcreteMethod(t, "to_string"); custom != nil {
		tyStr := g.llvmType(t)
		al := g.typeAlign(t)
		v := g.newReg()
		s := g.newReg()
		g.indentLine(1, "%s = load %s, ptr %s, align %d", v, tyStr, valPtr, al)
		g.indentLine(1, "%s = call %%struct.ll_string @%s(%s %s)", s, custom.CName, tyStr, v)
		g.indentLine(1, "store %%struct.ll_string %s, ptr %s, align 8", s, outStrPtr)
		return
	}
	fn := "ll_tostr_p__" + t.Mangled()
	g.indentLine(1, "call void @%s(ptr %s, ptr %s)", fn, outStrPtr, valPtr)
}

func (g *llvmEmitter) emitPushLitToStrInline(outStrPtr, lit string) {
	if len(lit) == 0 {
		return
	}
	sym := g.internString(lit).symbol
	g.indentLine(1, "call void @ll_str_push_bytes(ptr %s, ptr %s, i64 %d)", outStrPtr, sym, len(lit))
}

func (g *llvmEmitter) emitAppendValueToStrInline(outStrPtr, valPtr string, t types.Type) {
	tmp := g.newReg()
	g.indentLine(1, "%s = alloca %%struct.ll_string, align 8", tmp)
	g.emitValueToStrInline(tmp, valPtr, t)
	g.indentLine(1, "call void @ll_str_push_p(ptr %s, ptr %s)", outStrPtr, tmp)
	g.indentLine(1, "call void @ll_drop__string(ptr %s)", tmp)
}

func (g *llvmEmitter) emitPrimitiveHelpersLLVM() {
	ptrFmtSym := g.internString("%p").symbol
	nullPtrSym := g.internString("0x0").symbol
	emptySym := g.internString("").symbol

	g.funcSet["ll_tostr_ptr_p"] = true
	g.funcSet["ll_tostr__string"] = true
	g.funcSet["ll_tostr_p__string"] = true
	g.funcSet["ll_eq__string"] = true
	g.funcSet["ll_eq_p__string"] = true
	g.funcSet["ll_cmp_p__string"] = true
	g.funcSet["ll_hash__string"] = true
	g.funcSet["ll_hash_p__string"] = true

	g.line(`define internal void @ll_tostr_ptr_p(ptr %%out, ptr %%val) {
entry:
    %%is_null = icmp eq ptr %%val, null
    br i1 %%is_null, label %%store_null, label %%fmt_ptr
store_null:
    %%n0 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 0
    %%n1 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 1
    %%n2 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 2
    store ptr %s, ptr %%n0, align 8
    store i64 3, ptr %%n1, align 8
    store i64 0, ptr %%n2, align 8
    ret void
fmt_ptr:
    %%buf = alloca [32 x i8], align 1
    %%n32 = call i32 (ptr, i64, ptr, ...) @snprintf(ptr %%buf, i64 32, ptr %s, ptr %%val)
    %%n64 = sext i32 %%n32 to i64
    store %%struct.ll_string zeroinitializer, ptr %%out, align 8
    call void @ll_str_push_bytes(ptr %%out, ptr %%buf, i64 %%n64)
    ret void
}

define internal %%struct.ll_string @ll_tostr__string(%%struct.ll_string %%s) {
entry:
    %%r = call %%struct.ll_string @ll_copy__string(%%struct.ll_string %%s)
    ret %%struct.ll_string %%r
}

define internal void @ll_tostr_p__string(ptr %%out, ptr %%s) {
entry:
    call void @ll_copy_p__string(ptr %%out, ptr %%s)
    ret void
}

define internal i1 @ll_eq__string(%%struct.ll_string %%a, %%struct.ll_string %%b) {
entry:
    %%pa = alloca %%struct.ll_string, align 8
    %%pb = alloca %%struct.ll_string, align 8
    store %%struct.ll_string %%a, ptr %%pa, align 8
    store %%struct.ll_string %%b, ptr %%pb, align 8
    %%r = call i1 @ll_str_eq_p(ptr %%pa, ptr %%pb)
    ret i1 %%r
}

define internal i1 @ll_eq_p__string(ptr %%a, ptr %%b) {
entry:
    %%r = call i1 @ll_str_eq_p(ptr %%a, ptr %%b)
    ret i1 %%r
}

define internal i64 @ll_cmp_p__string(ptr %%a, ptr %%b) {
entry:
    %%r = call i64 @ll_str_cmp_p(ptr %%a, ptr %%b)
    ret i64 %%r
}

define internal i64 @ll_hash__string(%%struct.ll_string %%s) {
entry:
    %%ps = alloca %%struct.ll_string, align 8
    store %%struct.ll_string %%s, ptr %%ps, align 8
    %%r = call i64 @ll_str_hash_p(ptr %%ps)
    ret i64 %%r
}

define internal i64 @ll_hash_p__string(ptr %%s) {
entry:
    %%r = call i64 @ll_str_hash_p(ptr %%s)
    ret i64 %%r
}
`, nullPtrSym, ptrFmtSym)

	type primSpec struct {
		mangled  string
		ty       types.Type
		llvmTy   string
		align    int
		isSigned bool
		isFloat  bool
	}
	prims := []primSpec{
		{"i8", types.TypeI8, "i8", 1, true, false},
		{"i16", types.TypeI16, "i16", 2, true, false},
		{"i32", types.TypeI32, "i32", 4, true, false},
		{"i64", types.TypeInt, "i64", 8, true, false},
		{"u8", types.TypeU8, "i8", 1, false, false},
		{"u16", types.TypeU16, "i16", 2, false, false},
		{"u32", types.TypeU32, "i32", 4, false, false},
		{"u64", types.TypeU64, "i64", 8, false, false},
		{"bool", types.TypeBool, "i1", 1, false, false},
		{"char", types.TypeChar, "i32", 4, false, false},
		{"f32", types.TypeF32, "float", 4, false, true},
		{"f64", types.TypeFloat, "double", 8, false, true},
	}

	for _, p := range prims {
		for _, prefix := range []string{"ll_drop__", "ll_copy__", "ll_copy_p__", "ll_eq__", "ll_eq_p__", "ll_hash__", "ll_hash_p__", "ll_tostr__", "ll_tostr_p__"} {
			g.funcSet[prefix+p.mangled] = true
		}

		g.line("define internal void @ll_drop__%s(ptr %%p) { ret void }", p.mangled)
		g.line("define internal %s @ll_copy__%s(%s %%v) { ret %s %%v }", p.llvmTy, p.mangled, p.llvmTy, p.llvmTy)
		g.line(`define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {
entry:
    %%v = load %s, ptr %%src, align %d
    store %s %%v, ptr %%dst, align %d
    ret void
}`, p.mangled, p.llvmTy, p.align, p.llvmTy, p.align)

		if p.isFloat {
			g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%eq = fcmp oeq %s %%a, %%b
    %%na = fcmp uno %s %%a, %%a
    %%nb = fcmp uno %s %%b, %%b
    %%both_nan = and i1 %%na, %%nb
    %%res = or i1 %%eq, %%both_nan
    ret i1 %%res
}`, p.mangled, p.llvmTy, p.llvmTy, p.llvmTy, p.llvmTy, p.llvmTy)
		} else {
			g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%res = icmp eq %s %%a, %%b
    ret i1 %%res
}`, p.mangled, p.llvmTy, p.llvmTy, p.llvmTy)
		}

		g.line(`define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {
entry:
    %%va = load %s, ptr %%a, align %d
    %%vb = load %s, ptr %%b, align %d
    %%res = call i1 @ll_eq__%s(%s %%va, %s %%vb)
    ret i1 %%res
}`, p.mangled, p.llvmTy, p.align, p.llvmTy, p.align, p.mangled, p.llvmTy, p.llvmTy)

		if p.isFloat {
			if p.mangled == "f32" {
				g.line(`define internal i64 @ll_hash__f32(float %%v) {
entry:
    %%is_zero = fcmp oeq float %%v, 0.0
    %%is_nan = fcmp uno float %%v, %%v
    %%bits32 = bitcast float %%v to i32
    %%bits64 = zext i32 %%bits32 to i64
    %%s1 = select i1 %%is_nan, i64 2143289344, i64 %%bits64
    %%s2 = select i1 %%is_zero, i64 0, i64 %%s1
    %%h = call i64 @ll_hash_mix64(i64 %%s2)
    ret i64 %%h
}`)
			} else {
				g.line(`define internal i64 @ll_hash__f64(double %%v) {
entry:
    %%is_zero = fcmp oeq double %%v, 0.0
    %%is_nan = fcmp uno double %%v, %%v
    %%bits64 = bitcast double %%v to i64
    %%s1 = select i1 %%is_nan, i64 9221120237041090560, i64 %%bits64
    %%s2 = select i1 %%is_zero, i64 0, i64 %%s1
    %%h = call i64 @ll_hash_mix64(i64 %%s2)
    ret i64 %%h
}`)
			}
		} else if p.llvmTy == "i64" {
			g.line(`define internal i64 @ll_hash__%s(i64 %%v) {
entry:
    %%h = call i64 @ll_hash_mix64(i64 %%v)
    ret i64 %%h
}`, p.mangled)
		} else {
			castOp := "zext"
			if p.isSigned {
				castOp = "sext"
			}
			g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%v64 = %s %s %%v to i64
    %%h = call i64 @ll_hash_mix64(i64 %%v64)
    ret i64 %%h
}`, p.mangled, p.llvmTy, castOp, p.llvmTy)
		}

		g.line(`define internal i64 @ll_hash_p__%s(ptr %%p) {
entry:
    %%v = load %s, ptr %%p, align %d
    %%h = call i64 @ll_hash__%s(%s %%v)
    ret i64 %%h
}`, p.mangled, p.llvmTy, p.align, p.mangled, p.llvmTy)

		var toStrM *sema.FuncInstance
		if g.prog.Checked != nil {
			toStrM = g.prog.Checked.LookupConcreteMethod(p.ty, "to_string")
		}
		if toStrM != nil {
			g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%s = call %%struct.ll_string @%s(%s %%v)
    ret %%struct.ll_string %%s
}
define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {
entry:
    %%v = load %s, ptr %%p, align %d
    %%s = call %%struct.ll_string @%s(%s %%v)
    store %%struct.ll_string %%s, ptr %%out, align 8
    ret void
}`, p.mangled, p.llvmTy, toStrM.CName, p.llvmTy, p.mangled, p.llvmTy, p.align, toStrM.CName, p.llvmTy)
		} else {
			g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    ret %%struct.ll_string zeroinitializer
}
define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {
entry:
    %%p0 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 0
    %%p1 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 1
    %%p2 = getelementptr inbounds %%struct.ll_string, ptr %%out, i32 0, i32 2
    store ptr %s, ptr %%p0, align 8
    store i64 0, ptr %%p1, align 8
    store i64 0, ptr %%p2, align 8
    ret void
}`, p.mangled, p.llvmTy, p.mangled, emptySym)
		}
	}
}

func (g *llvmEmitter) emitSimpleEnumHelpersLLVM(en *types.Enum) {
	mangled := en.Mangled()
	tyStr := g.llvmType(en)
	al := g.typeAlign(en)

	for _, prefix := range []string{"ll_drop__", "ll_copy__", "ll_copy_p__", "ll_eq__", "ll_eq_p__", "ll_hash__", "ll_hash_p__", "ll_tostr__", "ll_tostr_p__"} {
		g.funcSet[prefix+mangled] = true
	}

	g.line("define internal void @ll_drop__%s(ptr %%p) { ret void }", mangled)
	g.line("define internal %s @ll_copy__%s(%s %%v) { ret %s %%v }", tyStr, mangled, tyStr, tyStr)
	g.line(`define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {
entry:
    %%v = load %s, ptr %%src, align %d
    store %s %%v, ptr %%dst, align %d
    ret void
}
define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%r = icmp eq %s %%a, %%b
    ret i1 %%r
}
define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {
entry:
    %%va = load %s, ptr %%a, align %d
    %%vb = load %s, ptr %%b, align %d
    %%r = icmp eq %s %%va, %%vb
    ret i1 %%r
}`, mangled, tyStr, al, tyStr, al, mangled, tyStr, tyStr, tyStr, mangled, tyStr, al, tyStr, al, tyStr)

	extOp := "sext"
	if g.isUnsigned(en.Underlying) {
		extOp = "zext"
	}
	if tyStr == "i64" {
		g.line(`define internal i64 @ll_hash__%s(i64 %%v) {
entry:
    %%h = call i64 @ll_hash_mix64(i64 %%v)
    ret i64 %%h
}`, mangled)
	} else {
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%v64 = %s %s %%v to i64
    %%h = call i64 @ll_hash_mix64(i64 %%v64)
    ret i64 %%h
}`, mangled, tyStr, extOp, tyStr)
	}
	g.line(`define internal i64 @ll_hash_p__%s(ptr %%p) {
entry:
    %%v = load %s, ptr %%p, align %d
    %%h = call i64 @ll_hash__%s(%s %%v)
    ret i64 %%h
}`, mangled, tyStr, al, mangled, tyStr)

	g.nextRegID = 0
	g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
	g.line("entry:")
	if custom := g.prog.Checked.LookupConcreteMethod(en, "to_string"); custom != nil {
		g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
		g.indentLine(1, "%%s = call %%struct.ll_string @%s(%s %%v)", custom.CName, tyStr)
		g.indentLine(1, "store %%struct.ll_string %%s, ptr %%out, align 8")
		g.indentLine(1, "ret void")
	} else {
		g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
		for i, vr := range en.Variants {
			chkLbl := fmt.Sprintf("chk.%d", i)
			hitLbl := fmt.Sprintf("hit.%d", i)
			nxtChk := "def_label"
			if i+1 < len(en.Variants) {
				nxtChk = fmt.Sprintf("chk.%d", i+1)
			}
			if i == 0 {
				g.indentLine(1, "br label %%%s", chkLbl)
			}
			g.line("%s:", chkLbl)
			g.indentLine(1, "%%c.%d = icmp eq %s %%v, %d", i, tyStr, vr.Value)
			g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
			g.line("%s:", hitLbl)
			label := en.Name + "." + vr.Name
			sym := g.internString(label).symbol
			g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
			g.indentLine(1, "call void @ll_str_push_bytes(ptr %%out, ptr %s, i64 %d)", sym, len(label))
			g.indentLine(1, "ret void")
		}
		g.line("def_label:")
		defLabel := en.Name + "(?)"
		defSym := g.internString(defLabel).symbol
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.indentLine(1, "call void @ll_str_push_bytes(ptr %%out, ptr %s, i64 %d)", defSym, len(defLabel))
		g.indentLine(1, "ret void")
	}
	g.line("}")
	g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
}

func (g *llvmEmitter) emitDistinctHelpersLLVM(d *types.Distinct) {
	mangled := d.Mangled()
	baseMangled := d.Base.Mangled()
	tyStr := g.llvmType(d)
	al := g.typeAlign(d)

	g.funcSet["ll_drop__"+mangled] = true
	g.line("define internal void @ll_drop__%s(ptr %%p) {", mangled)
	g.line("entry:")
	if customDrop := g.prog.Checked.LookupConcreteMethod(d, "drop"); customDrop != nil {
		g.indentLine(1, "call void @%s(ptr %%p)", customDrop.CName)
	}
	if !d.Base.IsPlain() {
		g.emitValueDropInline("%p", d.Base)
	}
	g.indentLine(1, "ret void")
	g.line("}")

	if CanCopyType(g.prog, d) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		if customCopy := g.prog.Checked.LookupConcreteMethod(d, "copy"); customCopy != nil {
			g.indentLine(1, "%%v = load %s, ptr %%src, align %d", tyStr, al)
			g.indentLine(1, "%%r = call %s @%s(%s %%v)", tyStr, customCopy.CName, tyStr)
			g.indentLine(1, "store %s %%r, ptr %%dst, align %d", tyStr, al)
		} else {
			g.emitValueCopyInline("%dst", "%src", d.Base)
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	if CanEqType(g.prog, d) {
		g.funcSet["ll_eq_p__"+mangled] = true
		g.funcSet["ll_eq__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(d, "eq"); custom != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%r = call i1 @%s(%s %%va, %s %%vb)", custom.CName, tyStr, tyStr)
			g.indentLine(1, "ret i1 %%r")
		} else if customCmp := g.prog.Checked.LookupConcreteMethod(d, "cmp"); customCmp != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%c = call i64 @%s(%s %%va, %s %%vb)", customCmp.CName, tyStr, tyStr)
			g.indentLine(1, "%%r = icmp eq i64 %%c, 0")
			g.indentLine(1, "ret i1 %%r")
		} else {
			r := g.emitValueEqInline("%a", "%b", d.Base)
			g.indentLine(1, "ret i1 %s", r)
		}
		g.line("}")
		g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i1 @ll_eq_p__%s(ptr %%pa, ptr %%pb)
    ret i1 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	if CanCmpType(g.prog, d) {
		g.funcSet["ll_cmp_p__"+mangled] = true
		g.funcSet["ll_cmp__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_cmp_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if customCmp := g.prog.Checked.LookupConcreteMethod(d, "cmp"); customCmp != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%c = call i64 @%s(%s %%va, %s %%vb)", customCmp.CName, tyStr, tyStr)
			g.indentLine(1, "ret i64 %%c")
		} else {
			r := g.emitValueCmpInline("%a", "%b", d.Base)
			g.indentLine(1, "ret i64 %s", r)
		}
		g.line("}")
		g.line(`define internal i64 @ll_cmp__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i64 @ll_cmp_p__%s(ptr %%pa, ptr %%pb)
    ret i64 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	if CanHashType(g.prog, d) {
		g.funcSet["ll_hash_p__"+mangled] = true
		g.funcSet["ll_hash__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_hash_p__%s(ptr %%p) {", mangled)
		g.line("entry:")
		if customHash := g.prog.Checked.LookupConcreteMethod(d, "hash"); customHash != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%h = call i64 @%s(%s %%v)", customHash.CName, tyStr)
			g.indentLine(1, "ret i64 %%h")
		} else {
			h := g.emitValueHashInline("%p", d.Base)
			g.indentLine(1, "ret i64 %s", h)
		}
		g.line("}")
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    store %s %%v, ptr %%p, align %d
    %%h = call i64 @ll_hash_p__%s(ptr %%p)
    ret i64 %%h
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	if CanToStringType(g.prog, d) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
		g.line("entry:")
		if customToStr := g.prog.Checked.LookupConcreteMethod(d, "to_string"); customToStr != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%s = call %%struct.ll_string @%s(%s %%v)", customToStr.CName, tyStr)
			g.indentLine(1, "store %%struct.ll_string %%s, ptr %%out, align 8")
		} else {
			g.emitValueToStrInline("%out", "%p", d.Base)
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}
	_ = baseMangled
}

func (g *llvmEmitter) emitStructHelpersLLVM(st *types.Struct) {
	mangled := st.Mangled()
	tyStr := g.llvmType(st)
	al := g.typeAlign(st)

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%p) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%p, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%do_drop")
	g.line("do_drop:")
	if st.HasCustomDrop {
		if dropM := g.prog.Checked.LookupConcreteMethod(st, "drop"); dropM != nil {
			g.indentLine(1, "call void @%s(ptr %%p)", dropM.CName)
		}
	}
	for i := len(st.Fields) - 1; i >= 0; i-- {
		f := st.Fields[i]
		if !f.Type.IsPlain() {
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
			g.emitValueDropInline(fp, f.Type)
		}
	}
	g.indentLine(1, "store %s zeroinitializer, ptr %%p, align %d", tyStr, al)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, st) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(st, "copy"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%src, align %d", tyStr, al)
			g.indentLine(1, "%%r = call %s @%s(%s %%v)", tyStr, custom.CName, tyStr)
			g.indentLine(1, "store %s %%r, ptr %%dst, align %d", tyStr, al)
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align %d", tyStr, al)
			for i, f := range st.Fields {
				sf := g.newReg()
				df := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%src, i32 0, i32 %d", sf, tyStr, i)
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%dst, i32 0, i32 %d", df, tyStr, i)
				g.emitValueCopyInline(df, sf, f.Type)
			}
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	// 3. Eq
	if CanEqType(g.prog, st) {
		g.funcSet["ll_eq_p__"+mangled] = true
		g.funcSet["ll_eq__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(st, "eq"); custom != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%r = call i1 @%s(%s %%va, %s %%vb)", custom.CName, tyStr, tyStr)
			g.indentLine(1, "ret i1 %%r")
		} else if customCmp := g.prog.Checked.LookupConcreteMethod(st, "cmp"); customCmp != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%c = call i64 @%s(%s %%va, %s %%vb)", customCmp.CName, tyStr, tyStr)
			g.indentLine(1, "%%r = icmp eq i64 %%c, 0")
			g.indentLine(1, "ret i1 %%r")
		} else if len(st.Fields) == 0 {
			g.indentLine(1, "ret i1 true")
		} else {
			for i, f := range st.Fields {
				fa := g.newReg()
				fb := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%a, i32 0, i32 %d", fa, tyStr, i)
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%b, i32 0, i32 %d", fb, tyStr, i)
				eq := g.emitValueEqInline(fa, fb, f.Type)
				nxtLbl := fmt.Sprintf("eq.next.%d", i)
				g.indentLine(1, "br i1 %s, label %%%s, label %%ret_false", eq, nxtLbl)
				g.line("%s:", nxtLbl)
			}
			g.indentLine(1, "ret i1 true")
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		}
		g.line("}")
		g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i1 @ll_eq_p__%s(ptr %%pa, ptr %%pb)
    ret i1 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	// 4. Hash
	if CanHashType(g.prog, st) {
		g.funcSet["ll_hash_p__"+mangled] = true
		g.funcSet["ll_hash__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_hash_p__%s(ptr %%p) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(st, "hash"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%h = call i64 @%s(%s %%v)", custom.CName, tyStr)
			g.indentLine(1, "ret i64 %%h")
		} else {
			curH := "1469598103934665603" // 0xcbf29ce484222325 (int64) = -3750763034362895579
			curH = "-3750763034362895579"
			for i, f := range st.Fields {
				fp := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
				fh := g.emitValueHashInline(fp, f.Type)
				comb := g.newReg()
				g.indentLine(1, "%s = call i64 @ll_hash_combine(i64 %s, i64 %s)", comb, curH, fh)
				curH = comb
			}
			g.indentLine(1, "ret i64 %s", curH)
		}
		g.line("}")
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    store %s %%v, ptr %%p, align %d
    %%h = call i64 @ll_hash_p__%s(ptr %%p)
    ret i64 %%h
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	// 5. ToString
	if CanToStringType(g.prog, st) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(st, "to_string"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%s = call %%struct.ll_string @%s(%s %%v)", custom.CName, tyStr)
			g.indentLine(1, "store %%struct.ll_string %%s, ptr %%out, align 8")
		} else {
			g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
			g.emitPushLitToStrInline("%out", st.Name+"[")
			for i, f := range st.Fields {
				if i > 0 {
					g.emitPushLitToStrInline("%out", ", ")
				}
				g.emitPushLitToStrInline("%out", f.Name+": ")
				fp := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
				g.emitAppendValueToStrInline("%out", fp, f.Type)
			}
			g.emitPushLitToStrInline("%out", "]")
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}
}

func (g *llvmEmitter) emitTaggedEnumHelpersLLVM(en *types.Enum) {
	mangled := en.Mangled()
	tyStr := g.llvmType(en)
	al := g.typeAlign(en)

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%p) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%p, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%do_drop")
	g.line("do_drop:")
	if customDrop := g.prog.Checked.LookupConcreteMethod(en, "drop"); customDrop != nil {
		g.indentLine(1, "call void @%s(ptr %%p)", customDrop.CName)
	}
	g.indentLine(1, "%%p_tag = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%tag = load i32, ptr %%p_tag, align 4")
	for i, vr := range en.Variants {
		chkLbl := fmt.Sprintf("drop.chk.%d", i)
		hitLbl := fmt.Sprintf("drop.hit.%d", i)
		nxtChk := "zero_out"
		if i+1 < len(en.Variants) {
			nxtChk = fmt.Sprintf("drop.chk.%d", i+1)
		}
		if i == 0 {
			g.indentLine(1, "br label %%%s", chkLbl)
		}
		g.line("%s:", chkLbl)
		g.indentLine(1, "%%c.%d = icmp eq i32 %%tag, %d", i, vr.Value)
		g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
		g.line("%s:", hitLbl)
		if vr.Kind == types.VariantTuple && !vr.Payload.IsPlain() {
			g.emitValueDropInline("%p_data", vr.Payload)
		}
		g.indentLine(1, "br label %%zero_out")
	}
	g.line("zero_out:")
	g.indentLine(1, "store %s zeroinitializer, ptr %%p, align %d", tyStr, al)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, en) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(en, "copy"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%src, align %d", tyStr, al)
			g.indentLine(1, "%%r = call %s @%s(%s %%v)", tyStr, custom.CName, tyStr)
			g.indentLine(1, "store %s %%r, ptr %%dst, align %d", tyStr, al)
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align %d", tyStr, al)
			g.indentLine(1, "%%s_tag_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%d_tag_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%s_data = getelementptr inbounds %s, ptr %%src, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%d_data = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%tag = load i32, ptr %%s_tag_p, align 4")
			g.indentLine(1, "store i32 %%tag, ptr %%d_tag_p, align 4")
			for i, vr := range en.Variants {
				chkLbl := fmt.Sprintf("copy.chk.%d", i)
				hitLbl := fmt.Sprintf("copy.hit.%d", i)
				nxtChk := "copy.done"
				if i+1 < len(en.Variants) {
					nxtChk = fmt.Sprintf("copy.chk.%d", i+1)
				}
				if i == 0 {
					g.indentLine(1, "br label %%%s", chkLbl)
				}
				g.line("%s:", chkLbl)
				g.indentLine(1, "%%c.%d = icmp eq i32 %%tag, %d", i, vr.Value)
				g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
				g.line("%s:", hitLbl)
				if vr.Kind == types.VariantTuple {
					g.emitValueCopyInline("%d_data", "%s_data", vr.Payload)
				}
				g.indentLine(1, "br label %%copy.done")
			}
			g.line("copy.done:")
			g.indentLine(1, "ret void")
		}
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	// 3. Eq
	if CanEqType(g.prog, en) {
		g.funcSet["ll_eq_p__"+mangled] = true
		g.funcSet["ll_eq__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(en, "eq"); custom != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%r = call i1 @%s(%s %%va, %s %%vb)", custom.CName, tyStr, tyStr)
			g.indentLine(1, "ret i1 %%r")
		} else if customCmp := g.prog.Checked.LookupConcreteMethod(en, "cmp"); customCmp != nil {
			g.indentLine(1, "%%va = load %s, ptr %%a, align %d", tyStr, al)
			g.indentLine(1, "%%vb = load %s, ptr %%b, align %d", tyStr, al)
			g.indentLine(1, "%%c = call i64 @%s(%s %%va, %s %%vb)", customCmp.CName, tyStr, tyStr)
			g.indentLine(1, "%%r = icmp eq i64 %%c, 0")
			g.indentLine(1, "ret i1 %%r")
		} else {
			g.indentLine(1, "%%ta_p = getelementptr inbounds %s, ptr %%a, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%tb_p = getelementptr inbounds %s, ptr %%b, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%da = getelementptr inbounds %s, ptr %%a, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%db = getelementptr inbounds %s, ptr %%b, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%ta = load i32, ptr %%ta_p, align 4")
			g.indentLine(1, "%%tb = load i32, ptr %%tb_p, align 4")
			g.indentLine(1, "%%t_eq = icmp eq i32 %%ta, %%tb")
			g.indentLine(1, "br i1 %%t_eq, label %%chk_variants, label %%ret_false")
			g.line("chk_variants:")
			for i, vr := range en.Variants {
				chkLbl := fmt.Sprintf("eq.chk.%d", i)
				hitLbl := fmt.Sprintf("eq.hit.%d", i)
				nxtChk := "ret_false"
				if i+1 < len(en.Variants) {
					nxtChk = fmt.Sprintf("eq.chk.%d", i+1)
				}
				if i == 0 {
					g.indentLine(1, "br label %%%s", chkLbl)
				}
				g.line("%s:", chkLbl)
				g.indentLine(1, "%%c.%d = icmp eq i32 %%ta, %d", i, vr.Value)
				g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
				g.line("%s:", hitLbl)
				switch vr.Kind {
				case types.VariantUnit:
					g.indentLine(1, "ret i1 true")
				case types.VariantTuple:
					eq := g.emitValueEqInline("%da", "%db", vr.Payload)
					g.indentLine(1, "ret i1 %s", eq)
				}
			}
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		}
		g.line("}")
		g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i1 @ll_eq_p__%s(ptr %%pa, ptr %%pb)
    ret i1 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	// 4. Hash
	if CanHashType(g.prog, en) {
		g.funcSet["ll_hash_p__"+mangled] = true
		g.funcSet["ll_hash__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_hash_p__%s(ptr %%p) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(en, "hash"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%h = call i64 @%s(%s %%v)", custom.CName, tyStr)
			g.indentLine(1, "ret i64 %%h")
		} else {
			g.indentLine(1, "%%p_tag = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%tag = load i32, ptr %%p_tag, align 4")
			g.indentLine(1, "%%tag64 = sext i32 %%tag to i64")
			g.indentLine(1, "%%h0 = call i64 @ll_hash_mix64(i64 %%tag64)")
			for i, vr := range en.Variants {
				chkLbl := fmt.Sprintf("hash.chk.%d", i)
				hitLbl := fmt.Sprintf("hash.hit.%d", i)
				nxtChk := "hash.done"
				if i+1 < len(en.Variants) {
					nxtChk = fmt.Sprintf("hash.chk.%d", i+1)
				}
				if i == 0 {
					g.indentLine(1, "br label %%%s", chkLbl)
				}
				g.line("%s:", chkLbl)
				g.indentLine(1, "%%c.%d = icmp eq i32 %%tag, %d", i, vr.Value)
				g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
				g.line("%s:", hitLbl)
				curH := "%h0"
				if vr.Kind == types.VariantTuple {
					ph := g.emitValueHashInline("%p_data", vr.Payload)
					comb := g.newReg()
					g.indentLine(1, "%s = call i64 @ll_hash_combine(i64 %s, i64 %s)", comb, curH, ph)
					curH = comb
				}
				g.indentLine(1, "ret i64 %s", curH)
			}
			g.line("hash.done:")
			g.indentLine(1, "ret i64 %%h0")
		}
		g.line("}")
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    store %s %%v, ptr %%p, align %d
    %%h = call i64 @ll_hash_p__%s(ptr %%p)
    ret i64 %%h
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	// 5. ToString
	if CanToStringType(g.prog, en) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
		g.line("entry:")
		if custom := g.prog.Checked.LookupConcreteMethod(en, "to_string"); custom != nil {
			g.indentLine(1, "%%v = load %s, ptr %%p, align %d", tyStr, al)
			g.indentLine(1, "%%s = call %%struct.ll_string @%s(%s %%v)", custom.CName, tyStr)
			g.indentLine(1, "store %%struct.ll_string %%s, ptr %%out, align 8")
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
			g.indentLine(1, "%%p_tag = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%tag = load i32, ptr %%p_tag, align 4")
			for i, vr := range en.Variants {
				chkLbl := fmt.Sprintf("ts.chk.%d", i)
				hitLbl := fmt.Sprintf("ts.hit.%d", i)
				nxtChk := "ts.default"
				if i+1 < len(en.Variants) {
					nxtChk = fmt.Sprintf("ts.chk.%d", i+1)
				}
				if i == 0 {
					g.indentLine(1, "br label %%%s", chkLbl)
				}
				g.line("%s:", chkLbl)
				g.indentLine(1, "%%c.%d = icmp eq i32 %%tag, %d", i, vr.Value)
				g.indentLine(1, "br i1 %%c.%d, label %%%s, label %%%s", i, hitLbl, nxtChk)
				g.line("%s:", hitLbl)
				switch vr.Kind {
				case types.VariantUnit:
					g.emitPushLitToStrInline("%out", en.Name+"."+vr.Name)
				case types.VariantTuple:
					g.emitPushLitToStrInline("%out", en.Name+"."+vr.Name+"(")
					g.emitAppendValueToStrInline("%out", "%p_data", vr.Payload)
					g.emitPushLitToStrInline("%out", ")")
				}
				g.indentLine(1, "ret void")
			}
			g.line("ts.default:")
			g.emitPushLitToStrInline("%out", en.Name+".?")
			g.indentLine(1, "ret void")
		}
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}
}

func (g *llvmEmitter) emitOptionalHelpersLLVM(opt *types.Optional) {
	mangled := opt.Mangled()
	tyStr := g.llvmType(opt)
	al := g.typeAlign(opt)
	elem := opt.Elem
	boxed := opt.IsBoxed()

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%p) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%p, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%chk")
	g.line("chk:")
	if boxed {
		g.indentLine(1, "%%box = load ptr, ptr %%p, align 8")
		g.indentLine(1, "%%has = icmp ne ptr %%box, null")
		g.indentLine(1, "br i1 %%has, label %%do_drop, label %%done")
		g.line("do_drop:")
		if !elem.IsPlain() {
			g.emitValueDropInline("%box", elem)
		}
		g.indentLine(1, "call void @ll_free(ptr %%box)")
		g.indentLine(1, "store ptr null, ptr %%p, align 8")
		g.indentLine(1, "br label %%done")
	} else {
		g.indentLine(1, "%%p_has = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%has = load i1, ptr %%p_has, align 1")
		g.indentLine(1, "br i1 %%has, label %%do_drop, label %%zero")
		g.line("do_drop:")
		if !elem.IsPlain() {
			g.indentLine(1, "%%p_val = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
			g.emitValueDropInline("%p_val", elem)
		}
		g.indentLine(1, "br label %%zero")
		g.line("zero:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%p, align %d", tyStr, al)
		g.indentLine(1, "br label %%done")
	}
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, opt) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		if boxed {
			g.indentLine(1, "%%box = load ptr, ptr %%src, align 8")
			g.indentLine(1, "%%has = icmp ne ptr %%box, null")
			g.indentLine(1, "br i1 %%has, label %%some, label %%none")
			g.line("some:")
			g.indentLine(1, "%%nb = call ptr @ll_alloc(i64 %d)", g.typeSize(elem))
			g.emitValueCopyInline("%nb", "%box", elem)
			g.indentLine(1, "store ptr %%nb, ptr %%dst, align 8")
			g.indentLine(1, "ret void")
			g.line("none:")
			g.indentLine(1, "store ptr null, ptr %%dst, align 8")
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align %d", tyStr, al)
			g.indentLine(1, "%%s_has_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%has = load i1, ptr %%s_has_p, align 1")
			g.indentLine(1, "br i1 %%has, label %%some, label %%done")
			g.line("some:")
			g.indentLine(1, "%%d_has_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%s_val_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%d_val_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1", tyStr)
			g.indentLine(1, "store i1 true, ptr %%d_has_p, align 1")
			g.emitValueCopyInline("%d_val_p", "%s_val_p", elem)
			g.indentLine(1, "br label %%done")
			g.line("done:")
			g.indentLine(1, "ret void")
		}
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	// 3. Eq
	if CanEqType(g.prog, opt) {
		g.funcSet["ll_eq_p__"+mangled] = true
		g.funcSet["ll_eq__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if boxed {
			g.indentLine(1, "%%ba = load ptr, ptr %%a, align 8")
			g.indentLine(1, "%%bb = load ptr, ptr %%b, align 8")
			g.indentLine(1, "%%ha = icmp ne ptr %%ba, null")
			g.indentLine(1, "%%hb = icmp ne ptr %%bb, null")
			g.indentLine(1, "%%same_has = icmp eq i1 %%ha, %%hb")
			g.indentLine(1, "br i1 %%same_has, label %%chk_some, label %%ret_false")
			g.line("chk_some:")
			g.indentLine(1, "br i1 %%ha, label %%cmp_val, label %%ret_true")
			g.line("cmp_val:")
			eq := g.emitValueEqInline("%ba", "%bb", elem)
			g.indentLine(1, "ret i1 %s", eq)
			g.line("ret_true:")
			g.indentLine(1, "ret i1 true")
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		} else {
			g.indentLine(1, "%%pa_has = getelementptr inbounds %s, ptr %%a, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%pb_has = getelementptr inbounds %s, ptr %%b, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%ha = load i1, ptr %%pa_has, align 1")
			g.indentLine(1, "%%hb = load i1, ptr %%pb_has, align 1")
			g.indentLine(1, "%%same_has = icmp eq i1 %%ha, %%hb")
			g.indentLine(1, "br i1 %%same_has, label %%chk_some, label %%ret_false")
			g.line("chk_some:")
			g.indentLine(1, "br i1 %%ha, label %%cmp_val, label %%ret_true")
			g.line("cmp_val:")
			g.indentLine(1, "%%pa_val = getelementptr inbounds %s, ptr %%a, i32 0, i32 1", tyStr)
			g.indentLine(1, "%%pb_val = getelementptr inbounds %s, ptr %%b, i32 0, i32 1", tyStr)
			eq := g.emitValueEqInline("%pa_val", "%pb_val", elem)
			g.indentLine(1, "ret i1 %s", eq)
			g.line("ret_true:")
			g.indentLine(1, "ret i1 true")
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		}
		g.line("}")
		g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i1 @ll_eq_p__%s(ptr %%pa, ptr %%pb)
    ret i1 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	// 4. Hash
	if CanHashType(g.prog, opt) {
		g.funcSet["ll_hash_p__"+mangled] = true
		g.funcSet["ll_hash__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_hash_p__%s(ptr %%p) {", mangled)
		g.line("entry:")
		if boxed {
			g.indentLine(1, "%%box = load ptr, ptr %%p, align 8")
			g.indentLine(1, "%%has = icmp ne ptr %%box, null")
			g.indentLine(1, "br i1 %%has, label %%some, label %%none")
			g.line("some:")
			eh := g.emitValueHashInline("%box", elem)
			g.indentLine(1, "%%h = call i64 @ll_hash_combine(i64 1, i64 %s)", eh)
			g.indentLine(1, "ret i64 %%h")
			g.line("none:")
			g.indentLine(1, "ret i64 -7046029254386353131")
		} else {
			g.indentLine(1, "%%p_has = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%has = load i1, ptr %%p_has, align 1")
			g.indentLine(1, "br i1 %%has, label %%some, label %%none")
			g.line("some:")
			g.indentLine(1, "%%p_val = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
			eh := g.emitValueHashInline("%p_val", elem)
			g.indentLine(1, "%%h = call i64 @ll_hash_combine(i64 1, i64 %s)", eh)
			g.indentLine(1, "ret i64 %%h")
			g.line("none:")
			g.indentLine(1, "ret i64 -7046029254386353131")
		}
		g.line("}")
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    store %s %%v, ptr %%p, align %d
    %%h = call i64 @ll_hash_p__%s(ptr %%p)
    ret i64 %%h
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	// 5. ToString
	if CanToStringType(g.prog, opt) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
		g.line("entry:")
		if boxed {
			g.indentLine(1, "%%box = load ptr, ptr %%p, align 8")
			g.indentLine(1, "%%has = icmp ne ptr %%box, null")
			g.indentLine(1, "br i1 %%has, label %%some, label %%none")
			g.line("some:")
			g.emitValueToStrInline("%out", "%box", elem)
			g.indentLine(1, "ret void")
			g.line("none:")
			g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
			g.emitPushLitToStrInline("%out", "none")
			g.indentLine(1, "ret void")
		} else {
			g.indentLine(1, "%%p_has = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%has = load i1, ptr %%p_has, align 1")
			g.indentLine(1, "br i1 %%has, label %%some, label %%none")
			g.line("some:")
			g.indentLine(1, "%%p_val = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
			g.emitValueToStrInline("%out", "%p_val", elem)
			g.indentLine(1, "ret void")
			g.line("none:")
			g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
			g.emitPushLitToStrInline("%out", "none")
			g.indentLine(1, "ret void")
		}
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}
}

func (g *llvmEmitter) emitClosureHelpersLLVM(fn *types.Func) {
	mangled := fn.Mangled()

	if !g.funcSet["ll_closure_drop"] {
		g.funcSet["ll_closure_drop"] = true
		g.funcSet["ll_closure_copy_p"] = true
		g.funcSet["ll_closure_tostr_p"] = true
		fnLitSym := g.internString("<fn>").symbol
		g.line(`define internal void @ll_closure_drop(ptr %%c) {
entry:
    %%is_null = icmp eq ptr %%c, null
    br i1 %%is_null, label %%done, label %%chk
chk:
    %%p_env = getelementptr inbounds %%struct.ll_closure, ptr %%c, i32 0, i32 1
    %%p_drop = getelementptr inbounds %%struct.ll_closure, ptr %%c, i32 0, i32 3
    %%env = load ptr, ptr %%p_env, align 8
    %%drop_fn = load ptr, ptr %%p_drop, align 8
    %%c1 = icmp ne ptr %%env, null
    %%c2 = icmp ne ptr %%drop_fn, null
    %%do_it = and i1 %%c1, %%c2
    br i1 %%do_it, label %%call_drop, label %%zero
call_drop:
    call void %%drop_fn(ptr %%env)
    br label %%zero
zero:
    store %%struct.ll_closure zeroinitializer, ptr %%c, align 8
    br label %%done
done:
    ret void
}

define internal void @ll_closure_copy_p(ptr %%dst, ptr %%src) {
entry:
    %%v = load %%struct.ll_closure, ptr %%src, align 8
    store %%struct.ll_closure %%v, ptr %%dst, align 8
    %%p_env = getelementptr inbounds %%struct.ll_closure, ptr %%src, i32 0, i32 1
    %%p_copy = getelementptr inbounds %%struct.ll_closure, ptr %%src, i32 0, i32 2
    %%env = load ptr, ptr %%p_env, align 8
    %%copy_fn = load ptr, ptr %%p_copy, align 8
    %%c1 = icmp ne ptr %%env, null
    %%c2 = icmp ne ptr %%copy_fn, null
    %%do_it = and i1 %%c1, %%c2
    br i1 %%do_it, label %%call_copy, label %%done
call_copy:
    %%new_env = call ptr %%copy_fn(ptr %%env)
    %%d_env = getelementptr inbounds %%struct.ll_closure, ptr %%dst, i32 0, i32 1
    store ptr %%new_env, ptr %%d_env, align 8
    br label %%done
done:
    ret void
}

define internal void @ll_closure_tostr_p(ptr %%out, ptr %%c) {
entry:
    store %%struct.ll_string zeroinitializer, ptr %%out, align 8
    call void @ll_str_push_bytes(ptr %%out, ptr %s, i64 4)
    ret void
}
`, fnLitSym)
	}

	for _, prefix := range []string{"ll_drop__", "ll_copy__", "ll_copy_p__", "ll_tostr__", "ll_tostr_p__"} {
		g.funcSet[prefix+mangled] = true
	}

	g.line(`define internal void @ll_drop__%s(ptr %%p) {
entry:
    call void @ll_closure_drop(ptr %%p)
    ret void
}
define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {
entry:
    call void @ll_closure_copy_p(ptr %%dst, ptr %%src)
    ret void
}
define internal %%struct.ll_closure @ll_copy__%s(%%struct.ll_closure %%v) {
entry:
    %%src = alloca %%struct.ll_closure, align 8
    %%dst = alloca %%struct.ll_closure, align 8
    store %%struct.ll_closure %%v, ptr %%src, align 8
    call void @ll_closure_copy_p(ptr %%dst, ptr %%src)
    %%r = load %%struct.ll_closure, ptr %%dst, align 8
    ret %%struct.ll_closure %%r
}
define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {
entry:
    call void @ll_closure_tostr_p(ptr %%out, ptr %%p)
    ret void
}
define internal %%struct.ll_string @ll_tostr__%s(%%struct.ll_closure %%v) {
entry:
    %%out = alloca %%struct.ll_string, align 8
    call void @ll_closure_tostr_p(ptr %%out, ptr null)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, mangled, mangled, mangled, mangled)
}

func (g *llvmEmitter) emitInterfaceHelpersLLVM(ifc *types.Interface) {
	mangled := ifc.Mangled()
	tyStr := g.llvmType(ifc)
	ifcLitSym := g.internString("<" + ifc.Name + ">").symbol

	for _, prefix := range []string{"ll_drop__", "ll_copy__", "ll_copy_p__", "ll_tostr__", "ll_tostr_p__"} {
		g.funcSet[prefix+mangled] = true
	}

	g.line(`define internal void @ll_drop__%s(ptr %%p) {
entry:
    %%is_null = icmp eq ptr %%p, null
    br i1 %%is_null, label %%done, label %%chk
chk:
    %%p_data = getelementptr inbounds %s, ptr %%p, i32 0, i32 0
    %%p_vt = getelementptr inbounds %s, ptr %%p, i32 0, i32 1
    %%p_own = getelementptr inbounds %s, ptr %%p, i32 0, i32 2
    %%own = load i1, ptr %%p_own, align 1
    %%data = load ptr, ptr %%p_data, align 8
    %%vt = load ptr, ptr %%p_vt, align 8
    %%c1 = icmp ne ptr %%data, null
    %%c2 = icmp ne ptr %%vt, null
    %%c3 = and i1 %%own, %%c1
    %%do_it = and i1 %%c3, %%c2
    br i1 %%do_it, label %%call_drop, label %%zero
call_drop:
    %%p_drop = getelementptr inbounds ptr, ptr %%vt, i64 0
    %%drop_fn = load ptr, ptr %%p_drop, align 8
    %%has_fn = icmp ne ptr %%drop_fn, null
    br i1 %%has_fn, label %%do_call, label %%zero
do_call:
    call void %%drop_fn(ptr %%data)
    br label %%zero
zero:
    store %s zeroinitializer, ptr %%p, align 8
    br label %%done
done:
    ret void
}

define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {
entry:
    store %s zeroinitializer, ptr %%dst, align 8
    %%s_data_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 0
    %%s_vt_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 1
    %%s_own_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 2
    %%d_data_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0
    %%d_vt_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1
    %%d_own_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 2
    %%data = load ptr, ptr %%s_data_p, align 8
    %%vt = load ptr, ptr %%s_vt_p, align 8
    %%own = load i1, ptr %%s_own_p, align 1
    store ptr %%vt, ptr %%d_vt_p, align 8
    store i1 %%own, ptr %%d_own_p, align 1
    %%c1 = icmp ne ptr %%data, null
    %%c2 = icmp ne ptr %%vt, null
    %%c3 = and i1 %%own, %%c1
    %%do_clone = and i1 %%c3, %%c2
    br i1 %%do_clone, label %%clone_box, label %%copy_ptr
clone_box:
    %%p_clone = getelementptr inbounds ptr, ptr %%vt, i64 1
    %%clone_fn = load ptr, ptr %%p_clone, align 8
    %%nd = call ptr %%clone_fn(ptr %%data)
    store ptr %%nd, ptr %%d_data_p, align 8
    ret void
copy_ptr:
    store ptr %%data, ptr %%d_data_p, align 8
    ret void
}

define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align 8
    %%dst = alloca %s, align 8
    store %s %%v, ptr %%src, align 8
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align 8
    ret %s %%r
}
`, mangled, tyStr, tyStr, tyStr, tyStr,
		mangled, tyStr, tyStr, tyStr, tyStr, tyStr, tyStr, tyStr,
		tyStr, mangled, tyStr, tyStr, tyStr, tyStr, mangled, tyStr, tyStr)

	toStrMethodIdx := -1
	for idx, m := range ifc.Methods {
		if m.Name == "to_string" && len(m.Params) == 0 && m.Ret != nil && m.Ret.Equals(types.TypeString) {
			toStrMethodIdx = idx + 2 // after drop_box, clone_box
			break
		}
	}
	g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
	g.line("entry:")
	if toStrMethodIdx >= 0 {
		g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%p, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%p_vt = getelementptr inbounds %s, ptr %%p, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
		g.indentLine(1, "%%vt = load ptr, ptr %%p_vt, align 8")
		g.indentLine(1, "%%has_vt = icmp ne ptr %%vt, null")
		g.indentLine(1, "br i1 %%has_vt, label %%call_m, label %%fallback")
		g.line("call_m:")
		g.indentLine(1, "%%p_fn = getelementptr inbounds ptr, ptr %%vt, i64 %d", toStrMethodIdx)
		g.indentLine(1, "%%fn = load ptr, ptr %%p_fn, align 8")
		g.indentLine(1, "%%s = call %%struct.ll_string %%fn(ptr %%data)")
		g.indentLine(1, "store %%struct.ll_string %%s, ptr %%out, align 8")
		g.indentLine(1, "ret void")
		g.line("fallback:")
	}
	g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
	g.indentLine(1, "call void @ll_str_push_bytes(ptr %%out, ptr %s, i64 %d)", ifcLitSym, len(ifc.Name)+2)
	g.indentLine(1, "ret void")
	g.line("}")
	g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align 8
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align 8
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, tyStr, mangled)
}

func (g *llvmEmitter) emitLambdaEnvHelpersLLVM(li *sema.LambdaInfo) {
	envTy := "%struct." + li.EnvCName
	dropFn := li.EnvCName + "_drop"
	copyFn := li.EnvCName + "_copy"
	g.funcSet[dropFn] = true
	g.funcSet[copyFn] = true

	g.nextRegID = 0
	g.line("define internal void @%s(ptr %%raw) {", dropFn)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%raw, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%do_drop")
	g.line("do_drop:")
	for i := len(li.Captures) - 1; i >= 0; i-- {
		c := li.Captures[i]
		if !c.Type.IsPlain() {
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%raw, i32 0, i32 %d", fp, envTy, i)
			g.emitValueDropInline(fp, c.Type)
		}
	}
	g.indentLine(1, "call void @ll_free(ptr %%raw)")
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	canCopyAll := true
	for _, c := range li.Captures {
		if !CanCopyType(g.prog, c.Type) {
			canCopyAll = false
			break
		}
	}

	g.nextRegID = 0
	g.line("define internal ptr @%s(ptr %%raw) {", copyFn)
	g.line("entry:")
	if !canCopyAll {
		msg := g.internString("cannot copy closure capturing non-copyable resource").symbol
		g.indentLine(1, "call void @ll_panic_cstr(ptr %s, ptr null, i32 0)", msg)
		g.indentLine(1, "unreachable")
	} else {
		g.indentLine(1, "%%is_null = icmp eq ptr %%raw, null")
		g.indentLine(1, "br i1 %%is_null, label %%ret_null, label %%do_copy")
		g.line("ret_null:")
		g.indentLine(1, "ret ptr null")
		g.line("do_copy:")
		g.indentLine(1, "%%sz_p = getelementptr %s, ptr null, i64 1", envTy)
		g.indentLine(1, "%%sz = ptrtoint ptr %%sz_p to i64")
		g.indentLine(1, "%%dst = call ptr @ll_alloc(i64 %%sz)")
		for i, c := range li.Captures {
			sf := g.newReg()
			df := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%raw, i32 0, i32 %d", sf, envTy, i)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%dst, i32 0, i32 %d", df, envTy, i)
			g.emitValueCopyInline(df, sf, c.Type)
		}
		g.indentLine(1, "ret ptr %%dst")
	}
	g.line("}")
}

func (g *llvmEmitter) emitVectorHelpersLLVM(vec *types.Vector) {
	mangled := vec.Mangled()
	tyStr := g.llvmType(vec)
	elem := vec.Elem
	elemTyStr := g.llvmType(elem)

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%v) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%v, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%chk")
	g.line("chk:")
	g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%cap = load i64, ptr %%p_cap, align 8")
	g.indentLine(1, "%%c1 = icmp sgt i64 %%cap, 0")
	g.indentLine(1, "%%c2 = icmp ne ptr %%data, null")
	g.indentLine(1, "%%owned = and i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%owned, label %%drop_elems, label %%zero")
	g.line("drop_elems:")
	if !elem.IsPlain() {
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%len, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%free_buf")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%ni", elemTyStr)
		g.emitValueDropInline("%ep", elem)
		g.indentLine(1, "br label %%loop_cond")
		g.line("free_buf:")
	}
	g.indentLine(1, "call void @ll_free(ptr %%data)")
	g.indentLine(1, "br label %%zero")
	g.line("zero:")
	g.indentLine(1, "store %s zeroinitializer, ptr %%v, align 8", tyStr)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, vec) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align 8", tyStr)
		g.indentLine(1, "%%s_data_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%s_len_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%s_data = load ptr, ptr %%s_data_p, align 8")
		g.indentLine(1, "%%s_len = load i64, ptr %%s_len_p, align 8")
		g.indentLine(1, "%%nonempty = icmp sgt i64 %%s_len, 0")
		g.indentLine(1, "br i1 %%nonempty, label %%alloc, label %%done")
		g.line("alloc:")
		g.indentLine(1, "%%sz_p = getelementptr %s, ptr null, i64 %%s_len", elemTyStr)
		g.indentLine(1, "%%tot = ptrtoint ptr %%sz_p to i64")
		g.indentLine(1, "%%nd = call ptr @ll_alloc(i64 %%tot)")
		g.indentLine(1, "%%d_data_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%d_len_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%d_cap_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 2", tyStr)
		g.indentLine(1, "store ptr %%nd, ptr %%d_data_p, align 8")
		g.indentLine(1, "store i64 %%s_len, ptr %%d_len_p, align 8")
		g.indentLine(1, "store i64 %%s_len, ptr %%d_cap_p, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%s_len")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%sp = getelementptr inbounds %s, ptr %%s_data, i64 %%i", elemTyStr)
		g.indentLine(1, "%%dp = getelementptr inbounds %s, ptr %%nd, i64 %%i", elemTyStr)
		g.emitValueCopyInline("%dp", "%sp", elem)
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align 8
    %%dst = alloca %s, align 8
    store %s %%v, ptr %%src, align 8
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align 8
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, tyStr, tyStr, mangled, tyStr, tyStr)
	}

	// 3. ToString
	if CanToStringType(g.prog, vec) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%v) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.emitPushLitToStrInline("%out", "[")
		g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
		g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%len")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%gt0 = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%gt0, label %%add_sep, label %%add_elem")
		g.line("add_sep:")
		g.emitPushLitToStrInline("%out", ", ")
		g.indentLine(1, "br label %%add_elem")
		g.line("add_elem:")
		g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%i", elemTyStr)
		g.emitAppendValueToStrInline("%out", "%ep", elem)
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.emitPushLitToStrInline("%out", "]")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align 8
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align 8
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, tyStr, mangled)
	}
}

func (g *llvmEmitter) emitStoreOptSome(outPtr, src string, srcIsPtr bool, elem types.Type, opt *types.Optional) {
	elemTyStr := g.llvmType(elem)
	elemAlign := g.typeAlign(elem)
	optTyStr := g.llvmType(opt)
	storeInto := func(dstPtr string) {
		if srcIsPtr {
			g.emitValueCopyInline(dstPtr, src, elem)
		} else {
			g.indentLine(1, "store %s %s, ptr %s, align %d", elemTyStr, src, dstPtr, elemAlign)
		}
	}
	// Nested optionals flatten (`?(?T) -> ?T`), so an `?T` element is stored directly.
	if _, elemIsOpt := elem.(*types.Optional); elemIsOpt {
		storeInto(outPtr)
		return
	}
	if opt.IsBoxed() {
		szP := g.newReg()
		sz := g.newReg()
		box := g.newReg()
		g.indentLine(1, "%s = getelementptr %s, ptr null, i64 1", szP, elemTyStr)
		g.indentLine(1, "%s = ptrtoint ptr %s to i64", sz, szP)
		g.indentLine(1, "%s = call ptr @ll_alloc(i64 %s)", box, sz)
		storeInto(box)
		g.indentLine(1, "store ptr %s, ptr %s, align 8", box, outPtr)
		return
	}
	oHas := g.newReg()
	oVal := g.newReg()
	g.indentLine(1, "store %s zeroinitializer, ptr %s, align 8", optTyStr, outPtr)
	g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", oHas, optTyStr, outPtr)
	g.indentLine(1, "%s = getelementptr inbounds %s, ptr %s, i32 0, i32 1", oVal, optTyStr, outPtr)
	g.indentLine(1, "store i1 true, ptr %s, align 1", oHas)
	storeInto(oVal)
}

func (g *llvmEmitter) emitVectorMethodsLLVM(vec *types.Vector) {
	mangled := vec.Mangled()
	tyStr := g.llvmType(vec)
	elem := vec.Elem
	elemTyStr := g.llvmType(elem)
	elemAlign := g.typeAlign(elem)
	optElem := types.NewOptional(elem)
	optTyStr := g.llvmType(optElem)
	sliceOobSym := g.internString("vector slice bounds out of range").symbol

	fns := []string{
		"ll_vec_ensure_owned_cap__" + mangled,
		"ll_vec_push_p__" + mangled,
		"ll_vec_push__" + mangled,
		"ll_vec_pop_p__" + mangled,
		"ll_vec_pop__" + mangled,
		"ll_vec_clear__" + mangled,
		"ll_vec_slice_view_p__" + mangled,
		"ll_vec_reserve__" + mangled,
		"ll_vec_remove__" + mangled,
		"ll_vec_swap_remove__" + mangled,
	}
	if CanCopyType(g.prog, elem) {
		fns = append(fns, "ll_vec_get__"+mangled)
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	// 1. ensure_owned_cap
	g.nextRegID = 0
	g.line("define internal void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%min_cap) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%cap = load i64, ptr %%p_cap, align 8")
	g.indentLine(1, "%%is_view = icmp eq i64 %%cap, 0")
	g.indentLine(1, "br i1 %%is_view, label %%from_view, label %%chk_grow")
	g.line("from_view:")
	g.indentLine(1, "%%c_init = select i1 true, i64 4, i64 4")
	g.indentLine(1, "%%need1 = icmp slt i64 4, %%min_cap")
	g.indentLine(1, "%%nc1 = select i1 %%need1, i64 %%min_cap, i64 4")
	g.indentLine(1, "%%need2 = icmp slt i64 %%nc1, %%len")
	g.indentLine(1, "%%nc = select i1 %%need2, i64 %%len, i64 %%nc1")
	g.indentLine(1, "%%sz_p = getelementptr %s, ptr null, i64 %%nc", elemTyStr)
	g.indentLine(1, "%%tot = ptrtoint ptr %%sz_p to i64")
	g.indentLine(1, "%%nd = call ptr @ll_alloc(i64 %%tot)")
	g.indentLine(1, "%%has_elems = icmp sgt i64 %%len, 0")
	g.indentLine(1, "%%has_ptr = icmp ne ptr %%data, null")
	g.indentLine(1, "%%do_cp = and i1 %%has_elems, %%has_ptr")
	g.indentLine(1, "br i1 %%do_cp, label %%view_copy, label %%view_done")
	g.line("view_copy:")
	if CanCopyType(g.prog, elem) && !elem.IsPlain() {
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%vc_cond")
		g.line("vc_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%len")
		g.indentLine(1, "br i1 %%more, label %%vc_body, label %%view_done")
		g.line("vc_body:")
		g.indentLine(1, "%%sp = getelementptr inbounds %s, ptr %%data, i64 %%i", elemTyStr)
		g.indentLine(1, "%%dp = getelementptr inbounds %s, ptr %%nd, i64 %%i", elemTyStr)
		g.emitValueCopyInline("%dp", "%sp", elem)
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%vc_cond")
	} else {
		g.indentLine(1, "%%cp_p = getelementptr %s, ptr null, i64 %%len", elemTyStr)
		g.indentLine(1, "%%cp_bytes = ptrtoint ptr %%cp_p to i64")
		g.indentLine(1, "call void @llvm.memcpy.p0.p0.i64(ptr %%nd, ptr %%data, i64 %%cp_bytes, i1 false)")
		g.indentLine(1, "br label %%view_done")
	}
	g.line("view_done:")
	g.indentLine(1, "store ptr %%nd, ptr %%p_data, align 8")
	g.indentLine(1, "store i64 %%nc, ptr %%p_cap, align 8")
	g.indentLine(1, "ret void")
	g.line("chk_grow:")
	g.indentLine(1, "%%need_grow = icmp slt i64 %%cap, %%min_cap")
	g.indentLine(1, "br i1 %%need_grow, label %%do_grow, label %%done")
	g.line("do_grow:")
	g.indentLine(1, "%%c2 = mul i64 %%cap, 2")
	g.indentLine(1, "%%lt_min = icmp slt i64 %%c2, %%min_cap")
	g.indentLine(1, "%%gcap = select i1 %%lt_min, i64 %%min_cap, i64 %%c2")
	g.indentLine(1, "%%gsz_p = getelementptr %s, ptr null, i64 %%gcap", elemTyStr)
	g.indentLine(1, "%%gtot = ptrtoint ptr %%gsz_p to i64")
	g.indentLine(1, "%%gnd = call ptr @ll_realloc(ptr %%data, i64 %%gtot)")
	g.indentLine(1, "store ptr %%gnd, ptr %%p_data, align 8")
	g.indentLine(1, "store i64 %%gcap, ptr %%p_cap, align 8")
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. push_p & push
	g.line(`define internal void @ll_vec_push_p__%s(ptr %%v, ptr %%item) {
entry:
    %%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%need = add i64 %%len, 1
    call void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%need)
    %%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%slot = getelementptr inbounds %s, ptr %%data, i64 %%len
    %%val = load %s, ptr %%item, align %d
    store %s %%val, ptr %%slot, align %d
    store i64 %%need, ptr %%p_len, align 8
    ret void
}
define internal void @ll_vec_push__%s(ptr %%v, %s %%item) {
entry:
    %%p = alloca %s, align %d
    store %s %%item, ptr %%p, align %d
    call void @ll_vec_push_p__%s(ptr %%v, ptr %%p)
    ret void
}`, mangled, tyStr, mangled, tyStr, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemAlign,
		mangled, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemAlign, mangled)

	// 3. pop_p & pop
	g.nextRegID = 0
	g.line("define internal void @ll_vec_pop_p__%s(ptr %%out, ptr %%v) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%empty = icmp sle i64 %%len, 0")
	g.indentLine(1, "br i1 %%empty, label %%ret_none, label %%do_pop")
	g.line("ret_none:")
	if optElem.IsBoxed() {
		g.indentLine(1, "store ptr null, ptr %%out, align 8")
	} else {
		g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", optTyStr)
	}
	g.indentLine(1, "ret void")
	g.line("do_pop:")
	g.indentLine(1, "%%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%cap = load i64, ptr %%p_cap, align 8")
	g.indentLine(1, "%%is_view = icmp eq i64 %%cap, 0")
	g.indentLine(1, "br i1 %%is_view, label %%ensure_own, label %%pop_elem")
	g.line("ensure_own:")
	g.indentLine(1, "call void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%len)", mangled)
	g.indentLine(1, "br label %%pop_elem")
	g.line("pop_elem:")
	g.indentLine(1, "%%nlen = sub i64 %%len, 1")
	g.indentLine(1, "store i64 %%nlen, ptr %%p_len, align 8")
	g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
	g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%nlen", elemTyStr)
	g.indentLine(1, "%%val = load %s, ptr %%ep, align %d", elemTyStr, elemAlign)
	g.indentLine(1, "store %s zeroinitializer, ptr %%ep, align %d", elemTyStr, elemAlign)
	g.emitStoreOptSome("%out", "%val", false, elem, optElem)
	g.indentLine(1, "ret void")
	g.line("}")

	g.line(`define internal %s @ll_vec_pop__%s(ptr %%v) {
entry:
    %%out = alloca %s, align 8
    call void @ll_vec_pop_p__%s(ptr %%out, ptr %%v)
    %%r = load %s, ptr %%out, align 8
    ret %s %%r
}`, optTyStr, mangled, optTyStr, mangled, optTyStr, optTyStr)

	if CanCopyType(g.prog, elem) {
		g.nextRegID = 0
		g.line("define internal %s @ll_vec_get__%s(%s %%v, i64 %%idx) {", optTyStr, mangled, tyStr)
		g.line("entry:")
		g.indentLine(1, "%%out = alloca %s, align 8", optTyStr)
		g.indentLine(1, "%%vec_slot = alloca %s, align 8", tyStr)
		g.indentLine(1, "store %s %%v, ptr %%vec_slot, align 8", tyStr)
		g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%vec_slot, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%vec_slot, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
		g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%c1 = icmp sge i64 %%idx, 0")
		g.indentLine(1, "%%c2 = icmp slt i64 %%idx, %%len")
		g.indentLine(1, "%%ok = and i1 %%c1, %%c2")
		g.indentLine(1, "br i1 %%ok, label %%in_bounds, label %%oob")
		g.line("oob:")
		if optElem.IsBoxed() {
			g.indentLine(1, "store ptr null, ptr %%out, align 8")
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", optTyStr)
		}
		g.indentLine(1, "br label %%done")
		g.line("in_bounds:")
		g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%idx", elemTyStr)
		g.indentLine(1, "%%val_slot = alloca %s, align %d", elemTyStr, elemAlign)
		g.emitValueCopyInline("%val_slot", "%ep", elem)
		g.indentLine(1, "%%val = load %s, ptr %%val_slot, align %d", elemTyStr, elemAlign)
		g.emitStoreOptSome("%out", "%val", false, elem, optElem)
		g.indentLine(1, "br label %%done")
		g.line("done:")
		g.indentLine(1, "%%r = load %s, ptr %%out, align 8", optTyStr)
		g.indentLine(1, "ret %s %%r", optTyStr)
		g.line("}")
	}

	// 4. clear
	g.nextRegID = 0
	g.line("define internal void @ll_vec_clear__%s(ptr %%v) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%cap = load i64, ptr %%p_cap, align 8")
	g.indentLine(1, "%%c1 = icmp sgt i64 %%cap, 0")
	g.indentLine(1, "%%c2 = icmp ne ptr %%data, null")
	g.indentLine(1, "%%owned = and i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%owned, label %%drop_elems, label %%clear_view")
	g.line("drop_elems:")
	if !elem.IsPlain() {
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%len, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%set_zero")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%ni", elemTyStr)
		g.emitValueDropInline("%ep", elem)
		g.indentLine(1, "br label %%loop_cond")
		g.line("set_zero:")
	}
	g.indentLine(1, "store i64 0, ptr %%p_len, align 8")
	g.indentLine(1, "ret void")
	g.line("clear_view:")
	g.indentLine(1, "store ptr null, ptr %%p_data, align 8")
	g.indentLine(1, "store i64 0, ptr %%p_len, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	// 5. contains_p & contains
	if CanEqType(g.prog, elem) {
		g.funcSet["ll_vec_contains_p__"+mangled] = true
		g.funcSet["ll_vec_contains__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_vec_contains_p__%s(ptr %%v, ptr %%target) {", mangled)
		g.line("entry:")
		g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%data = load ptr, ptr %%p_data, align 8")
		g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%len")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%ret_false")
		g.line("loop_body:")
		g.indentLine(1, "%%ep = getelementptr inbounds %s, ptr %%data, i64 %%i", elemTyStr)
		eq := g.emitValueEqInline("%ep", "%target", elem)
		g.indentLine(1, "br i1 %s, label %%ret_true, label %%next_iter", eq)
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("ret_true:")
		g.indentLine(1, "ret i1 true")
		g.line("ret_false:")
		g.indentLine(1, "ret i1 false")
		g.line("}")

		g.line(`define internal i1 @ll_vec_contains__%s(%s %%v, %s %%target) {
entry:
    %%pv = alloca %s, align 8
    %%pt = alloca %s, align %d
    store %s %%v, ptr %%pv, align 8
    store %s %%target, ptr %%pt, align %d
    %%r = call i1 @ll_vec_contains_p__%s(ptr %%pv, ptr %%pt)
    ret i1 %%r
}`, mangled, tyStr, elemTyStr, tyStr, elemTyStr, elemAlign, tyStr, elemTyStr, elemAlign, mangled)
	}

	// 6. slice_view_p & slice_owned_p
	g.line(`define internal void @ll_vec_slice_view_p__%s(ptr %%out, ptr %%v, i64 %%lo, i64 %%hi, i32 %%check, ptr %%file, i64 %%line) {
entry:
    %%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0
    %%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1
    %%data = load ptr, ptr %%p_data, align 8
    %%len = load i64, ptr %%p_len, align 8
    %%do_chk = icmp ne i32 %%check, 0
    br i1 %%do_chk, label %%chk_bounds, label %%make_view
chk_bounds:
    %%c1 = icmp slt i64 %%lo, 0
    %%c2 = icmp slt i64 %%hi, %%lo
    %%c3 = icmp sgt i64 %%hi, %%len
    %%b1 = or i1 %%c1, %%c2
    %%bad = or i1 %%b1, %%c3
    br i1 %%bad, label %%panic, label %%make_view
panic:
    %%line32 = trunc i64 %%line to i32
    call void @ll_panic_cstr(ptr %s, ptr %%file, i32 %%line32)
    unreachable
make_view:
    %%slen = sub i64 %%hi, %%lo
    %%has_data = icmp ne ptr %%data, null
    br i1 %%has_data, label %%gep_ptr, label %%null_ptr
gep_ptr:
    %%gp = getelementptr inbounds %s, ptr %%data, i64 %%lo
    br label %%store_out
null_ptr:
    br label %%store_out
store_out:
    %%sptr = phi ptr [ %%gp, %%gep_ptr ], [ null, %%null_ptr ]
    %%o0 = getelementptr inbounds %s, ptr %%out, i32 0, i32 0
    %%o1 = getelementptr inbounds %s, ptr %%out, i32 0, i32 1
    %%o2 = getelementptr inbounds %s, ptr %%out, i32 0, i32 2
    store ptr %%sptr, ptr %%o0, align 8
    store i64 %%slen, ptr %%o1, align 8
    store i64 0, ptr %%o2, align 8
    ret void
}`, mangled, tyStr, tyStr, sliceOobSym, elemTyStr, tyStr, tyStr, tyStr)

	if CanCopyType(g.prog, vec) {
		g.funcSet["ll_vec_slice_owned_p__"+mangled] = true
		g.line(`define internal void @ll_vec_slice_owned_p__%s(ptr %%out, ptr %%v, i64 %%lo, i64 %%hi, i32 %%check, ptr %%file, i64 %%line) {
entry:
    %%view = alloca %s, align 8
    call void @ll_vec_slice_view_p__%s(ptr %%view, ptr %%v, i64 %%lo, i64 %%hi, i32 %%check, ptr %%file, i64 %%line)
    call void @ll_copy_p__%s(ptr %%out, ptr %%view)
    ret void
}`, mangled, tyStr, mangled, mangled)
	}

	// 7. reserve
	g.line(`define internal void @ll_vec_reserve__%s(ptr %%v, i64 %%want) {
entry:
    %%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%le = icmp sle i64 %%want, %%cap
    br i1 %%le, label %%done, label %%do_res
do_res:
    call void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%want)
    br label %%done
done:
    ret void
}`, mangled, tyStr, mangled)

	// 8. remove
	g.line(`define internal %s @ll_vec_remove__%s(ptr %%v, i64 %%idx, i64 %%check, ptr %%file, i64 %%line) {
entry:
    %%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%do_chk = icmp ne i64 %%check, 0
    br i1 %%do_chk, label %%chk, label %%after_chk
chk:
    call i64 @ll_check_bounds(i64 %%idx, i64 %%len, ptr %%file, i64 %%line)
    br label %%after_chk
after_chk:
    %%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%is_view = icmp eq i64 %%cap, 0
    br i1 %%is_view, label %%ensure_own, label %%do_rem
ensure_own:
    call void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%len)
    br label %%do_rem
do_rem:
    %%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%ep = getelementptr inbounds %s, ptr %%data, i64 %%idx
    %%out = load %s, ptr %%ep, align %d
    %%tail = sub i64 %%len, %%idx
    %%tail_dec = sub i64 %%tail, 1
    %%has_tail = icmp sgt i64 %%tail_dec, 0
    br i1 %%has_tail, label %%shift, label %%finish
shift:
    %%idx_p1 = add i64 %%idx, 1
    %%next_p = getelementptr inbounds %s, ptr %%data, i64 %%idx_p1
    %%sz_p = getelementptr %s, ptr null, i64 %%tail_dec
    %%tail_bytes = ptrtoint ptr %%sz_p to i64
    call ptr @memmove(ptr %%ep, ptr %%next_p, i64 %%tail_bytes)
    br label %%finish
finish:
    %%nlen = sub i64 %%len, 1
    store i64 %%nlen, ptr %%p_len, align 8
    %%tail_p = getelementptr inbounds %s, ptr %%data, i64 %%nlen
    store %s zeroinitializer, ptr %%tail_p, align %d
    ret %s %%out
}`, elemTyStr, mangled, tyStr, tyStr, mangled, tyStr, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemTyStr, elemTyStr, elemTyStr, elemAlign, elemTyStr)

	// 9. swap_remove
	g.line(`define internal %s @ll_vec_swap_remove__%s(ptr %%v, i64 %%idx, i64 %%check, ptr %%file, i64 %%line) {
entry:
    %%p_len = getelementptr inbounds %s, ptr %%v, i32 0, i32 1
    %%len = load i64, ptr %%p_len, align 8
    %%do_chk = icmp ne i64 %%check, 0
    br i1 %%do_chk, label %%chk, label %%after_chk
chk:
    call i64 @ll_check_bounds(i64 %%idx, i64 %%len, ptr %%file, i64 %%line)
    br label %%after_chk
after_chk:
    %%p_cap = getelementptr inbounds %s, ptr %%v, i32 0, i32 2
    %%cap = load i64, ptr %%p_cap, align 8
    %%is_view = icmp eq i64 %%cap, 0
    br i1 %%is_view, label %%ensure_own, label %%do_rem
ensure_own:
    call void @ll_vec_ensure_owned_cap__%s(ptr %%v, i64 %%len)
    br label %%do_rem
do_rem:
    %%p_data = getelementptr inbounds %s, ptr %%v, i32 0, i32 0
    %%data = load ptr, ptr %%p_data, align 8
    %%ep = getelementptr inbounds %s, ptr %%data, i64 %%idx
    %%out = load %s, ptr %%ep, align %d
    %%nlen = sub i64 %%len, 1
    store i64 %%nlen, ptr %%p_len, align 8
    %%needs_swap = icmp slt i64 %%idx, %%nlen
    br i1 %%needs_swap, label %%do_swap, label %%finish
do_swap:
    %%last_p = getelementptr inbounds %s, ptr %%data, i64 %%nlen
    %%last_val = load %s, ptr %%last_p, align %d
    store %s %%last_val, ptr %%ep, align %d
    br label %%finish
finish:
    %%tail_p = getelementptr inbounds %s, ptr %%data, i64 %%nlen
    store %s zeroinitializer, ptr %%tail_p, align %d
    ret %s %%out
}`, elemTyStr, mangled, tyStr, tyStr, mangled, tyStr, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemAlign, elemTyStr, elemTyStr, elemAlign, elemTyStr)
}

func (g *llvmEmitter) emitStackArrayHelpersLLVM(arr *types.StackArray) {
	mangled := arr.Mangled()
	tyStr := g.llvmType(arr)
	al := g.typeAlign(arr)
	elem := arr.Elem
	elemTyStr := g.llvmType(elem)

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%a) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%a, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%do_drop")
	g.line("do_drop:")
	if !elem.IsPlain() && arr.Size > 0 {
		g.indentLine(1, "%%p_elems = getelementptr inbounds %s, ptr %%a, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %d, ptr %%idx, align 8", arr.Size)
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%zero")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ep = getelementptr inbounds [%d x %s], ptr %%p_elems, i64 0, i64 %%ni", arr.Size, elemTyStr)
		g.emitValueDropInline("%ep", elem)
		g.indentLine(1, "br label %%loop_cond")
		g.line("zero:")
	}
	g.indentLine(1, "store %s zeroinitializer, ptr %%a, align %d", tyStr, al)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, arr) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align %d", tyStr, al)
		if arr.Size > 0 {
			g.indentLine(1, "%%s_elems = getelementptr inbounds %s, ptr %%src, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%d_elems = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%idx = alloca i64, align 8")
			g.indentLine(1, "store i64 0, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("loop_cond:")
			g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
			g.indentLine(1, "%%more = icmp slt i64 %%i, %d", arr.Size)
			g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
			g.line("loop_body:")
			g.indentLine(1, "%%sp = getelementptr inbounds [%d x %s], ptr %%s_elems, i64 0, i64 %%i", arr.Size, elemTyStr)
			g.indentLine(1, "%%dp = getelementptr inbounds [%d x %s], ptr %%d_elems, i64 0, i64 %%i", arr.Size, elemTyStr)
			g.emitValueCopyInline("%dp", "%sp", elem)
			g.indentLine(1, "%%ni = add i64 %%i, 1")
			g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("done:")
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	// 3. ToString
	if CanToStringType(g.prog, arr) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%a) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.emitPushLitToStrInline("%out", "[")
		if arr.Size > 0 {
			g.indentLine(1, "%%p_elems = getelementptr inbounds %s, ptr %%a, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%idx = alloca i64, align 8")
			g.indentLine(1, "store i64 0, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("loop_cond:")
			g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
			g.indentLine(1, "%%more = icmp slt i64 %%i, %d", arr.Size)
			g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
			g.line("loop_body:")
			g.indentLine(1, "%%gt0 = icmp sgt i64 %%i, 0")
			g.indentLine(1, "br i1 %%gt0, label %%add_sep, label %%add_elem")
			g.line("add_sep:")
			g.emitPushLitToStrInline("%out", ", ")
			g.indentLine(1, "br label %%add_elem")
			g.line("add_elem:")
			g.indentLine(1, "%%ep = getelementptr inbounds [%d x %s], ptr %%p_elems, i64 0, i64 %%i", arr.Size, elemTyStr)
			g.emitAppendValueToStrInline("%out", "%ep", elem)
			g.indentLine(1, "%%ni = add i64 %%i, 1")
			g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("done:")
		}
		g.emitPushLitToStrInline("%out", "]")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}
}

func (g *llvmEmitter) emitStackArrayMethodsLLVM(arr *types.StackArray) {
	mangled := arr.Mangled()
	tyStr := g.llvmType(arr)
	al := g.typeAlign(arr)
	elem := arr.Elem
	elemTyStr := g.llvmType(elem)
	elemAlign := g.typeAlign(elem)

	if CanEqType(g.prog, elem) {
		g.funcSet["ll_arr_contains_p__"+mangled] = true
		g.funcSet["ll_arr_contains__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_arr_contains_p__%s(ptr %%a, ptr %%target) {", mangled)
		g.line("entry:")
		if arr.Size <= 0 {
			g.indentLine(1, "ret i1 false")
		} else {
			g.indentLine(1, "%%p_elems = getelementptr inbounds %s, ptr %%a, i32 0, i32 0", tyStr)
			g.indentLine(1, "%%idx = alloca i64, align 8")
			g.indentLine(1, "store i64 0, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("loop_cond:")
			g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
			g.indentLine(1, "%%more = icmp slt i64 %%i, %d", arr.Size)
			g.indentLine(1, "br i1 %%more, label %%loop_body, label %%ret_false")
			g.line("loop_body:")
			g.indentLine(1, "%%ep = getelementptr inbounds [%d x %s], ptr %%p_elems, i64 0, i64 %%i", arr.Size, elemTyStr)
			eq := g.emitValueEqInline("%ep", "%target", elem)
			g.indentLine(1, "br i1 %s, label %%ret_true, label %%next_iter", eq)
			g.line("next_iter:")
			g.indentLine(1, "%%ni = add i64 %%i, 1")
			g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
			g.indentLine(1, "br label %%loop_cond")
			g.line("ret_true:")
			g.indentLine(1, "ret i1 true")
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		}
		g.line("}")
		g.line(`define internal i1 @ll_arr_contains__%s(%s %%a, %s %%target) {
entry:
    %%pa = alloca %s, align %d
    %%pt = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%target, ptr %%pt, align %d
    %%r = call i1 @ll_arr_contains_p__%s(ptr %%pa, ptr %%pt)
    ret i1 %%r
}`, mangled, tyStr, elemTyStr, tyStr, al, elemTyStr, elemAlign, tyStr, al, elemTyStr, elemAlign, mangled)
	}

	if CanCopyType(g.prog, elem) {
		g.funcSet["ll_arr_get__"+mangled] = true
		optElem := types.NewOptional(elem)
		optTyStr := g.llvmType(optElem)
		g.nextRegID = 0
		g.line("define internal %s @ll_arr_get__%s(%s %%a, i64 %%idx) {", optTyStr, mangled, tyStr)
		g.line("entry:")
		g.indentLine(1, "%%res = alloca %s, align 8", optTyStr)
		g.indentLine(1, "%%a_slot = alloca %s, align %d", tyStr, al)
		g.indentLine(1, "store %s %%a, ptr %%a_slot, align %d", tyStr, al)
		g.indentLine(1, "%%c1 = icmp slt i64 %%idx, 0")
		g.indentLine(1, "%%c2 = icmp sge i64 %%idx, %d", arr.Size)
		g.indentLine(1, "%%oob = or i1 %%c1, %%c2")
		g.indentLine(1, "br i1 %%oob, label %%ret_none, label %%ret_some")
		g.line("ret_none:")
		if optElem.IsBoxed() {
			g.indentLine(1, "store ptr null, ptr %%res, align 8")
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%res, align 8", optTyStr)
		}
		g.indentLine(1, "br label %%finish")
		g.line("ret_some:")
		g.indentLine(1, "%%p_data = getelementptr inbounds %s, ptr %%a_slot, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%ep = getelementptr inbounds [%d x %s], ptr %%p_data, i64 0, i64 %%idx", arr.Size, elemTyStr)
		g.emitStoreOptSome("%res", "%ep", true, elem, optElem)
		g.indentLine(1, "br label %%finish")
		g.line("finish:")
		g.indentLine(1, "%%out = load %s, ptr %%res, align 8", optTyStr)
		g.indentLine(1, "ret %s %%out", optTyStr)
		g.line("}")
	}
}

func (g *llvmEmitter) emitTupleHelpersLLVM(tup *types.Tuple) {
	mangled := tup.Mangled()
	tyStr := g.llvmType(tup)
	al := g.typeAlign(tup)

	// 1. Drop
	if !tup.IsPlain() {
		g.funcSet["ll_drop__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_drop__%s(ptr %%p) {", mangled)
		g.line("entry:")
		g.indentLine(1, "%%is_null = icmp eq ptr %%p, null")
		g.indentLine(1, "br i1 %%is_null, label %%done, label %%do_drop")
		g.line("do_drop:")
		for i := len(tup.Elems) - 1; i >= 0; i-- {
			el := tup.Elems[i]
			if !el.IsPlain() {
				fp := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
				g.emitValueDropInline(fp, el)
			}
		}
		g.indentLine(1, "store %s zeroinitializer, ptr %%p, align %d", tyStr, al)
		g.indentLine(1, "br label %%done")
		g.line("done:")
		g.indentLine(1, "ret void")
		g.line("}")
	}

	// 2. Copy
	if CanCopyType(g.prog, tup) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align %d", tyStr, al)
		for i, el := range tup.Elems {
			sf := g.newReg()
			df := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%src, i32 0, i32 %d", sf, tyStr, i)
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%dst, i32 0, i32 %d", df, tyStr, i)
			g.emitValueCopyInline(df, sf, el)
		}
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align %d
    %%dst = alloca %s, align %d
    store %s %%v, ptr %%src, align %d
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align %d
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, al, tyStr, al, tyStr, al, mangled, tyStr, al, tyStr)
	}

	// 3. Eq
	if CanEqType(g.prog, tup) {
		g.funcSet["ll_eq_p__"+mangled] = true
		g.funcSet["ll_eq__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i1 @ll_eq_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if len(tup.Elems) == 0 {
			g.indentLine(1, "ret i1 true")
		} else {
			for i, el := range tup.Elems {
				fa := g.newReg()
				fb := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%a, i32 0, i32 %d", fa, tyStr, i)
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%b, i32 0, i32 %d", fb, tyStr, i)
				eq := g.emitValueEqInline(fa, fb, el)
				nxtLbl := fmt.Sprintf("eq.next.%d", i)
				g.indentLine(1, "br i1 %s, label %%%s, label %%ret_false", eq, nxtLbl)
				g.line("%s:", nxtLbl)
			}
			g.indentLine(1, "ret i1 true")
			g.line("ret_false:")
			g.indentLine(1, "ret i1 false")
		}
		g.line("}")
		g.line(`define internal i1 @ll_eq__%s(%s %%a, %s %%b) {
entry:
    %%pa = alloca %s, align %d
    %%pb = alloca %s, align %d
    store %s %%a, ptr %%pa, align %d
    store %s %%b, ptr %%pb, align %d
    %%r = call i1 @ll_eq_p__%s(ptr %%pa, ptr %%pb)
    ret i1 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}

	// 4. Hash
	if CanHashType(g.prog, tup) {
		g.funcSet["ll_hash_p__"+mangled] = true
		g.funcSet["ll_hash__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_hash_p__%s(ptr %%p) {", mangled)
		g.line("entry:")
		curH := "-3750763034362895579"
		for i, el := range tup.Elems {
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
			fh := g.emitValueHashInline(fp, el)
			comb := g.newReg()
			g.indentLine(1, "%s = call i64 @ll_hash_combine(i64 %s, i64 %s)", comb, curH, fh)
			curH = comb
		}
		g.indentLine(1, "ret i64 %s", curH)
		g.line("}")
		g.line(`define internal i64 @ll_hash__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    store %s %%v, ptr %%p, align %d
    %%h = call i64 @ll_hash_p__%s(ptr %%p)
    ret i64 %%h
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	// 5. ToString
	if CanToStringType(g.prog, tup) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%p) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.emitPushLitToStrInline("%out", "+[")
		for i, el := range tup.Elems {
			if i > 0 {
				g.emitPushLitToStrInline("%out", ", ")
			}
			fp := g.newReg()
			g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%p, i32 0, i32 %d", fp, tyStr, i)
			g.emitAppendValueToStrInline("%out", fp, el)
		}
		g.emitPushLitToStrInline("%out", "]")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align %d
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align %d
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, al, tyStr, al, mangled)
	}

	// 6. Cmp
	if CanCmpType(g.prog, tup) {
		g.funcSet["ll_cmp_p__"+mangled] = true
		g.funcSet["ll_cmp__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal i64 @ll_cmp_p__%s(ptr %%a, ptr %%b) {", mangled)
		g.line("entry:")
		if len(tup.Elems) == 0 {
			g.indentLine(1, "ret i64 0")
		} else {
			for i, el := range tup.Elems {
				sfA := g.newReg()
				sfB := g.newReg()
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%a, i32 0, i32 %d", sfA, tyStr, i)
				g.indentLine(1, "%s = getelementptr inbounds %s, ptr %%b, i32 0, i32 %d", sfB, tyStr, i)
				cRes := g.emitValueCmpInline(sfA, sfB, el)
				cond := g.newReg()
				g.indentLine(1, "%s = icmp ne i64 %s, 0", cond, cRes)
				lRet := g.newBlockLabel(fmt.Sprintf("cmp.ret.%d", i))
				lNext := g.newBlockLabel(fmt.Sprintf("cmp.next.%d", i))
				g.indentLine(1, "br i1 %s, label %%%s, label %%%s", cond, lRet, lNext)
				g.isTerminated = true
				g.emitLabel(lRet)
				g.indentLine(1, "ret i64 %s", cRes)
				g.emitLabel(lNext)
			}
			g.indentLine(1, "ret i64 0")
		}
		g.line("}")
		g.line(`define internal i64 @ll_cmp__%s(%s %%a, %s %%b) {
entry:
    %%pA = alloca %s, align %d
    store %s %%a, ptr %%pA, align %d
    %%pB = alloca %s, align %d
    store %s %%b, ptr %%pB, align %d
    %%r = call i64 @ll_cmp_p__%s(ptr %%pA, ptr %%pB)
    ret i64 %%r
}`, mangled, tyStr, tyStr, tyStr, al, tyStr, al, tyStr, al, tyStr, al, mangled)
	}
}

func (g *llvmEmitter) emitSetHelpersLLVM(set *types.Set) {
	mangled := set.Mangled()
	tyStr := g.llvmType(set)
	entTyStr := tyStr + "_ent"
	elem := set.Elem

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%s) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%s, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%chk")
	g.line("chk:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_elen = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%elen = load i64, ptr %%p_elen, align 8")
	if !elem.IsPlain() {
		g.indentLine(1, "%%has_e = icmp ne ptr %%e, null")
		g.indentLine(1, "br i1 %%has_e, label %%drop_loop, label %%free_ptrs")
		g.line("drop_loop:")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%elen, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%free_ptrs")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%ni", entTyStr)
		g.indentLine(1, "%%p_alive = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%alive = load i1, ptr %%p_alive, align 1")
		g.indentLine(1, "br i1 %%alive, label %%do_drop_k, label %%loop_cond")
		g.line("do_drop_k:")
		g.indentLine(1, "%%p_key = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		g.emitValueDropInline("%p_key", elem)
		g.indentLine(1, "br label %%loop_cond")
		g.line("free_ptrs:")
	}
	g.indentLine(1, "%%has_e2 = icmp ne ptr %%e, null")
	g.indentLine(1, "br i1 %%has_e2, label %%free_e, label %%chk_b")
	g.line("free_e:")
	g.indentLine(1, "call void @ll_free(ptr %%e)")
	g.indentLine(1, "br label %%chk_b")
	g.line("chk_b:")
	g.indentLine(1, "%%has_b = icmp ne ptr %%b, null")
	g.indentLine(1, "br i1 %%has_b, label %%free_b, label %%zero")
	g.line("free_b:")
	g.indentLine(1, "call void @ll_free(ptr %%b)")
	g.indentLine(1, "br label %%zero")
	g.line("zero:")
	g.indentLine(1, "store %s zeroinitializer, ptr %%s, align 8", tyStr)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, set) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align 8", tyStr)
		g.indentLine(1, "%%s_len_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%s_len = load i64, ptr %%s_len_p, align 8")
		g.indentLine(1, "%%nonempty = icmp sgt i64 %%s_len, 0")
		g.indentLine(1, "br i1 %%nonempty, label %%alloc, label %%done")
		g.line("alloc:")
		g.indentLine(1, "%%s_nb_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%s_e_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%s_el_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%s_ec_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 4", tyStr)
		g.indentLine(1, "%%nb = load i64, ptr %%s_nb_p, align 8")
		g.indentLine(1, "%%s_e = load ptr, ptr %%s_e_p, align 8")
		g.indentLine(1, "%%el = load i64, ptr %%s_el_p, align 8")
		g.indentLine(1, "%%ec = load i64, ptr %%s_ec_p, align 8")
		g.indentLine(1, "%%b_bytes = mul i64 %%nb, 8")
		g.indentLine(1, "%%nb_ptr = call ptr @ll_alloc(i64 %%b_bytes)")
		g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%nb_ptr, i8 -1, i64 %%b_bytes, i1 false)")
		g.indentLine(1, "%%e_sz_p = getelementptr %s, ptr null, i64 %%ec", entTyStr)
		g.indentLine(1, "%%e_bytes = ptrtoint ptr %%e_sz_p to i64")
		g.indentLine(1, "%%ne_ptr = call ptr @ll_alloc(i64 %%e_bytes)")
		g.indentLine(1, "%%d_b_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%d_nb_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%d_e_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%d_el_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%d_ec_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 4", tyStr)
		g.indentLine(1, "%%d_len_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 5", tyStr)
		g.indentLine(1, "store ptr %%nb_ptr, ptr %%d_b_p, align 8")
		g.indentLine(1, "store i64 %%nb, ptr %%d_nb_p, align 8")
		g.indentLine(1, "store ptr %%ne_ptr, ptr %%d_e_p, align 8")
		g.indentLine(1, "store i64 0, ptr %%d_el_p, align 8")
		g.indentLine(1, "store i64 %%ec, ptr %%d_ec_p, align 8")
		g.indentLine(1, "store i64 %%s_len, ptr %%d_len_p, align 8")
		g.indentLine(1, "%%mask = sub i64 %%nb, 1")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%sent = getelementptr inbounds %s, ptr %%s_e, i64 %%i", entTyStr)
		g.indentLine(1, "%%s_al_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%s_al_p, align 1")
		g.indentLine(1, "br i1 %%al, label %%copy_ent, label %%next_iter")
		g.line("copy_ent:")
		g.indentLine(1, "%%s_h_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 1", entTyStr)
		g.indentLine(1, "%%s_k_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "%%h = load i64, ptr %%s_h_p, align 8")
		g.indentLine(1, "%%di = load i64, ptr %%d_el_p, align 8")
		g.indentLine(1, "%%nd_el = add i64 %%di, 1")
		g.indentLine(1, "store i64 %%nd_el, ptr %%d_el_p, align 8")
		g.indentLine(1, "%%dent = getelementptr inbounds %s, ptr %%ne_ptr, i64 %%di", entTyStr)
		g.indentLine(1, "%%d_al_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%d_h_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 1", entTyStr)
		g.indentLine(1, "%%d_k_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "store i1 true, ptr %%d_al_p, align 1")
		g.indentLine(1, "store i64 %%h, ptr %%d_h_p, align 8")
		g.emitValueCopyInline("%d_k_p", "%s_k_p", elem)
		g.indentLine(1, "%%b_slot = alloca i64, align 8")
		g.indentLine(1, "%%b0 = and i64 %%h, %%mask")
		g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_cond")
		g.line("probe_cond:")
		g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
		g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%nb_ptr, i64 %%b_cur")
		g.indentLine(1, "%%bv = load i64, ptr %%bp, align 8")
		g.indentLine(1, "%%occ = icmp sge i64 %%bv, 0")
		g.indentLine(1, "br i1 %%occ, label %%probe_next, label %%probe_done")
		g.line("probe_next:")
		g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
		g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
		g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_cond")
		g.line("probe_done:")
		g.indentLine(1, "store i64 %%di, ptr %%bp, align 8")
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align 8
    %%dst = alloca %s, align 8
    store %s %%v, ptr %%src, align 8
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align 8
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, tyStr, tyStr, mangled, tyStr, tyStr)
	}

	// 3. ToString
	if CanToStringType(g.prog, set) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%s) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.emitPushLitToStrInline("%out", "#{")
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "%%first = alloca i1, align 1")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "store i1 true, ptr %%first, align 1")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%i", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%chk_first, label %%next_iter")
		g.line("chk_first:")
		g.indentLine(1, "%%f = load i1, ptr %%first, align 1")
		g.indentLine(1, "br i1 %%f, label %%mark_not_first, label %%add_sep")
		g.line("mark_not_first:")
		g.indentLine(1, "store i1 false, ptr %%first, align 1")
		g.indentLine(1, "br label %%add_elem")
		g.line("add_sep:")
		g.emitPushLitToStrInline("%out", ", ")
		g.indentLine(1, "br label %%add_elem")
		g.line("add_elem:")
		g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		g.emitAppendValueToStrInline("%out", "%p_k", elem)
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.emitPushLitToStrInline("%out", "}")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align 8
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align 8
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, tyStr, mangled)
	}
}

func (g *llvmEmitter) emitSetMethodsLLVM(set *types.Set) {
	mangled := set.Mangled()
	tyStr := g.llvmType(set)
	entTyStr := tyStr + "_ent"
	elem := set.Elem
	elemTyStr := g.llvmType(elem)
	elemAlign := g.typeAlign(elem)

	fns := []string{
		"ll_set_rehash__" + mangled,
		"ll_set_insert_p__" + mangled,
		"ll_set_insert__" + mangled,
		"ll_set_has_p__" + mangled,
		"ll_set_has__" + mangled,
		"ll_set_remove_p__" + mangled,
		"ll_set_remove__" + mangled,
		"ll_set_clear__" + mangled,
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	// 1. rehash
	g.nextRegID = 0
	g.line("define internal void @ll_set_rehash__%s(ptr %%s, i64 %%new_nb) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%b_bytes = mul i64 %%new_nb, 8")
	g.indentLine(1, "%%nb_ptr = call ptr @ll_alloc(i64 %%b_bytes)")
	g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%nb_ptr, i8 -1, i64 %%b_bytes, i1 false)")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%s, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_ec = getelementptr inbounds %s, ptr %%s, i32 0, i32 4", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%old_b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%old_e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%old_el = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%old_ec = load i64, ptr %%p_ec, align 8")
	g.indentLine(1, "%%s_len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%min_ec = mul i64 %%s_len, 2")
	g.indentLine(1, "%%c1 = icmp slt i64 %%old_ec, 8")
	g.indentLine(1, "%%ec1 = select i1 %%c1, i64 8, i64 %%old_ec")
	g.indentLine(1, "%%c2 = icmp slt i64 %%ec1, %%min_ec")
	g.indentLine(1, "%%new_ec = select i1 %%c2, i64 %%min_ec, i64 %%ec1")
	g.indentLine(1, "%%e_sz_p = getelementptr %s, ptr null, i64 %%new_ec", entTyStr)
	g.indentLine(1, "%%e_bytes = ptrtoint ptr %%e_sz_p to i64")
	g.indentLine(1, "%%ne_ptr = call ptr @ll_alloc(i64 %%e_bytes)")
	g.indentLine(1, "%%mask = sub i64 %%new_nb, 1")
	g.indentLine(1, "%%idx = alloca i64, align 8")
	g.indentLine(1, "%%new_el = alloca i64, align 8")
	g.indentLine(1, "store i64 0, ptr %%idx, align 8")
	g.indentLine(1, "store i64 0, ptr %%new_el, align 8")
	g.indentLine(1, "br label %%loop_cond")
	g.line("loop_cond:")
	g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
	g.indentLine(1, "%%more = icmp slt i64 %%i, %%old_el")
	g.indentLine(1, "br i1 %%more, label %%loop_body, label %%loop_done")
	g.line("loop_body:")
	g.indentLine(1, "%%sent = getelementptr inbounds %s, ptr %%old_e, i64 %%i", entTyStr)
	g.indentLine(1, "%%s_al_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%al = load i1, ptr %%s_al_p, align 1")
	g.indentLine(1, "br i1 %%al, label %%move_ent, label %%next_iter")
	g.line("move_ent:")
	g.indentLine(1, "%%di = load i64, ptr %%new_el, align 8")
	g.indentLine(1, "%%ndi = add i64 %%di, 1")
	g.indentLine(1, "store i64 %%ndi, ptr %%new_el, align 8")
	g.indentLine(1, "%%dent = getelementptr inbounds %s, ptr %%ne_ptr, i64 %%di", entTyStr)
	g.indentLine(1, "%%ev = load %s, ptr %%sent, align 8", entTyStr)
	g.indentLine(1, "store %s %%ev, ptr %%dent, align 8", entTyStr)
	g.indentLine(1, "%%s_h_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%h = load i64, ptr %%s_h_p, align 8")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %%h, %%mask")
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_cond")
	g.line("probe_cond:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%nb_ptr, i64 %%b_cur")
	g.indentLine(1, "%%bv = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%occ = icmp sge i64 %%bv, 0")
	g.indentLine(1, "br i1 %%occ, label %%probe_next, label %%probe_done")
	g.line("probe_next:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_cond")
	g.line("probe_done:")
	g.indentLine(1, "store i64 %%di, ptr %%bp, align 8")
	g.indentLine(1, "br label %%next_iter")
	g.line("next_iter:")
	g.indentLine(1, "%%ni = add i64 %%i, 1")
	g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
	g.indentLine(1, "br label %%loop_cond")
	g.line("loop_done:")
	g.indentLine(1, "%%has_ob = icmp ne ptr %%old_b, null")
	g.indentLine(1, "br i1 %%has_ob, label %%free_ob, label %%chk_oe")
	g.line("free_ob:")
	g.indentLine(1, "call void @ll_free(ptr %%old_b)")
	g.indentLine(1, "br label %%chk_oe")
	g.line("chk_oe:")
	g.indentLine(1, "%%has_oe = icmp ne ptr %%old_e, null")
	g.indentLine(1, "br i1 %%has_oe, label %%free_oe, label %%finish")
	g.line("free_oe:")
	g.indentLine(1, "call void @ll_free(ptr %%old_e)")
	g.indentLine(1, "br label %%finish")
	g.line("finish:")
	g.indentLine(1, "%%fin_el = load i64, ptr %%new_el, align 8")
	g.indentLine(1, "store ptr %%nb_ptr, ptr %%p_b, align 8")
	g.indentLine(1, "store i64 %%new_nb, ptr %%p_nb, align 8")
	g.indentLine(1, "store ptr %%ne_ptr, ptr %%p_e, align 8")
	g.indentLine(1, "store i64 %%fin_el, ptr %%p_el, align 8")
	g.indentLine(1, "store i64 %%new_ec, ptr %%p_ec, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. insert_p & insert
	g.nextRegID = 0
	g.line("define internal i1 @ll_set_insert_p__%s(ptr %%s, ptr %%key) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%s, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_ec = getelementptr inbounds %s, ptr %%s, i32 0, i32 4", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%nb0 = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%el0 = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%c_init = icmp eq i64 %%nb0, 0")
	g.indentLine(1, "%%el_p1 = add i64 %%el0, 1")
	g.indentLine(1, "%%lhs = mul i64 %%el_p1, 4")
	g.indentLine(1, "%%rhs = mul i64 %%nb0, 3")
	g.indentLine(1, "%%c_load = icmp sge i64 %%lhs, %%rhs")
	g.indentLine(1, "%%need_rehash = or i1 %%c_init, %%c_load")
	g.indentLine(1, "br i1 %%need_rehash, label %%do_rehash, label %%after_rehash")
	g.line("do_rehash:")
	g.indentLine(1, "%%nb_x2 = mul i64 %%nb0, 2")
	g.indentLine(1, "%%new_nb = select i1 %%c_init, i64 16, i64 %%nb_x2")
	g.indentLine(1, "call void @ll_set_rehash__%s(ptr %%s, i64 %%new_nb)", mangled)
	g.indentLine(1, "br label %%after_rehash")
	g.line("after_rehash:")
	hReg := g.emitValueHashInline("%key", elem)
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr0 = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%tomb_slot = alloca i64, align 8")
	g.indentLine(1, "%%b_init = and i64 %s, %%mask", hReg)
	g.indentLine(1, "store i64 %%b_init, ptr %%b_slot, align 8")
	g.indentLine(1, "store i64 -1, ptr %%tomb_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_empty = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_empty, label %%insert_new, label %%chk_tomb")
	g.line("chk_tomb:")
	g.indentLine(1, "%%is_tomb = icmp eq i64 %%idx, -2")
	g.indentLine(1, "br i1 %%is_tomb, label %%rec_tomb, label %%chk_match")
	g.line("rec_tomb:")
	g.indentLine(1, "%%t_cur = load i64, ptr %%tomb_slot, align 8")
	g.indentLine(1, "%%t_unset = icmp slt i64 %%t_cur, 0")
	g.indentLine(1, "%%t_new = select i1 %%t_unset, i64 %%b_cur, i64 %%t_cur")
	g.indentLine(1, "store i64 %%t_new, ptr %%tomb_slot, align 8")
	g.indentLine(1, "br label %%probe_adv")
	g.line("chk_match:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr0, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hReg)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_key, label %%probe_adv")
	g.line("cmp_key:")
	kEq := g.emitValueEqInline("%e_k_p", "%key", elem)
	g.indentLine(1, "br i1 %s, label %%found_dup, label %%probe_adv", kEq)
	g.line("found_dup:")
	if !elem.IsPlain() {
		g.emitValueDropInline("%key", elem)
	}
	g.indentLine(1, "ret i1 false")
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("insert_new:")
	g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%ec = load i64, ptr %%p_ec, align 8")
	g.indentLine(1, "%%need_e_grow = icmp sge i64 %%el, %%ec")
	g.indentLine(1, "br i1 %%need_e_grow, label %%grow_e, label %%write_e")
	g.line("grow_e:")
	g.indentLine(1, "%%ec_zero = icmp slt i64 %%ec, 8")
	g.indentLine(1, "%%ec_x2 = mul i64 %%ec, 2")
	g.indentLine(1, "%%new_ec = select i1 %%ec_zero, i64 8, i64 %%ec_x2")
	g.indentLine(1, "%%esz_p = getelementptr %s, ptr null, i64 %%new_ec", entTyStr)
	g.indentLine(1, "%%ebytes = ptrtoint ptr %%esz_p to i64")
	g.indentLine(1, "%%ne = call ptr @ll_realloc(ptr %%e_arr0, i64 %%ebytes)")
	g.indentLine(1, "store ptr %%ne, ptr %%p_e, align 8")
	g.indentLine(1, "store i64 %%new_ec, ptr %%p_ec, align 8")
	g.indentLine(1, "br label %%write_e")
	g.line("write_e:")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%nel = add i64 %%el, 1")
	g.indentLine(1, "store i64 %%nel, ptr %%p_el, align 8")
	g.indentLine(1, "%%nent = getelementptr inbounds %s, ptr %%e_arr, i64 %%el", entTyStr)
	g.indentLine(1, "%%n_al_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%n_h_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%n_k_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "store i1 true, ptr %%n_al_p, align 1")
	g.indentLine(1, "store i64 %s, ptr %%n_h_p, align 8", hReg)
	g.indentLine(1, "%%kval = load %s, ptr %%key, align %d", elemTyStr, elemAlign)
	g.indentLine(1, "store %s %%kval, ptr %%n_k_p, align %d", elemTyStr, elemAlign)
	g.indentLine(1, "%%t_fin = load i64, ptr %%tomb_slot, align 8")
	g.indentLine(1, "%%use_tomb = icmp sge i64 %%t_fin, 0")
	g.indentLine(1, "%%target_b = select i1 %%use_tomb, i64 %%t_fin, i64 %%b_cur")
	g.indentLine(1, "%%tbp = getelementptr inbounds i64, ptr %%b_arr, i64 %%target_b")
	g.indentLine(1, "store i64 %%el, ptr %%tbp, align 8")
	g.indentLine(1, "%%cur_len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nlen = add i64 %%cur_len, 1")
	g.indentLine(1, "store i64 %%nlen, ptr %%p_len, align 8")
	g.indentLine(1, "ret i1 true")
	g.line("}")

	g.line(`define internal i1 @ll_set_insert__%s(ptr %%s, %s %%key) {
entry:
    %%pk = alloca %s, align %d
    store %s %%key, ptr %%pk, align %d
    %%r = call i1 @ll_set_insert_p__%s(ptr %%s, ptr %%pk)
    ret i1 %%r
}`, mangled, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemAlign, mangled)

	// 3. has_p & has
	g.nextRegID = 0
	g.line("define internal i1 @ll_set_has_p__%s(ptr %%s, ptr %%key) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%s, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%c1 = icmp sle i64 %%len, 0")
	g.indentLine(1, "%%c2 = icmp sle i64 %%nb, 0")
	g.indentLine(1, "%%empty = or i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%empty, label %%ret_false, label %%search")
	g.line("search:")
	hHas := g.emitValueHashInline("%key", elem)
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %s, %%mask", hHas)
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_emp = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_emp, label %%ret_false, label %%chk_valid")
	g.line("chk_valid:")
	g.indentLine(1, "%%is_val = icmp sge i64 %%idx, 0")
	g.indentLine(1, "br i1 %%is_val, label %%chk_ent, label %%probe_adv")
	g.line("chk_ent:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hHas)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_k, label %%probe_adv")
	g.line("cmp_k:")
	eqHas := g.emitValueEqInline("%e_k_p", "%key", elem)
	g.indentLine(1, "br i1 %s, label %%ret_true, label %%probe_adv", eqHas)
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("ret_true:")
	g.indentLine(1, "ret i1 true")
	g.line("ret_false:")
	g.indentLine(1, "ret i1 false")
	g.line("}")

	g.line(`define internal i1 @ll_set_has__%s(%s %%s, %s %%key) {
entry:
    %%ps = alloca %s, align 8
    %%pk = alloca %s, align %d
    store %s %%s, ptr %%ps, align 8
    store %s %%key, ptr %%pk, align %d
    %%r = call i1 @ll_set_has_p__%s(ptr %%ps, ptr %%pk)
    ret i1 %%r
}`, mangled, tyStr, elemTyStr, tyStr, elemTyStr, elemAlign, tyStr, elemTyStr, elemAlign, mangled)

	// 4. remove_p & remove
	g.nextRegID = 0
	g.line("define internal i1 @ll_set_remove_p__%s(ptr %%s, ptr %%key) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%s, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%c1 = icmp sle i64 %%len, 0")
	g.indentLine(1, "%%c2 = icmp sle i64 %%nb, 0")
	g.indentLine(1, "%%empty = or i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%empty, label %%ret_false, label %%search")
	g.line("search:")
	hRem := g.emitValueHashInline("%key", elem)
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %s, %%mask", hRem)
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_emp = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_emp, label %%ret_false, label %%chk_valid")
	g.line("chk_valid:")
	g.indentLine(1, "%%is_val = icmp sge i64 %%idx, 0")
	g.indentLine(1, "br i1 %%is_val, label %%chk_ent, label %%probe_adv")
	g.line("chk_ent:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hRem)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_k, label %%probe_adv")
	g.line("cmp_k:")
	eqRem := g.emitValueEqInline("%e_k_p", "%key", elem)
	g.indentLine(1, "br i1 %s, label %%do_remove, label %%probe_adv", eqRem)
	g.line("do_remove:")
	g.indentLine(1, "store i1 false, ptr %%e_al_p, align 1")
	if !elem.IsPlain() {
		g.emitValueDropInline("%e_k_p", elem)
	}
	g.indentLine(1, "store i64 -2, ptr %%bp, align 8")
	g.indentLine(1, "%%nlen = sub i64 %%len, 1")
	g.indentLine(1, "store i64 %%nlen, ptr %%p_len, align 8")
	g.indentLine(1, "ret i1 true")
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("ret_false:")
	g.indentLine(1, "ret i1 false")
	g.line("}")

	g.line(`define internal i1 @ll_set_remove__%s(ptr %%s, %s %%key) {
entry:
    %%pk = alloca %s, align %d
    store %s %%key, ptr %%pk, align %d
    %%r = call i1 @ll_set_remove_p__%s(ptr %%s, ptr %%pk)
    ret i1 %%r
}`, mangled, elemTyStr, elemTyStr, elemAlign, elemTyStr, elemAlign, mangled)

	// 5. clear
	g.nextRegID = 0
	g.line("define internal void @ll_set_clear__%s(ptr %%s) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%s, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%s, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
	if !elem.IsPlain() {
		g.indentLine(1, "%%has_e = icmp ne ptr %%e, null")
		g.indentLine(1, "br i1 %%has_e, label %%drop_loop, label %%reset_b")
		g.line("drop_loop:")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%el, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%reset_b")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%ni", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%do_drop, label %%loop_cond")
		g.line("do_drop:")
		g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		g.emitValueDropInline("%p_k", elem)
		g.indentLine(1, "store i1 false, ptr %%p_al, align 1")
		g.indentLine(1, "br label %%loop_cond")
		g.line("reset_b:")
	}
	g.indentLine(1, "%%has_b = icmp ne ptr %%b, null")
	g.indentLine(1, "%%pos_nb = icmp sgt i64 %%nb, 0")
	g.indentLine(1, "%%do_memset = and i1 %%has_b, %%pos_nb")
	g.indentLine(1, "br i1 %%do_memset, label %%memset_b, label %%finish")
	g.line("memset_b:")
	g.indentLine(1, "%%b_bytes = mul i64 %%nb, 8")
	g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%b, i8 -1, i64 %%b_bytes, i1 false)")
	g.indentLine(1, "br label %%finish")
	g.line("finish:")
	g.indentLine(1, "store i64 0, ptr %%p_el, align 8")
	g.indentLine(1, "store i64 0, ptr %%p_len, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	vecElem := &types.Vector{Elem: set.Elem}
	if g.hasVectorType(vecElem) && CanCopyType(g.prog, elem) {
		vecTyStr := g.llvmType(vecElem)
		vm := vecElem.Mangled()
		fnName := "ll_set_to_vec__" + mangled
		g.funcSet[fnName] = true
		g.nextRegID = 0
		g.line("define internal %s @%s(ptr %%s) {", vecTyStr, fnName)
		g.line("entry:")
		g.indentLine(1, "%%out = alloca %s, align 8", vecTyStr)
		g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%s, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%s_len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%has_items = icmp sgt i64 %%s_len, 0")
		g.indentLine(1, "br i1 %%has_items, label %%reserve, label %%check_loop")
		g.line("reserve:")
		g.indentLine(1, "call void @ll_vec_reserve__%s(ptr %%out, i64 %%s_len)", vm)
		g.indentLine(1, "br label %%check_loop")
		g.line("check_loop:")
		g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%s, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%s, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
		g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%finish")
		g.line("loop_body:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%i", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%push_item, label %%next_iter")
		g.line("push_item:")
		g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		if elem.IsPlain() {
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%p_k)", vm)
		} else {
			g.indentLine(1, "%%copied_k = alloca %s, align %d", elemTyStr, elemAlign)
			g.emitValueCopyInline("%copied_k", "%p_k", elem)
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%copied_k)", vm)
		}
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("finish:")
		g.indentLine(1, "%%r = load %s, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "ret %s %%r", vecTyStr)
		g.line("}")
	}
}

func (g *llvmEmitter) emitMapHelpersLLVM(mp *types.Map) {
	mangled := mp.Mangled()
	tyStr := g.llvmType(mp)
	entTyStr := tyStr + "_ent"
	keyTy := mp.Key
	valTy := mp.Value

	// 1. Drop
	g.funcSet["ll_drop__"+mangled] = true
	g.nextRegID = 0
	g.line("define internal void @ll_drop__%s(ptr %%m) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%is_null = icmp eq ptr %%m, null")
	g.indentLine(1, "br i1 %%is_null, label %%done, label %%chk")
	g.line("chk:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
	if !keyTy.IsPlain() || !valTy.IsPlain() {
		g.indentLine(1, "%%has_e = icmp ne ptr %%e, null")
		g.indentLine(1, "br i1 %%has_e, label %%drop_loop, label %%free_ptrs")
		g.line("drop_loop:")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%el, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%free_ptrs")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%ni", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%do_drop_kv, label %%loop_cond")
		g.line("do_drop_kv:")
		if !valTy.IsPlain() {
			g.indentLine(1, "%%p_v = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
			g.emitValueDropInline("%p_v", valTy)
		}
		if !keyTy.IsPlain() {
			g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
			g.emitValueDropInline("%p_k", keyTy)
		}
		g.indentLine(1, "br label %%loop_cond")
		g.line("free_ptrs:")
	}
	g.indentLine(1, "%%has_e2 = icmp ne ptr %%e, null")
	g.indentLine(1, "br i1 %%has_e2, label %%free_e, label %%chk_b")
	g.line("free_e:")
	g.indentLine(1, "call void @ll_free(ptr %%e)")
	g.indentLine(1, "br label %%chk_b")
	g.line("chk_b:")
	g.indentLine(1, "%%has_b = icmp ne ptr %%b, null")
	g.indentLine(1, "br i1 %%has_b, label %%free_b, label %%zero")
	g.line("free_b:")
	g.indentLine(1, "call void @ll_free(ptr %%b)")
	g.indentLine(1, "br label %%zero")
	g.line("zero:")
	g.indentLine(1, "store %s zeroinitializer, ptr %%m, align 8", tyStr)
	g.indentLine(1, "br label %%done")
	g.line("done:")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. Copy
	if CanCopyType(g.prog, mp) {
		g.funcSet["ll_copy_p__"+mangled] = true
		g.funcSet["ll_copy__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_copy_p__%s(ptr %%dst, ptr %%src) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %s zeroinitializer, ptr %%dst, align 8", tyStr)
		g.indentLine(1, "%%s_len_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%s_len = load i64, ptr %%s_len_p, align 8")
		g.indentLine(1, "%%nonempty = icmp sgt i64 %%s_len, 0")
		g.indentLine(1, "br i1 %%nonempty, label %%alloc, label %%done")
		g.line("alloc:")
		g.indentLine(1, "%%s_nb_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%s_e_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%s_el_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%s_ec_p = getelementptr inbounds %s, ptr %%src, i32 0, i32 4", tyStr)
		g.indentLine(1, "%%nb = load i64, ptr %%s_nb_p, align 8")
		g.indentLine(1, "%%s_e = load ptr, ptr %%s_e_p, align 8")
		g.indentLine(1, "%%el = load i64, ptr %%s_el_p, align 8")
		g.indentLine(1, "%%ec = load i64, ptr %%s_ec_p, align 8")
		g.indentLine(1, "%%b_bytes = mul i64 %%nb, 8")
		g.indentLine(1, "%%nb_ptr = call ptr @ll_alloc(i64 %%b_bytes)")
		g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%nb_ptr, i8 -1, i64 %%b_bytes, i1 false)")
		g.indentLine(1, "%%e_sz_p = getelementptr %s, ptr null, i64 %%ec", entTyStr)
		g.indentLine(1, "%%e_bytes = ptrtoint ptr %%e_sz_p to i64")
		g.indentLine(1, "%%ne_ptr = call ptr @ll_alloc(i64 %%e_bytes)")
		g.indentLine(1, "%%d_b_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%d_nb_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%d_e_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%d_el_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%d_ec_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 4", tyStr)
		g.indentLine(1, "%%d_len_p = getelementptr inbounds %s, ptr %%dst, i32 0, i32 5", tyStr)
		g.indentLine(1, "store ptr %%nb_ptr, ptr %%d_b_p, align 8")
		g.indentLine(1, "store i64 %%nb, ptr %%d_nb_p, align 8")
		g.indentLine(1, "store ptr %%ne_ptr, ptr %%d_e_p, align 8")
		g.indentLine(1, "store i64 0, ptr %%d_el_p, align 8")
		g.indentLine(1, "store i64 %%ec, ptr %%d_ec_p, align 8")
		g.indentLine(1, "store i64 %%s_len, ptr %%d_len_p, align 8")
		g.indentLine(1, "%%mask = sub i64 %%nb, 1")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%sent = getelementptr inbounds %s, ptr %%s_e, i64 %%i", entTyStr)
		g.indentLine(1, "%%s_al_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%s_al_p, align 1")
		g.indentLine(1, "br i1 %%al, label %%copy_ent, label %%next_iter")
		g.line("copy_ent:")
		g.indentLine(1, "%%s_h_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 1", entTyStr)
		g.indentLine(1, "%%s_k_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "%%s_v_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 3", entTyStr)
		g.indentLine(1, "%%h = load i64, ptr %%s_h_p, align 8")
		g.indentLine(1, "%%di = load i64, ptr %%d_el_p, align 8")
		g.indentLine(1, "%%nd_el = add i64 %%di, 1")
		g.indentLine(1, "store i64 %%nd_el, ptr %%d_el_p, align 8")
		g.indentLine(1, "%%dent = getelementptr inbounds %s, ptr %%ne_ptr, i64 %%di", entTyStr)
		g.indentLine(1, "%%d_al_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%d_h_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 1", entTyStr)
		g.indentLine(1, "%%d_k_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "%%d_v_p = getelementptr inbounds %s, ptr %%dent, i32 0, i32 3", entTyStr)
		g.indentLine(1, "store i1 true, ptr %%d_al_p, align 1")
		g.indentLine(1, "store i64 %%h, ptr %%d_h_p, align 8")
		g.emitValueCopyInline("%d_k_p", "%s_k_p", keyTy)
		g.emitValueCopyInline("%d_v_p", "%s_v_p", valTy)
		g.indentLine(1, "%%b_slot = alloca i64, align 8")
		g.indentLine(1, "%%b0 = and i64 %%h, %%mask")
		g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_cond")
		g.line("probe_cond:")
		g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
		g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%nb_ptr, i64 %%b_cur")
		g.indentLine(1, "%%bv = load i64, ptr %%bp, align 8")
		g.indentLine(1, "%%occ = icmp sge i64 %%bv, 0")
		g.indentLine(1, "br i1 %%occ, label %%probe_next, label %%probe_done")
		g.line("probe_next:")
		g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
		g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
		g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_cond")
		g.line("probe_done:")
		g.indentLine(1, "store i64 %%di, ptr %%bp, align 8")
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %s @ll_copy__%s(%s %%v) {
entry:
    %%src = alloca %s, align 8
    %%dst = alloca %s, align 8
    store %s %%v, ptr %%src, align 8
    call void @ll_copy_p__%s(ptr %%dst, ptr %%src)
    %%r = load %s, ptr %%dst, align 8
    ret %s %%r
}`, tyStr, mangled, tyStr, tyStr, tyStr, tyStr, mangled, tyStr, tyStr)
	}

	// 3. ToString
	if CanToStringType(g.prog, mp) {
		g.funcSet["ll_tostr_p__"+mangled] = true
		g.funcSet["ll_tostr__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_tostr_p__%s(ptr %%out, ptr %%m) {", mangled)
		g.line("entry:")
		g.indentLine(1, "store %%struct.ll_string zeroinitializer, ptr %%out, align 8")
		g.emitPushLitToStrInline("%out", "#[")
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "%%first = alloca i1, align 1")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "store i1 true, ptr %%first, align 1")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%done")
		g.line("loop_body:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%i", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%chk_first, label %%next_iter")
		g.line("chk_first:")
		g.indentLine(1, "%%f = load i1, ptr %%first, align 1")
		g.indentLine(1, "br i1 %%f, label %%mark_not_first, label %%add_sep")
		g.line("mark_not_first:")
		g.indentLine(1, "store i1 false, ptr %%first, align 1")
		g.indentLine(1, "br label %%add_kv")
		g.line("add_sep:")
		g.emitPushLitToStrInline("%out", ", ")
		g.indentLine(1, "br label %%add_kv")
		g.line("add_kv:")
		g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "%%p_v = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
		g.emitAppendValueToStrInline("%out", "%p_k", keyTy)
		g.emitPushLitToStrInline("%out", ": ")
		g.emitAppendValueToStrInline("%out", "%p_v", valTy)
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("done:")
		g.emitPushLitToStrInline("%out", "]")
		g.indentLine(1, "ret void")
		g.line("}")
		g.line(`define internal %%struct.ll_string @ll_tostr__%s(%s %%v) {
entry:
    %%p = alloca %s, align 8
    %%out = alloca %%struct.ll_string, align 8
    store %s %%v, ptr %%p, align 8
    call void @ll_tostr_p__%s(ptr %%out, ptr %%p)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, mangled, tyStr, tyStr, tyStr, mangled)
	}
}

func (g *llvmEmitter) emitMapMethodsLLVM(mp *types.Map) {
	mangled := mp.Mangled()
	tyStr := g.llvmType(mp)
	entTyStr := tyStr + "_ent"
	keyTy := mp.Key
	valTy := mp.Value
	keyTyStr := g.llvmType(keyTy)
	valTyStr := g.llvmType(valTy)
	keyAlign := g.typeAlign(keyTy)
	valAlign := g.typeAlign(valTy)
	optVal := types.NewOptional(valTy)
	optValTyStr := g.llvmType(optVal)

	fns := []string{
		"ll_map_rehash__" + mangled,
		"ll_map_insert_p__" + mangled,
		"ll_map_insert__" + mangled,
		"ll_map_has_p__" + mangled,
		"ll_map_has__" + mangled,
		"ll_map_remove_p__" + mangled,
		"ll_map_remove__" + mangled,
		"ll_map_clear__" + mangled,
	}
	for _, fn := range fns {
		g.funcSet[fn] = true
	}

	// 1. rehash
	g.nextRegID = 0
	g.line("define internal void @ll_map_rehash__%s(ptr %%m, i64 %%new_nb) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%b_bytes = mul i64 %%new_nb, 8")
	g.indentLine(1, "%%nb_ptr = call ptr @ll_alloc(i64 %%b_bytes)")
	g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%nb_ptr, i8 -1, i64 %%b_bytes, i1 false)")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_ec = getelementptr inbounds %s, ptr %%m, i32 0, i32 4", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%old_b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%old_e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%old_el = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%old_ec = load i64, ptr %%p_ec, align 8")
	g.indentLine(1, "%%m_len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%min_ec = mul i64 %%m_len, 2")
	g.indentLine(1, "%%c1 = icmp slt i64 %%old_ec, 8")
	g.indentLine(1, "%%ec1 = select i1 %%c1, i64 8, i64 %%old_ec")
	g.indentLine(1, "%%c2 = icmp slt i64 %%ec1, %%min_ec")
	g.indentLine(1, "%%new_ec = select i1 %%c2, i64 %%min_ec, i64 %%ec1")
	g.indentLine(1, "%%e_sz_p = getelementptr %s, ptr null, i64 %%new_ec", entTyStr)
	g.indentLine(1, "%%e_bytes = ptrtoint ptr %%e_sz_p to i64")
	g.indentLine(1, "%%ne_ptr = call ptr @ll_alloc(i64 %%e_bytes)")
	g.indentLine(1, "%%mask = sub i64 %%new_nb, 1")
	g.indentLine(1, "%%idx = alloca i64, align 8")
	g.indentLine(1, "%%new_el = alloca i64, align 8")
	g.indentLine(1, "store i64 0, ptr %%idx, align 8")
	g.indentLine(1, "store i64 0, ptr %%new_el, align 8")
	g.indentLine(1, "br label %%loop_cond")
	g.line("loop_cond:")
	g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
	g.indentLine(1, "%%more = icmp slt i64 %%i, %%old_el")
	g.indentLine(1, "br i1 %%more, label %%loop_body, label %%loop_done")
	g.line("loop_body:")
	g.indentLine(1, "%%sent = getelementptr inbounds %s, ptr %%old_e, i64 %%i", entTyStr)
	g.indentLine(1, "%%s_al_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%al = load i1, ptr %%s_al_p, align 1")
	g.indentLine(1, "br i1 %%al, label %%move_ent, label %%next_iter")
	g.line("move_ent:")
	g.indentLine(1, "%%di = load i64, ptr %%new_el, align 8")
	g.indentLine(1, "%%ndi = add i64 %%di, 1")
	g.indentLine(1, "store i64 %%ndi, ptr %%new_el, align 8")
	g.indentLine(1, "%%dent = getelementptr inbounds %s, ptr %%ne_ptr, i64 %%di", entTyStr)
	g.indentLine(1, "%%ev = load %s, ptr %%sent, align 8", entTyStr)
	g.indentLine(1, "store %s %%ev, ptr %%dent, align 8", entTyStr)
	g.indentLine(1, "%%s_h_p = getelementptr inbounds %s, ptr %%sent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%h = load i64, ptr %%s_h_p, align 8")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %%h, %%mask")
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_cond")
	g.line("probe_cond:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%nb_ptr, i64 %%b_cur")
	g.indentLine(1, "%%bv = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%occ = icmp sge i64 %%bv, 0")
	g.indentLine(1, "br i1 %%occ, label %%probe_next, label %%probe_done")
	g.line("probe_next:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_cond")
	g.line("probe_done:")
	g.indentLine(1, "store i64 %%di, ptr %%bp, align 8")
	g.indentLine(1, "br label %%next_iter")
	g.line("next_iter:")
	g.indentLine(1, "%%ni = add i64 %%i, 1")
	g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
	g.indentLine(1, "br label %%loop_cond")
	g.line("loop_done:")
	g.indentLine(1, "%%has_ob = icmp ne ptr %%old_b, null")
	g.indentLine(1, "br i1 %%has_ob, label %%free_ob, label %%chk_oe")
	g.line("free_ob:")
	g.indentLine(1, "call void @ll_free(ptr %%old_b)")
	g.indentLine(1, "br label %%chk_oe")
	g.line("chk_oe:")
	g.indentLine(1, "%%has_oe = icmp ne ptr %%old_e, null")
	g.indentLine(1, "br i1 %%has_oe, label %%free_oe, label %%finish")
	g.line("free_oe:")
	g.indentLine(1, "call void @ll_free(ptr %%old_e)")
	g.indentLine(1, "br label %%finish")
	g.line("finish:")
	g.indentLine(1, "%%fin_el = load i64, ptr %%new_el, align 8")
	g.indentLine(1, "store ptr %%nb_ptr, ptr %%p_b, align 8")
	g.indentLine(1, "store i64 %%new_nb, ptr %%p_nb, align 8")
	g.indentLine(1, "store ptr %%ne_ptr, ptr %%p_e, align 8")
	g.indentLine(1, "store i64 %%fin_el, ptr %%p_el, align 8")
	g.indentLine(1, "store i64 %%new_ec, ptr %%p_ec, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	// 2. insert_p & insert
	g.nextRegID = 0
	g.line("define internal void @ll_map_insert_p__%s(ptr %%m, ptr %%key, ptr %%val) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_ec = getelementptr inbounds %s, ptr %%m, i32 0, i32 4", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%nb0 = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%el0 = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%c_init = icmp eq i64 %%nb0, 0")
	g.indentLine(1, "%%el_p1 = add i64 %%el0, 1")
	g.indentLine(1, "%%lhs = mul i64 %%el_p1, 4")
	g.indentLine(1, "%%rhs = mul i64 %%nb0, 3")
	g.indentLine(1, "%%c_load = icmp sge i64 %%lhs, %%rhs")
	g.indentLine(1, "%%need_rehash = or i1 %%c_init, %%c_load")
	g.indentLine(1, "br i1 %%need_rehash, label %%do_rehash, label %%after_rehash")
	g.line("do_rehash:")
	g.indentLine(1, "%%nb_x2 = mul i64 %%nb0, 2")
	g.indentLine(1, "%%new_nb = select i1 %%c_init, i64 16, i64 %%nb_x2")
	g.indentLine(1, "call void @ll_map_rehash__%s(ptr %%m, i64 %%new_nb)", mangled)
	g.indentLine(1, "br label %%after_rehash")
	g.line("after_rehash:")
	hReg := g.emitValueHashInline("%key", keyTy)
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr0 = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%tomb_slot = alloca i64, align 8")
	g.indentLine(1, "%%b_init = and i64 %s, %%mask", hReg)
	g.indentLine(1, "store i64 %%b_init, ptr %%b_slot, align 8")
	g.indentLine(1, "store i64 -1, ptr %%tomb_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_empty = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_empty, label %%insert_new, label %%chk_tomb")
	g.line("chk_tomb:")
	g.indentLine(1, "%%is_tomb = icmp eq i64 %%idx, -2")
	g.indentLine(1, "br i1 %%is_tomb, label %%rec_tomb, label %%chk_match")
	g.line("rec_tomb:")
	g.indentLine(1, "%%t_cur = load i64, ptr %%tomb_slot, align 8")
	g.indentLine(1, "%%t_unset = icmp slt i64 %%t_cur, 0")
	g.indentLine(1, "%%t_new = select i1 %%t_unset, i64 %%b_cur, i64 %%t_cur")
	g.indentLine(1, "store i64 %%t_new, ptr %%tomb_slot, align 8")
	g.indentLine(1, "br label %%probe_adv")
	g.line("chk_match:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr0, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_v_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hReg)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_key, label %%probe_adv")
	g.line("cmp_key:")
	kEq := g.emitValueEqInline("%e_k_p", "%key", keyTy)
	g.indentLine(1, "br i1 %s, label %%overwrite_val, label %%probe_adv", kEq)
	g.line("overwrite_val:")
	if !keyTy.IsPlain() {
		g.emitValueDropInline("%key", keyTy)
	}
	if !valTy.IsPlain() {
		g.emitValueDropInline("%e_v_p", valTy)
	}
	g.indentLine(1, "%%v_ow = load %s, ptr %%val, align %d", valTyStr, valAlign)
	g.indentLine(1, "store %s %%v_ow, ptr %%e_v_p, align %d", valTyStr, valAlign)
	g.indentLine(1, "ret void")
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("insert_new:")
	g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
	g.indentLine(1, "%%ec = load i64, ptr %%p_ec, align 8")
	g.indentLine(1, "%%need_e_grow = icmp sge i64 %%el, %%ec")
	g.indentLine(1, "br i1 %%need_e_grow, label %%grow_e, label %%write_e")
	g.line("grow_e:")
	g.indentLine(1, "%%ec_zero = icmp slt i64 %%ec, 8")
	g.indentLine(1, "%%ec_x2 = mul i64 %%ec, 2")
	g.indentLine(1, "%%new_ec = select i1 %%ec_zero, i64 8, i64 %%ec_x2")
	g.indentLine(1, "%%esz_p = getelementptr %s, ptr null, i64 %%new_ec", entTyStr)
	g.indentLine(1, "%%ebytes = ptrtoint ptr %%esz_p to i64")
	g.indentLine(1, "%%ne = call ptr @ll_realloc(ptr %%e_arr0, i64 %%ebytes)")
	g.indentLine(1, "store ptr %%ne, ptr %%p_e, align 8")
	g.indentLine(1, "store i64 %%new_ec, ptr %%p_ec, align 8")
	g.indentLine(1, "br label %%write_e")
	g.line("write_e:")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%nel = add i64 %%el, 1")
	g.indentLine(1, "store i64 %%nel, ptr %%p_el, align 8")
	g.indentLine(1, "%%nent = getelementptr inbounds %s, ptr %%e_arr, i64 %%el", entTyStr)
	g.indentLine(1, "%%n_al_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%n_h_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%n_k_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%n_v_p = getelementptr inbounds %s, ptr %%nent, i32 0, i32 3", entTyStr)
	g.indentLine(1, "store i1 true, ptr %%n_al_p, align 1")
	g.indentLine(1, "store i64 %s, ptr %%n_h_p, align 8", hReg)
	g.indentLine(1, "%%kval = load %s, ptr %%key, align %d", keyTyStr, keyAlign)
	g.indentLine(1, "%%vval = load %s, ptr %%val, align %d", valTyStr, valAlign)
	g.indentLine(1, "store %s %%kval, ptr %%n_k_p, align %d", keyTyStr, keyAlign)
	g.indentLine(1, "store %s %%vval, ptr %%n_v_p, align %d", valTyStr, valAlign)
	g.indentLine(1, "%%t_fin = load i64, ptr %%tomb_slot, align 8")
	g.indentLine(1, "%%use_tomb = icmp sge i64 %%t_fin, 0")
	g.indentLine(1, "%%target_b = select i1 %%use_tomb, i64 %%t_fin, i64 %%b_cur")
	g.indentLine(1, "%%tbp = getelementptr inbounds i64, ptr %%b_arr, i64 %%target_b")
	g.indentLine(1, "store i64 %%el, ptr %%tbp, align 8")
	g.indentLine(1, "%%cur_len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nlen = add i64 %%cur_len, 1")
	g.indentLine(1, "store i64 %%nlen, ptr %%p_len, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	g.line(`define internal void @ll_map_insert__%s(ptr %%m, %s %%key, %s %%val) {
entry:
    %%pk = alloca %s, align %d
    %%pv = alloca %s, align %d
    store %s %%key, ptr %%pk, align %d
    store %s %%val, ptr %%pv, align %d
    call void @ll_map_insert_p__%s(ptr %%m, ptr %%pk, ptr %%pv)
    ret void
}`, mangled, keyTyStr, valTyStr, keyTyStr, keyAlign, valTyStr, valAlign, keyTyStr, keyAlign, valTyStr, valAlign, mangled)

	// 3. get_p & get (only if CanCopyType(valTy))
	if CanCopyType(g.prog, valTy) {
		g.funcSet["ll_map_get_p__"+mangled] = true
		g.funcSet["ll_map_get__"+mangled] = true
		g.nextRegID = 0
		g.line("define internal void @ll_map_get_p__%s(ptr %%out, ptr %%m, ptr %%key) {", mangled)
		g.line("entry:")
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
		g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
		g.indentLine(1, "%%c1 = icmp sle i64 %%len, 0")
		g.indentLine(1, "%%c2 = icmp sle i64 %%nb, 0")
		g.indentLine(1, "%%empty = or i1 %%c1, %%c2")
		g.indentLine(1, "br i1 %%empty, label %%ret_none, label %%search")
		g.line("search:")
		hGet := g.emitValueHashInline("%key", keyTy)
		g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
		g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%mask = sub i64 %%nb, 1")
		g.indentLine(1, "%%b_slot = alloca i64, align 8")
		g.indentLine(1, "%%b0 = and i64 %s, %%mask", hGet)
		g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_loop")
		g.line("probe_loop:")
		g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
		g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
		g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
		g.indentLine(1, "%%is_emp = icmp eq i64 %%idx, -1")
		g.indentLine(1, "br i1 %%is_emp, label %%ret_none, label %%chk_valid")
		g.line("chk_valid:")
		g.indentLine(1, "%%is_val = icmp sge i64 %%idx, 0")
		g.indentLine(1, "br i1 %%is_val, label %%chk_ent, label %%probe_adv")
		g.line("chk_ent:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%idx", entTyStr)
		g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
		g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		g.indentLine(1, "%%e_v_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
		g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
		g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
		g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hGet)
		g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
		g.indentLine(1, "br i1 %%cand, label %%cmp_k, label %%probe_adv")
		g.line("cmp_k:")
		eqGet := g.emitValueEqInline("%e_k_p", "%key", keyTy)
		g.indentLine(1, "br i1 %s, label %%found_val, label %%probe_adv", eqGet)
		g.line("found_val:")
		g.emitStoreOptSome("%out", "%e_v_p", true, valTy, optVal)
		g.indentLine(1, "ret void")
		g.line("probe_adv:")
		g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
		g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
		g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
		g.indentLine(1, "br label %%probe_loop")
		g.line("ret_none:")
		if optVal.IsBoxed() {
			g.indentLine(1, "store ptr null, ptr %%out, align 8")
		} else {
			g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", optValTyStr)
		}
		g.indentLine(1, "ret void")
		g.line("}")

		g.line(`define internal %s @ll_map_get__%s(%s %%m, %s %%key) {
entry:
    %%pm = alloca %s, align 8
    %%pk = alloca %s, align %d
    %%out = alloca %s, align 8
    store %s %%m, ptr %%pm, align 8
    store %s %%key, ptr %%pk, align %d
    call void @ll_map_get_p__%s(ptr %%out, ptr %%pm, ptr %%pk)
    %%r = load %s, ptr %%out, align 8
    ret %s %%r
}`, optValTyStr, mangled, tyStr, keyTyStr, tyStr, keyTyStr, keyAlign, optValTyStr, tyStr, keyTyStr, keyAlign, mangled, optValTyStr, optValTyStr)
	}

	// 4. has_p & has
	g.nextRegID = 0
	g.line("define internal i1 @ll_map_has_p__%s(ptr %%m, ptr %%key) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%c1 = icmp sle i64 %%len, 0")
	g.indentLine(1, "%%c2 = icmp sle i64 %%nb, 0")
	g.indentLine(1, "%%empty = or i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%empty, label %%ret_false, label %%search")
	g.line("search:")
	hHas := g.emitValueHashInline("%key", keyTy)
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %s, %%mask", hHas)
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_emp = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_emp, label %%ret_false, label %%chk_valid")
	g.line("chk_valid:")
	g.indentLine(1, "%%is_val = icmp sge i64 %%idx, 0")
	g.indentLine(1, "br i1 %%is_val, label %%chk_ent, label %%probe_adv")
	g.line("chk_ent:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hHas)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_k, label %%probe_adv")
	g.line("cmp_k:")
	eqHas := g.emitValueEqInline("%e_k_p", "%key", keyTy)
	g.indentLine(1, "br i1 %s, label %%ret_true, label %%probe_adv", eqHas)
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("ret_true:")
	g.indentLine(1, "ret i1 true")
	g.line("ret_false:")
	g.indentLine(1, "ret i1 false")
	g.line("}")

	g.line(`define internal i1 @ll_map_has__%s(%s %%m, %s %%key) {
entry:
    %%pm = alloca %s, align 8
    %%pk = alloca %s, align %d
    store %s %%m, ptr %%pm, align 8
    store %s %%key, ptr %%pk, align %d
    %%r = call i1 @ll_map_has_p__%s(ptr %%pm, ptr %%pk)
    ret i1 %%r
}`, mangled, tyStr, keyTyStr, tyStr, keyTyStr, keyAlign, tyStr, keyTyStr, keyAlign, mangled)

	// 5. remove_p & remove
	g.nextRegID = 0
	g.line("define internal void @ll_map_remove_p__%s(ptr %%out, ptr %%m, ptr %%key) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%len = load i64, ptr %%p_len, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%c1 = icmp sle i64 %%len, 0")
	g.indentLine(1, "%%c2 = icmp sle i64 %%nb, 0")
	g.indentLine(1, "%%empty = or i1 %%c1, %%c2")
	g.indentLine(1, "br i1 %%empty, label %%ret_none, label %%search")
	g.line("search:")
	hRem := g.emitValueHashInline("%key", keyTy)
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%b_arr = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%mask = sub i64 %%nb, 1")
	g.indentLine(1, "%%b_slot = alloca i64, align 8")
	g.indentLine(1, "%%b0 = and i64 %s, %%mask", hRem)
	g.indentLine(1, "store i64 %%b0, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("probe_loop:")
	g.indentLine(1, "%%b_cur = load i64, ptr %%b_slot, align 8")
	g.indentLine(1, "%%bp = getelementptr inbounds i64, ptr %%b_arr, i64 %%b_cur")
	g.indentLine(1, "%%idx = load i64, ptr %%bp, align 8")
	g.indentLine(1, "%%is_emp = icmp eq i64 %%idx, -1")
	g.indentLine(1, "br i1 %%is_emp, label %%ret_none, label %%chk_valid")
	g.line("chk_valid:")
	g.indentLine(1, "%%is_val = icmp sge i64 %%idx, 0")
	g.indentLine(1, "br i1 %%is_val, label %%chk_ent, label %%probe_adv")
	g.line("chk_ent:")
	g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%idx", entTyStr)
	g.indentLine(1, "%%e_al_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
	g.indentLine(1, "%%e_h_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 1", entTyStr)
	g.indentLine(1, "%%e_k_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
	g.indentLine(1, "%%e_v_p = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
	g.indentLine(1, "%%e_al = load i1, ptr %%e_al_p, align 1")
	g.indentLine(1, "%%e_h = load i64, ptr %%e_h_p, align 8")
	g.indentLine(1, "%%h_eq = icmp eq i64 %%e_h, %s", hRem)
	g.indentLine(1, "%%cand = and i1 %%e_al, %%h_eq")
	g.indentLine(1, "br i1 %%cand, label %%cmp_k, label %%probe_adv")
	g.line("cmp_k:")
	eqRem := g.emitValueEqInline("%e_k_p", "%key", keyTy)
	g.indentLine(1, "br i1 %s, label %%do_remove, label %%probe_adv", eqRem)
	g.line("do_remove:")
	g.indentLine(1, "store i1 false, ptr %%e_al_p, align 1")
	if !keyTy.IsPlain() {
		g.emitValueDropInline("%e_k_p", keyTy)
	}
	g.indentLine(1, "%%v_mv = load %s, ptr %%e_v_p, align %d", valTyStr, valAlign)
	g.indentLine(1, "store %s zeroinitializer, ptr %%e_v_p, align %d", valTyStr, valAlign)
	g.indentLine(1, "store i64 -2, ptr %%bp, align 8")
	g.indentLine(1, "%%nlen = sub i64 %%len, 1")
	g.indentLine(1, "store i64 %%nlen, ptr %%p_len, align 8")
	g.emitStoreOptSome("%out", "%v_mv", false, valTy, optVal)
	g.indentLine(1, "ret void")
	g.line("probe_adv:")
	g.indentLine(1, "%%b_nxt_raw = add i64 %%b_cur, 1")
	g.indentLine(1, "%%b_nxt = and i64 %%b_nxt_raw, %%mask")
	g.indentLine(1, "store i64 %%b_nxt, ptr %%b_slot, align 8")
	g.indentLine(1, "br label %%probe_loop")
	g.line("ret_none:")
	if optVal.IsBoxed() {
		g.indentLine(1, "store ptr null, ptr %%out, align 8")
	} else {
		g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", optValTyStr)
	}
	g.indentLine(1, "ret void")
	g.line("}")

	g.line(`define internal %s @ll_map_remove__%s(ptr %%m, %s %%key) {
entry:
    %%pk = alloca %s, align %d
    %%out = alloca %s, align 8
    store %s %%key, ptr %%pk, align %d
    call void @ll_map_remove_p__%s(ptr %%out, ptr %%m, ptr %%pk)
    %%r = load %s, ptr %%out, align 8
    ret %s %%r
}`, optValTyStr, mangled, keyTyStr, keyTyStr, keyAlign, optValTyStr, keyTyStr, keyAlign, mangled, optValTyStr, optValTyStr)

	// 6. clear
	g.nextRegID = 0
	g.line("define internal void @ll_map_clear__%s(ptr %%m) {", mangled)
	g.line("entry:")
	g.indentLine(1, "%%p_b = getelementptr inbounds %s, ptr %%m, i32 0, i32 0", tyStr)
	g.indentLine(1, "%%p_nb = getelementptr inbounds %s, ptr %%m, i32 0, i32 1", tyStr)
	g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
	g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
	g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
	g.indentLine(1, "%%b = load ptr, ptr %%p_b, align 8")
	g.indentLine(1, "%%nb = load i64, ptr %%p_nb, align 8")
	g.indentLine(1, "%%e = load ptr, ptr %%p_e, align 8")
	g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
	if !keyTy.IsPlain() || !valTy.IsPlain() {
		g.indentLine(1, "%%has_e = icmp ne ptr %%e, null")
		g.indentLine(1, "br i1 %%has_e, label %%drop_loop, label %%reset_b")
		g.line("drop_loop:")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 %%el, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp sgt i64 %%i, 0")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%reset_b")
		g.line("loop_body:")
		g.indentLine(1, "%%ni = sub i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e, i64 %%ni", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%do_drop, label %%loop_cond")
		g.line("do_drop:")
		if !valTy.IsPlain() {
			g.indentLine(1, "%%p_v = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
			g.emitValueDropInline("%p_v", valTy)
		}
		if !keyTy.IsPlain() {
			g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
			g.emitValueDropInline("%p_k", keyTy)
		}
		g.indentLine(1, "store i1 false, ptr %%p_al, align 1")
		g.indentLine(1, "br label %%loop_cond")
		g.line("reset_b:")
	}
	g.indentLine(1, "%%has_b = icmp ne ptr %%b, null")
	g.indentLine(1, "%%pos_nb = icmp sgt i64 %%nb, 0")
	g.indentLine(1, "%%do_memset = and i1 %%has_b, %%pos_nb")
	g.indentLine(1, "br i1 %%do_memset, label %%memset_b, label %%finish")
	g.line("memset_b:")
	g.indentLine(1, "%%b_bytes = mul i64 %%nb, 8")
	g.indentLine(1, "call void @llvm.memset.p0.i64(ptr %%b, i8 -1, i64 %%b_bytes, i1 false)")
	g.indentLine(1, "br label %%finish")
	g.line("finish:")
	g.indentLine(1, "store i64 0, ptr %%p_el, align 8")
	g.indentLine(1, "store i64 0, ptr %%p_len, align 8")
	g.indentLine(1, "ret void")
	g.line("}")

	vecKey := &types.Vector{Elem: mp.Key}
	if g.hasVectorType(vecKey) && CanCopyType(g.prog, keyTy) {
		vecTyStr := g.llvmType(vecKey)
		vm := vecKey.Mangled()
		fnName := "ll_map_keys__" + mangled
		g.funcSet[fnName] = true
		g.nextRegID = 0
		g.line("define internal %s @%s(ptr %%m) {", vecTyStr, fnName)
		g.line("entry:")
		g.indentLine(1, "%%out = alloca %s, align 8", vecTyStr)
		g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%m_len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%has_items = icmp sgt i64 %%m_len, 0")
		g.indentLine(1, "br i1 %%has_items, label %%reserve, label %%check_loop")
		g.line("reserve:")
		g.indentLine(1, "call void @ll_vec_reserve__%s(ptr %%out, i64 %%m_len)", vm)
		g.indentLine(1, "br label %%check_loop")
		g.line("check_loop:")
		g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
		g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%finish")
		g.line("loop_body:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%i", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%push_item, label %%next_iter")
		g.line("push_item:")
		g.indentLine(1, "%%p_k = getelementptr inbounds %s, ptr %%ent, i32 0, i32 2", entTyStr)
		if keyTy.IsPlain() {
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%p_k)", vm)
		} else {
			g.indentLine(1, "%%copied_k = alloca %s, align %d", keyTyStr, keyAlign)
			g.emitValueCopyInline("%copied_k", "%p_k", keyTy)
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%copied_k)", vm)
		}
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("finish:")
		g.indentLine(1, "%%r = load %s, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "ret %s %%r", vecTyStr)
		g.line("}")
	}

	vecVal := &types.Vector{Elem: mp.Value}
	if g.hasVectorType(vecVal) && CanCopyType(g.prog, valTy) {
		vecTyStr := g.llvmType(vecVal)
		vm := vecVal.Mangled()
		fnName := "ll_map_values__" + mangled
		g.funcSet[fnName] = true
		g.nextRegID = 0
		g.line("define internal %s @%s(ptr %%m) {", vecTyStr, fnName)
		g.line("entry:")
		g.indentLine(1, "%%out = alloca %s, align 8", vecTyStr)
		g.indentLine(1, "store %s zeroinitializer, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "%%p_len = getelementptr inbounds %s, ptr %%m, i32 0, i32 5", tyStr)
		g.indentLine(1, "%%m_len = load i64, ptr %%p_len, align 8")
		g.indentLine(1, "%%has_items = icmp sgt i64 %%m_len, 0")
		g.indentLine(1, "br i1 %%has_items, label %%reserve, label %%check_loop")
		g.line("reserve:")
		g.indentLine(1, "call void @ll_vec_reserve__%s(ptr %%out, i64 %%m_len)", vm)
		g.indentLine(1, "br label %%check_loop")
		g.line("check_loop:")
		g.indentLine(1, "%%p_el = getelementptr inbounds %s, ptr %%m, i32 0, i32 3", tyStr)
		g.indentLine(1, "%%p_e = getelementptr inbounds %s, ptr %%m, i32 0, i32 2", tyStr)
		g.indentLine(1, "%%el = load i64, ptr %%p_el, align 8")
		g.indentLine(1, "%%e_arr = load ptr, ptr %%p_e, align 8")
		g.indentLine(1, "%%idx = alloca i64, align 8")
		g.indentLine(1, "store i64 0, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("loop_cond:")
		g.indentLine(1, "%%i = load i64, ptr %%idx, align 8")
		g.indentLine(1, "%%more = icmp slt i64 %%i, %%el")
		g.indentLine(1, "br i1 %%more, label %%loop_body, label %%finish")
		g.line("loop_body:")
		g.indentLine(1, "%%ent = getelementptr inbounds %s, ptr %%e_arr, i64 %%i", entTyStr)
		g.indentLine(1, "%%p_al = getelementptr inbounds %s, ptr %%ent, i32 0, i32 0", entTyStr)
		g.indentLine(1, "%%al = load i1, ptr %%p_al, align 1")
		g.indentLine(1, "br i1 %%al, label %%push_item, label %%next_iter")
		g.line("push_item:")
		g.indentLine(1, "%%p_v = getelementptr inbounds %s, ptr %%ent, i32 0, i32 3", entTyStr)
		if valTy.IsPlain() {
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%p_v)", vm)
		} else {
			g.indentLine(1, "%%copied_v = alloca %s, align %d", valTyStr, valAlign)
			g.emitValueCopyInline("%copied_v", "%p_v", valTy)
			g.indentLine(1, "call void @ll_vec_push_p__%s(ptr %%out, ptr %%copied_v)", vm)
		}
		g.indentLine(1, "br label %%next_iter")
		g.line("next_iter:")
		g.indentLine(1, "%%ni = add i64 %%i, 1")
		g.indentLine(1, "store i64 %%ni, ptr %%idx, align 8")
		g.indentLine(1, "br label %%loop_cond")
		g.line("finish:")
		g.indentLine(1, "%%r = load %s, ptr %%out, align 8", vecTyStr)
		g.indentLine(1, "ret %s %%r", vecTyStr)
		g.line("}")
	}
}

func (g *llvmEmitter) emitPointerHelpersFromDeclsLLVM() {
	for _, cd := range g.helperDecls {
		if g.funcSet[cd.fn] {
			continue
		}
		if strings.HasPrefix(cd.fn, "ll_drop__ptr_") || strings.HasPrefix(cd.fn, "ll_drop__ref_") {
			g.funcSet[cd.fn] = true
			g.line("define internal void @%s(ptr %%p) { ret void }", cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_copy_p__ptr_") || strings.HasPrefix(cd.fn, "ll_copy_p__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal void @%s(ptr %%dst, ptr %%src) {
entry:
    %%v = load ptr, ptr %%src, align 8
    store ptr %%v, ptr %%dst, align 8
    ret void
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_copy__ptr_") || strings.HasPrefix(cd.fn, "ll_copy__ref_") {
			g.funcSet[cd.fn] = true
			g.line("define internal ptr @%s(ptr %%v) { ret ptr %%v }", cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_eq_p__ptr_") || strings.HasPrefix(cd.fn, "ll_eq_p__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal i1 @%s(ptr %%a, ptr %%b) {
entry:
    %%va = load ptr, ptr %%a, align 8
    %%vb = load ptr, ptr %%b, align 8
    %%r = icmp eq ptr %%va, %%vb
    ret i1 %%r
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_eq__ptr_") || strings.HasPrefix(cd.fn, "ll_eq__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal i1 @%s(ptr %%a, ptr %%b) {
entry:
    %%r = icmp eq ptr %%a, %%b
    ret i1 %%r
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_hash_p__ptr_") || strings.HasPrefix(cd.fn, "ll_hash_p__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal i64 @%s(ptr %%p) {
entry:
    %%v = load ptr, ptr %%p, align 8
    %%i = ptrtoint ptr %%v to i64
    %%h = call i64 @ll_hash_mix64(i64 %%i)
    ret i64 %%h
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_hash__ptr_") || strings.HasPrefix(cd.fn, "ll_hash__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal i64 @%s(ptr %%v) {
entry:
    %%i = ptrtoint ptr %%v to i64
    %%h = call i64 @ll_hash_mix64(i64 %%i)
    ret i64 %%h
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_tostr_p__ptr_") || strings.HasPrefix(cd.fn, "ll_tostr_p__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal void @%s(ptr %%out, ptr %%p) {
entry:
    %%v = load ptr, ptr %%p, align 8
    call void @ll_tostr_ptr_p(ptr %%out, ptr %%v)
    ret void
}`, cd.fn)
		} else if strings.HasPrefix(cd.fn, "ll_tostr__ptr_") || strings.HasPrefix(cd.fn, "ll_tostr__ref_") {
			g.funcSet[cd.fn] = true
			g.line(`define internal %%struct.ll_string @%s(ptr %%v) {
entry:
    %%out = alloca %%struct.ll_string, align 8
    call void @ll_tostr_ptr_p(ptr %%out, ptr %%v)
    %%r = load %%struct.ll_string, ptr %%out, align 8
    ret %%struct.ll_string %%r
}`, cd.fn)
		}
	}
}
