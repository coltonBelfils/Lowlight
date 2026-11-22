package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func writeTestFile(t *testing.T, dir, relPath, content string) string {
	t.Helper()
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test file %s: %v", relPath, err)
	}
	return absPath
}

func runFileStrict(t *testing.T, name, entryPath string, release bool) string {
	t.Helper()
	root := projectRoot(t)

	var stdoutLLVM, stderrLLVM bytes.Buffer
	rcLLVM, errLLVM := driver.Run(driver.Options{
		EntryPath:   entryPath,
		ProjectRoot: root,
		Backend:     driver.BackendLLVM,
		Release:     release,
		ExtraCFlags: strictCFlags,
		Env: []string{
			"LOWLIGHT_CHECK_LEAKS=1",
			"ASAN_OPTIONS=detect_leaks=0",
		},
	}, nil, &stdoutLLVM, &stderrLLVM)
	if errLLVM != nil || rcLLVM != 0 {
		t.Fatalf("[%s LLVM] compilation/execution error: %v, code=%d\nstderr:\n%s", name, errLLVM, rcLLVM, stderrLLVM.String())
	}

	var stdoutC11, stderrC11 bytes.Buffer
	rcC11, errC11 := driver.Run(driver.Options{
		EntryPath:   entryPath,
		ProjectRoot: root,
		Backend:     driver.BackendC11,
		Release:     release,
		ExtraCFlags: strictCFlags,
		Env: []string{
			"LOWLIGHT_CHECK_LEAKS=1",
			"ASAN_OPTIONS=detect_leaks=0",
		},
	}, nil, &stdoutC11, &stderrC11)
	if errC11 != nil || rcC11 != 0 {
		t.Fatalf("[%s C11] compilation/execution error: %v, code=%d\nstderr:\n%s", name, errC11, rcC11, stderrC11.String())
	}

	if stdoutLLVM.String() != stdoutC11.String() {
		t.Fatalf("[%s] backend parity mismatch between LLVM and C11:\nLLVM: %q\nC11: %q", name, stdoutLLVM.String(), stdoutC11.String())
	}
	return stdoutLLVM.String()
}

// 1. Struct Field Encapsulation: Unexported fields cannot be accessed outside their defining package.
func TestEncapsulationFieldAccess(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	bankSrc := `
define BankAccount struct [
    owner: string,
    _balance: int,
]

bind BankAccount.new = fn(owner: string, initial: int) BankAccount: {
    BankAccount[owner: owner.copy(), _balance: initial]
}

bind BankAccount.balance = fn(self) int: {
    self._balance
}
`
	writeTestFile(t, tmp, "pkg_bank/bank.ll", bankSrc)

	// Direct read of _balance from an outside package must fail
	mainReadSrc := `
import "./pkg_bank" as bank

bind main = fn(): {
    let acc = bank.BankAccount.new("Alice", 100)
    let b = acc._balance
    println(b)
}
`
	entryRead := writeTestFile(t, tmp, "main_read.ll", mainReadSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryRead,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error accessing unexported field _balance, but check succeeded")
	}
	if !strings.Contains(err.Error(), `cannot access unexported field "_balance"`) {
		t.Fatalf("expected error mentioning unexported field, got: %v", err)
	}

	// Direct mutation of _balance from an outside package must fail
	mainWriteSrc := `
import "./pkg_bank" as bank

bind main = fn(): {
    let acc = bank.BankAccount.new("Alice", 100)
    acc._balance = 500
}
`
	entryWrite := writeTestFile(t, tmp, "main_write.ll", mainWriteSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryWrite,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error mutating unexported field _balance, but check succeeded")
	}
	if !strings.Contains(err.Error(), `cannot access unexported field "_balance"`) {
		t.Fatalf("expected error mentioning unexported field, got: %v", err)
	}
}

// 2. Struct Invariant Protection:
// If a struct contains any unexported field without a default value (and not ?T),
// it cannot be constructed via struct literal outside its package.
func TestEncapsulationStructInvariant(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	bankSrc := `
define BankAccount struct [
    owner: string,
    _balance: int,
]
`
	writeTestFile(t, tmp, "pkg_bank/bank.ll", bankSrc)

	// Attempting to name _balance in struct literal from outside package must fail
	mainExplicitFieldSrc := `
import "./pkg_bank" as bank

bind main = fn(): {
    let acc = bank.BankAccount[owner: "Alice", _balance: 1000]
    println(acc.owner)
}
`
	entryExplicit := writeTestFile(t, tmp, "main_explicit.ll", mainExplicitFieldSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryExplicit,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error initializing unexported field, but check succeeded")
	}
	if !strings.Contains(err.Error(), `cannot initialize unexported field "_balance"`) {
		t.Fatalf("expected error mentioning cannot initialize unexported field, got: %v", err)
	}

	// Attempting to omit _balance when it has no default value must fail with invariant error
	mainOmitFieldSrc := `
import "./pkg_bank" as bank

bind main = fn(): {
    let acc = bank.BankAccount[owner: "Alice"]
    println(acc.owner)
}
`
	entryOmit := writeTestFile(t, tmp, "main_omit.ll", mainOmitFieldSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryOmit,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected invariant construction error, but check succeeded")
	}
	if !strings.Contains(err.Error(), "cannot construct struct BankAccount outside its package") ||
		!strings.Contains(err.Error(), `unexported field "_balance" has no default value`) {
		t.Fatalf("expected invariant protection error mentioning constructor function, got: %v", err)
	}
}

// 3. Structs with Defaults or Optionals on Unexported Fields:
// When all unexported fields have default values or are optional (?T), outside packages CAN construct literals.
func TestEncapsulationStructDefaultsAllowed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	configSrc := `
define ServiceConfig struct [
    host: string,
    port: int,
    _id: int = 42,
    _tag?: ?string,
]

bind ServiceConfig.get_id = fn(self) int: {
    self._id
}
`
	writeTestFile(t, tmp, "pkg_config/config.ll", configSrc)

	mainSrc := `
import "./pkg_config" as cfg

bind main = fn(): {
    let c = cfg.ServiceConfig[host: "localhost", port: 8080]
    assert(c.host == "localhost")
    assert(c.port == 8080)
    assert(c.get_id() == 42)
}
`
	entryMain := writeTestFile(t, tmp, "main.ll", mainSrc)

	// Verify semantic check passes
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryMain,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error for struct with defaulted unexported fields: %v", err)
	}

	// Verify full execution across LLVM and C11 backends
	runFileStrict(t, "encap_defaults", entryMain, false)
}

// 4. Method Encapsulation: Unexported methods (_method) cannot be called outside their package.
func TestEncapsulationMethodAccess(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	counterSrc := `
define Counter struct [
    value: int = 0,
]

bind Counter.increment = fn(&self): {
    self._bump()
}

bind Counter._bump = fn(&self): {
    self.value = self.value + 1
}
`
	writeTestFile(t, tmp, "pkg_counter/counter.ll", counterSrc)

	mainSrc := `
import "./pkg_counter" as cnt

bind main = fn(): {
    let c = cnt.Counter[value: 10]
    c._bump()
}
`
	entryMain := writeTestFile(t, tmp, "main.ll", mainSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryMain,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error calling unexported method _bump, but check succeeded")
	}
	if !strings.Contains(err.Error(), `cannot call unexported method "_bump" on type Counter outside its package`) {
		t.Fatalf("expected error mentioning unexported method, got: %v", err)
	}
}

// 5. Top-Level Symbols Encapsulation: Unexported functions and types are not accessible across packages.
func TestEncapsulationTopLevelSymbols(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	libSrc := `
define _SecretData struct [
    secret: int,
]

define PublicData struct [
    info: string,
]

bind _secret_fn = fn() int: {
    999
}

bind public_fn = fn() int: {
    _secret_fn()
}
`
	writeTestFile(t, tmp, "pkg_internal/lib.ll", libSrc)

	// Calling unexported function via module alias must fail
	mainCallFnSrc := `
import "./pkg_internal" as lib

bind main = fn(): {
    let x = lib._secret_fn()
    println(x)
}
`
	entryCallFn := writeTestFile(t, tmp, "main_call.ll", mainCallFnSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryCallFn,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error accessing unexported top-level function _secret_fn, but check succeeded")
	}
	if !strings.Contains(err.Error(), `cannot access unexported symbol "_secret_fn"`) {
		t.Fatalf("expected error mentioning unexported symbol, got: %v", err)
	}

	// Accessing unexported type via qualified package import must fail
	mainPrivateTypeSrc := `
import "./pkg_internal" as lib

bind main = fn(): {
    let s: lib._SecretData = lib._SecretData[secret: 1]
    println(s.secret)
}
`
	entryPrivateType := writeTestFile(t, tmp, "main_open.ll", mainPrivateTypeSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryPrivateType,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error resolving unexported type _SecretData from outside package, but check succeeded")
	}
}

// 6. Same-Directory Package Sharing:
// Multiple files residing in the same directory belong to the same package and can freely access
// unexported fields, methods, functions, and types.
func TestEncapsulationSameDirectoryPackageSharing(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	stateSrc := `
define ServiceState struct [
    _secret_key: int,
    active: bool,
]

bind _internal_helper = fn(k: int) int: {
    k * 2
}
`
	writeTestFile(t, tmp, "pkg_shared/state.ll", stateSrc)

	methodsSrc := `
bind ServiceState.new = fn(key: int) ServiceState: {
    // Both files are in pkg_shared, so initializing _secret_key is valid!
    ServiceState[
        _secret_key: _internal_helper(key),
        active: true,
    ]
}

bind ServiceState.key = fn(self) int: {
    // Both files in pkg_shared, so reading _secret_key is valid!
    self._secret_key
}
`
	writeTestFile(t, tmp, "pkg_shared/methods.ll", methodsSrc)

	mainSrc := `
import "./pkg_shared" as svc

bind main = fn(): {
    let s = svc.ServiceState.new(21)
    assert(s.active == true)
    assert(s.key() == 42)
}
`
	entryMain := writeTestFile(t, tmp, "main.ll", mainSrc)

	// Verify semantic check passes
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryMain,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error for same-directory package sharing: %v", err)
	}

	// Verify end-to-end execution across LLVM and C11 backends
	runFileStrict(t, "encap_shared", entryMain, false)

	// Verify that outside file CANNOT access the unexported function from pkg_shared
	badMainSrc := `
import "./pkg_shared" as svc

bind main = fn(): {
    let x = svc._internal_helper(10)
}
`
	badEntry := writeTestFile(t, tmp, "bad_main.ll", badMainSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   badEntry,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error accessing unexported function across package boundaries, but got none")
	}
}

// 7. Sealed Interfaces:
// If an interface contains one or more unexported methods (_method), only types defined in the
// same package as the interface can implement it. Outside types cannot satisfy the interface.
func TestEncapsulationSealedInterface(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	sealedSrc := `
define InternalNode interface [
    id: fn(self) int,
    _token: fn(self) int,
]

define ValidNode struct [
    my_id: int,
]

bind ValidNode.id = fn(self) int: {
    self.my_id
}

bind ValidNode._token = fn(self) int: {
    self.my_id * 10
}

bind process_node = fn(node: InternalNode) int: {
    node.id()
}
`
	writeTestFile(t, tmp, "pkg_sealed/sealed.ll", sealedSrc)

	// Type defined in outside package attempting to implement sealed InternalNode
	mainInvalidSrc := `
import "./pkg_sealed" as sealed

define OutsideNode struct [
    my_id: int,
]

bind OutsideNode.id = fn(self) int: {
    self.my_id
}

bind OutsideNode._token = fn(self) int: {
    123
}

bind main = fn(): {
    let out = OutsideNode[my_id: 5]
    // OutsideNode cannot satisfy sealed InternalNode!
    let res = sealed.process_node(out)
    println(res)
}
`
	entryInvalid := writeTestFile(t, tmp, "main_invalid.ll", mainInvalidSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryInvalid,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected failure implementing sealed interface from outside package, but check succeeded")
	}

	// Type defined inside package can implement sealed InternalNode and run
	mainValidSrc := `
import "./pkg_sealed" as sealed

bind main = fn(): {
    let valid = sealed.ValidNode[my_id: 7]
    let res = sealed.process_node(valid)
    assert(res == 7)
}
`
	entryValid := writeTestFile(t, tmp, "main_valid.ll", mainValidSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryValid,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error for valid inside-package implementation of sealed interface: %v", err)
	}

	runFileStrict(t, "sealed_e2e", entryValid, false)
}

// 8. End-to-End Object Encapsulation Runtime:
// Verifies full execution of a complete encapsulated module with constructor, getters,
// mutating methods, and invariants over both backends.
func TestEncapsulationEndToEndExecution(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	walletSrc := `
define Wallet struct [
    owner: string,
    _coins: int,
    _history_count: int = 0,
]

bind Wallet.new = fn(owner: string, initial_coins: int) Wallet: {
    Wallet[
        owner: owner.copy(),
        _coins: initial_coins,
        _history_count: 0,
    ]
}

bind Wallet.coins = fn(self) int: {
    self._coins
}

bind Wallet.transactions = fn(self) int: {
    self._history_count
}

bind Wallet.credit = fn(&self, amount: int): {
    if (amount > 0): {
        self._coins = self._coins + amount
        self._record_tx()
    }
}

bind Wallet.debit = fn(&self, amount: int) bool: {
    if (amount > 0 and self._coins >= amount): {
        self._coins = self._coins - amount
        self._record_tx()
        true
    } else: {
        false
    }
}

bind Wallet._record_tx = fn(&self): {
    self._history_count = self._history_count + 1
}
`
	writeTestFile(t, tmp, "pkg_wallet/wallet.ll", walletSrc)

	mainSrc := `
import "./pkg_wallet" as w

bind main = fn(): {
    let wallet = w.Wallet.new("Alice", 100)
    assert(wallet.owner == "Alice")
    assert(wallet.coins() == 100)
    assert(wallet.transactions() == 0)

    wallet.credit(50)
    assert(wallet.coins() == 150)
    assert(wallet.transactions() == 1)

    let ok = wallet.debit(30)
    assert(ok == true)
    assert(wallet.coins() == 120)
    assert(wallet.transactions() == 2)

    let failed = wallet.debit(1000)
    assert(failed == false)
    assert(wallet.coins() == 120)
    assert(wallet.transactions() == 2)
}
`
	entryMain := writeTestFile(t, tmp, "main.ll", mainSrc)

	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryMain,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error in encapsulation e2e test: %v", err)
	}

	runFileStrict(t, "wallet_e2e", entryMain, false)
}

// 9. Enum Variant Encapsulation:
// Unexported enum variants (_Variant) cannot be constructed, accessed, or matched outside their defining package.
// Outside match expressions on enums with unexported variants require a `default:` catch-all arm.
func TestEncapsulationEnumVariants(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	tokenSrc := `
define Token enum [
    Ident: string,
    Number: int,
    _Internal: int,
    _Eof,
]

bind Token.make_eof = fn() Token: {
    Token._Eof
}

bind Token.describe = fn(self) string: {
    match (self):
        when Token.Ident(s): "ident:" + s
        when Token.Number(n): "num"
        when Token._Internal(code): "internal"
        default: "eof"
}
`
	writeTestFile(t, tmp, "pkg_tok/token.ll", tokenSrc)

	// Valid outside usage with catch-all else:
	mainSrc := `
import "./pkg_tok" as tok

bind main = fn(): {
    let t = tok.Token.Ident("foo")
    let kind = match (t):
        when tok.Token.Ident(s): "got_ident"
        when tok.Token.Number(n): "got_num"
        default: "other"
    assert(kind == "got_ident")

    let eof = tok.Token.make_eof()
    assert(eof.describe() == "eof")
}
`
	entryMain := writeTestFile(t, tmp, "main.ll", mainSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryMain,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error for enum variant usage: %v", err)
	}

	runFileStrict(t, "encap_enum_ok", entryMain, false)

	// Verify outside cannot match unexported variant
	badMatchSrc := `
import "./pkg_tok" as tok

bind test_bad = fn(t: tok.Token) int: {
    match (t):
        when tok.Token._Internal(c): 0
        default: 1
}
`
	badMatch := writeTestFile(t, tmp, "bad_match.ll", badMatchSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   badMatch,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error matching unexported enum variant, got none")
	}

	// Verify outside non-exhaustive match without else is rejected
	badNoElseSrc := `
import "./pkg_tok" as tok

bind test_bad = fn(t: tok.Token) int: {
    match (t):
        when tok.Token.Ident(s): 0
        when tok.Token.Number(n): 1
}
`
	badNoElse := writeTestFile(t, tmp, "bad_no_else.ll", badNoElseSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   badNoElse,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error for non-exhaustive match missing unexported variants without else, got none")
	}
}

// 10. Standard Core Package Encapsulation: Unexported symbols in std/core/ are protected from outside access.
func TestEncapsulationCorePackageProtection(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := projectRoot(t)

	// Valid usage of std/core exported types and functions
	validSrc := `
bind main = fn(): {
    let r! = Result[int].value(42)
    assert(r!.is_ok())
    assert(r!.unwrap() == 42)
}
`
	entryValid := writeTestFile(t, tmp, "valid_core.ll", validSrc)
	_, _, err := driver.Check(driver.Options{
		EntryPath:   entryValid,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected check error for valid core usage: %v", err)
	}
	runFileStrict(t, "encap_core_valid", entryValid, false)

	// Direct call to unexported _hash_mix_u64 must fail
	badCallSrc := `
bind main = fn(): {
    let h = _hash_mix_u64(u64(123))
    println(h)
}
`
	entryBadCall := writeTestFile(t, tmp, "bad_core_call.ll", badCallSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryBadCall,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error calling unexported _hash_mix_u64 from outside std/core, but check succeeded")
	}

	// Direct access to unexported ErrorFrame._file field must fail
	badFieldSrc := `
bind main = fn(): {
    let f = ErrorFrame.new("a.ll", 1, 1, none)
    println(f._file)
}
`
	entryBadField := writeTestFile(t, tmp, "bad_core_field.ll", badFieldSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryBadField,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error accessing unexported field _file from outside std/core, but check succeeded")
	}

	// Direct access to unexported Error._message field must fail
	badErrFieldSrc := `
bind main = fn(): {
    let e = Error.new("boom")
    println(e._message)
}
`
	entryBadErrField := writeTestFile(t, tmp, "bad_error_field.ll", badErrFieldSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryBadErrField,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error accessing unexported field _message from outside std/core, but check succeeded")
	}

	// Direct struct literal construction of Error without unexported fields must fail
	badErrLitSrc := `
bind main = fn(): {
    let e = Error[]
    println(e.message())
}
`
	entryBadErrLit := writeTestFile(t, tmp, "bad_error_lit.ll", badErrLitSrc)
	_, _, err = driver.Check(driver.Options{
		EntryPath:   entryBadErrLit,
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error constructing Error struct literal from outside std/core, but check succeeded")
	}
}
