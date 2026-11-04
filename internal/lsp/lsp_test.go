package lsp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lowlight/internal/lsp"
)

// rpcHarness drives an `lsp.Server` over real Content-Length framed pipes,
// matching how VS Code's Extension Host communicates over stdio.
type rpcHarness struct {
	t       *testing.T
	clientW io.WriteCloser
	clientR *bufio.Reader
	nextID  int
	diags   map[string][]lsp.Diagnostic
	doneCh  chan int
}

func newHarness(t *testing.T) *rpcHarness {
	t.Helper()
	srvR, cliW := io.Pipe()
	cliR, srvW := io.Pipe()

	srv := lsp.NewServer(srvR, srvW, nil)
	done := make(chan int, 1)
	go func() {
		code := srv.Serve()
		_ = srvW.Close()
		done <- code
	}()

	h := &rpcHarness{
		t:       t,
		clientW: cliW,
		clientR: bufio.NewReader(cliR),
		diags:   make(map[string][]lsp.Diagnostic),
		doneCh:  done,
	}
	t.Cleanup(func() {
		_ = cliW.Close()
	})
	return h
}

func (h *rpcHarness) sendRaw(payload any) {
	h.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		h.t.Fatalf("marshal error: %v", err)
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(b), string(b))
	if _, err := io.WriteString(h.clientW, frame); err != nil {
		h.t.Fatalf("write frame error: %v", err)
	}
}

func (h *rpcHarness) notify(method string, params any) {
	h.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (h *rpcHarness) request(method string, params any, out any) {
	h.t.Helper()
	h.nextID++
	reqID := h.nextID
	h.sendRaw(map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	})

	for {
		raw := h.readFrame()
		var env struct {
			ID     *int                `json:"id"`
			Method string              `json:"method"`
			Params json.RawMessage     `json:"params"`
			Result json.RawMessage     `json:"result"`
			Error  *lsp.ResponseError  `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			h.t.Fatalf("unmarshal frame %s: %v", string(raw), err)
		}
		if env.ID == nil && env.Method == "textDocument/publishDiagnostics" {
			var pd lsp.PublishDiagnosticsParams
			if err := json.Unmarshal(env.Params, &pd); err != nil {
				h.t.Fatalf("unmarshal publishDiagnostics: %v", err)
			}
			h.diags[pd.URI] = pd.Diagnostics
			continue
		}
		if env.ID != nil && *env.ID == reqID {
			if env.Error != nil {
				h.t.Fatalf("RPC %s returned error (%d): %s", method, env.Error.Code, env.Error.Message)
			}
			// Verify JSON-RPC 2.0 compliance: `"result"` key must always be present on non-error responses!
			if len(env.Result) == 0 {
				h.t.Fatalf("RPC %s missing mandatory JSON-RPC 2.0 `result` field in %s", method, string(raw))
			}
			if out != nil && string(env.Result) != "null" {
				if err := json.Unmarshal(env.Result, out); err != nil {
					h.t.Fatalf("unmarshal result of %s: %v (raw: %s)", method, err, string(env.Result))
				}
			}
			return
		}
	}
}

func (h *rpcHarness) waitDiagnostics(uri string) []lsp.Diagnostic {
	h.t.Helper()
	for {
		raw := h.readFrame()
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			h.t.Fatalf("unmarshal notification: %v", err)
		}
		if env.Method == "textDocument/publishDiagnostics" {
			var pd lsp.PublishDiagnosticsParams
			if err := json.Unmarshal(env.Params, &pd); err != nil {
				h.t.Fatalf("unmarshal publishDiagnostics: %v", err)
			}
			h.diags[pd.URI] = pd.Diagnostics
			if pd.URI == uri {
				return pd.Diagnostics
			}
		}
	}
}

func (h *rpcHarness) readFrame() []byte {
	h.t.Helper()
	cl := -1
	for {
		line, err := h.clientR.ReadString('\n')
		if err != nil {
			h.t.Fatalf("read header failed: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if cl >= 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err2 := strconv.Atoi(v)
			if err2 != nil {
				h.t.Fatalf("bad content-length %q: %v", v, err2)
			}
			cl = n
		}
	}
	buf := make([]byte, cl)
	if _, err := io.ReadFull(h.clientR, buf); err != nil {
		h.t.Fatalf("read payload failed: %v", err)
	}
	return buf
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func TestLSPRealtimeDiagnosticsAndLifecycle(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initRes)
	if !initRes.Capabilities.HoverProvider || !initRes.Capabilities.DefinitionProvider ||
		!initRes.Capabilities.CodeActionProvider || initRes.Capabilities.CompletionProvider == nil {
		t.Fatalf("missing expected server capabilities: %+v", initRes.Capabilities)
	}
	h.notify("initialized", map[string]any{})

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "live.ll")
	uri := lsp.PathToURI(docPath)

	// 1. Open valid Lowlight program -> 0 diagnostics
	validSrc := `bind main = () {
	let x = 42
	println(x)
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       validSrc,
		},
	})
	diags := h.waitDiagnostics(uri)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics on valid code, got %+v", diags)
	}

	// 2. Introduce ownership move error + missing `?` error in live buffer
	errSrc := `bind consume = (s: own string) {
	println(s)
}

bind main = () {
	let msg = "hello"
	consume(own msg)
	println(msg)
	let bad_opt: ?int = none
}
`
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: errSrc}},
	})
	diags = h.waitDiagnostics(uri)
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 diagnostics (use-after-move + missing '?'), got %+v", diags)
	}
	joined := ""
	for _, d := range diags {
		joined += d.Message + "\n"
	}
	if !strings.Contains(joined, "use of moved variable \"msg\"") {
		t.Errorf("missing use-after-move diagnostic in:\n%s", joined)
	}
	if !strings.Contains(joined, "must end with `?`") {
		t.Errorf("missing optional '?' identifier rule diagnostic in:\n%s", joined)
	}

	// 3. Fix errors via didChange -> 0 diagnostics
	fixedSrc := `bind consume = (s: own string) {
	println(s)
}

bind main = () {
	let msg = "hello"
	consume(msg.copy())
	println(msg)
	let good_opt?: ?int = none
	if (good_opt?) {
		println(good_opt)
	}
}
`
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 3},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: fixedSrc}},
	})
	diags = h.waitDiagnostics(uri)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics after fix, got %+v", diags)
	}

	// 4. Close document -> clears diagnostics; clean shutdown + exit returns 0
	h.notify("textDocument/didClose", lsp.DidCloseTextDocumentParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	})
	_ = h.waitDiagnostics(uri)

	h.request("shutdown", nil, nil)
	h.notify("exit", nil)
	if code := <-h.doneCh; code != 0 {
		t.Fatalf("expected clean exit code 0 after shutdown+exit, got %d", code)
	}
}

func TestLSPAutocompleteAllContexts(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(root)}, &initRes)
	h.notify("initialized", map[string]any{})

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "complete_me.ll")
	uri := lsp.PathToURI(docPath)

	openAndComplete := func(srcWithCursorMarker string) []lsp.CompletionItem {
		idx := strings.Index(srcWithCursorMarker, "/*CURSOR*/")
		if idx < 0 {
			t.Fatal("missing /*CURSOR*/ marker")
		}
		cleanSrc := srcWithCursorMarker[:idx] + srcWithCursorMarker[idx+len("/*CURSOR*/"):]
		pos := lsp.OffsetToLSP(cleanSrc, idx)
		h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: cleanSrc},
		})
		_ = h.waitDiagnostics(uri)

		var list lsp.CompletionList
		h.request("textDocument/completion", lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     pos,
		}, &list)
		return list.Items
	}

	hasLabel := func(items []lsp.CompletionItem, want string) *lsp.CompletionItem {
		for i := range items {
			if items[i].Label == want {
				return &items[i]
			}
		}
		return nil
	}

	// 1. Import path completion (`import "std/|"` -> `std/math.ll`, `std/fs.ll`, `std/pool.ll`)
	impItems := openAndComplete(`import "std//*CURSOR*/"
bind main = () {}
`)
	for _, want := range []string{"std/math.ll", "std/fs.ll", "std/pool.ll"} {
		if hasLabel(impItems, want) == nil {
			t.Errorf("import completion missing %q; got %+v", want, impItems)
		}
	}

	// 2. Struct dot-completion: fields + user methods + built-in `.copy()`
	structItems := openAndComplete(`define Point struct [
	x: float,
	y: float,
]

bind Point.translate = (&self, dx: float, dy: float) {
	self.x += dx
	self.y += dy
}

bind main = () {
	let pt = Point[x: 1.0, y: 2.0]
	pt./*CURSOR*/
}
`)
	for _, want := range []string{"x", "y", "translate", "copy"} {
		if hasLabel(structItems, want) == nil {
			t.Errorf("struct dot completion missing %q", want)
		}
	}

	// 3. Vector `[string]` built-in method completion (including string-specific `join`!)
	vecItems := openAndComplete(`bind main = () {
	let words = ["low", "light"]
	words./*CURSOR*/
}
`)
	for _, want := range []string{"push", "pop", "len", "get", "swap_remove", "join"} {
		if hasLabel(vecItems, want) == nil {
			t.Errorf("vector dot completion missing %q", want)
		}
	}

	// 4. Generic `Pool[int]` dot-completion on uninstantiated generic methods (`alloc`, `get`, `free`)!
	poolItems := openAndComplete(`import "std/pool.ll"

bind main = () {
	let p = Pool[int].new()
	p./*CURSOR*/
}
`)
	allocItem := hasLabel(poolItems, "alloc")
	if allocItem == nil {
		t.Fatalf("generic Pool[int] dot completion missing `alloc`; got %+v", poolItems)
	}
	if !strings.Contains(allocItem.Detail, "int") {
		t.Errorf("expected generic type substitution `int` in Pool[int].alloc detail, got %q", allocItem.Detail)
	}

	// 5. Optional chaining `user?.` -> exposes fields (`name`) with `?string` type and excludes `&self` mutators!
	optItems := openAndComplete(`define User struct [
	name: string,
]

bind User.rename = (&self, next: string) {
	self.name = next
}

bind User.greet = (self): string {
	"hi " + self.name
}

bind main = () {
	let user?: ?User = User[name: "Ada"]
	user?./*CURSOR*/
}
`)
	if nameItem := hasLabel(optItems, "name"); nameItem == nil {
		t.Errorf("optional chain user?. missing field `name`")
	} else if !strings.Contains(nameItem.Detail, "?string") {
		t.Errorf("expected optional-wrapped field type `?string` on `user?.name`, got %q", nameItem.Detail)
	}
	if hasLabel(optItems, "greet") == nil {
		t.Errorf("optional chain user?. missing read-only method `greet`")
	}
	if hasLabel(optItems, "rename") != nil {
		t.Errorf("optional chain user?. should NOT suggest mutating `&self` method `rename`")
	}

	// 6. Enum variant completion (`Color.|`), `Arena.|`, and module alias `math.|`
	enumItems := openAndComplete(`import "std/math.ll" as math
define Color enum [Red, Green, Blue]
bind main = () {
	let c = Color./*CURSOR*/
}
`)
	for _, want := range []string{"Red", "Green", "Blue"} {
		if hasLabel(enumItems, want) == nil {
			t.Errorf("enum completion missing %q", want)
		}
	}

	modItems := openAndComplete(`import "std/math.ll" as math
bind main = () {
	let v = math./*CURSOR*/
}
`)
	for _, want := range []string{"PI", "sin", "clamp_int", "lerp"} {
		if hasLabel(modItems, want) == nil {
			t.Errorf("module alias completion missing %q", want)
		}
	}

	// 7. Struct literal field completion (`Point[x: 1.0, |]` -> suggests `y`, omits already-used `x`)
	litItems := openAndComplete(`define Point struct [
	x: float,
	y: float,
]
bind main = () {
	let pt = Point[x: 1.0, /*CURSOR*/]
}
`)
	if hasLabel(litItems, "y") == nil {
		t.Errorf("struct literal completion missing remaining field `y`; got %+v", litItems)
	}
	if hasLabel(litItems, "x") != nil {
		t.Errorf("struct literal completion should omit already-initialized field `x`")
	}

	// 8. Narrowed optional scope inside `if (account?) { acc| }`
	narrowItems := openAndComplete(`bind main = () {
	let account?: ?string = "pro"
	if (account?) {
		println(acc/*CURSOR*/)
	}
}
`)
	accItem := hasLabel(narrowItems, "account")
	if accItem == nil {
		t.Fatalf("narrowed optional variable `account` missing inside `if (account?)` block")
	}
	if !strings.Contains(accItem.Detail, "narrowed") || !strings.Contains(accItem.Detail, "string") {
		t.Errorf("unexpected detail on narrowed variable `account`: %q", accItem.Detail)
	}
}

func TestLSPHoverDefinitionSignatureSymbolsAndQuickFixes(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(root)}, &initRes)
	h.notify("initialized", map[string]any{})

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "features.ll")
	uri := lsp.PathToURI(docPath)

	src := `import "std/math.ll" as math

define Counter struct [
	value: int,
]

bind Counter.add = (&self, delta: int, scale: int) {
	self.value += delta * scale
}

bind main = () {
	let c = Counter[value: 10]
	c.add(5, 2)
	let clamped = math.clamp_int(15, 0, 10)
	println(clamped)
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: src},
	})
	if diags := h.waitDiagnostics(uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	// 1. Hover on `clamped` variable
	clampedIdx := strings.Index(src, "println(clamped)") + len("println(")
	var hov lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, clampedIdx),
	}, &hov)
	if !strings.Contains(hov.Contents.Value, "let clamped: int") {
		t.Errorf("unexpected hover for `clamped`: %q", hov.Contents.Value)
	}

	// 2. Cross-file Go-to-Definition on `math.clamp_int` -> jumps into `std/math.ll`!
	clampCallIdx := strings.Index(src, "math.clamp_int") + len("math.cl")
	var defLoc lsp.Location
	h.request("textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, clampCallIdx),
	}, &defLoc)
	if !strings.HasSuffix(lsp.URIToPath(defLoc.URI), filepath.Join("std", "math.ll")) {
		t.Errorf("expected definition of `math.clamp` in std/math.ll, got URI %q", defLoc.URI)
	}

	// 3. Same-file Go-to-Definition on `c.add` -> jumps to `bind Counter.add` (line 6, 0-based)
	addCallIdx := strings.Index(src, "c.add") + 3
	var addLoc lsp.Location
	h.request("textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, addCallIdx),
	}, &addLoc)
	if addLoc.Range.Start.Line != 6 {
		t.Errorf("expected `c.add` definition at line 6, got %+v", addLoc.Range)
	}

	// 4. Signature Help on second argument of UFCS method `c.add(5, |2)` -> activeParameter == 1 (`scale: int`)!
	sigArg2Idx := strings.Index(src, "c.add(5, ") + len("c.add(5, ")
	var sigHelp lsp.SignatureHelp
	h.request("textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, sigArg2Idx),
	}, &sigHelp)
	if len(sigHelp.Signatures) == 0 || sigHelp.ActiveParameter != 1 {
		t.Fatalf("expected activeParameter == 1 on `c.add(5, |)`, got %+v", sigHelp)
	}
	if len(sigHelp.Signatures[0].Parameters) != 2 || !strings.Contains(sigHelp.Signatures[0].Parameters[1].Label, "scale: int") {
		t.Errorf("unexpected parameters in signatureHelp: %+v", sigHelp.Signatures[0].Parameters)
	}

	// 5. Document Symbols -> Counter (with child `value`), Counter.add, main
	var docSyms []lsp.DocumentSymbol
	h.request("textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}, &docSyms)
	if len(docSyms) != 3 {
		t.Fatalf("expected 3 top-level document symbols, got %d (%+v)", len(docSyms), docSyms)
	}
	if docSyms[0].Name != "Counter" || len(docSyms[0].Children) != 1 || docSyms[0].Children[0].Name != "value" {
		t.Errorf("unexpected struct symbol hierarchy: %+v", docSyms[0])
	}

	// 6. Quick-Fix Code Actions (missing `&`, missing `own`, `!` -> `not `)
	fixSrc := `bind bump = (n: &int) { n += 1 }
bind sink = (s: own string) { println(s) }
bind main = () {
	let x = 1
	bump(x)
	let name = "ll"
	sink(name)
	let b = !true
}
`
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: fixSrc}},
	})
	fixDiags := h.waitDiagnostics(uri)
	if len(fixDiags) < 3 {
		t.Fatalf("expected >= 3 diagnostics in fixSrc, got %+v", fixDiags)
	}

	var actions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 0},
			End:   lsp.Position{Line: 10, Character: 0},
		},
		Context: lsp.CodeActionContext{Diagnostics: fixDiags},
	}, &actions)

	var titles []string
	for _, a := range actions {
		titles = append(titles, a.Title)
	}
	joinedTitles := strings.Join(titles, " | ")
	for _, expectedSub := range []string{"Prefix argument with '&'", "Prefix argument with 'own '", "Append '.copy()'", "Replace '!' with 'not '"} {
		if !strings.Contains(joinedTitles, expectedSub) {
			t.Errorf("expected quick-fix %q in [%s]", expectedSub, joinedTitles)
		}
	}
}

func TestSubprocessCLIStdioRoundtrip(t *testing.T) {
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "lowlight")
	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/lowlight")
	buildCmd.Dir = root
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}

	var stdinBuf bytes.Buffer
	writeRPC := func(msg any) {
		b, _ := json.Marshal(msg)
		fmt.Fprintf(&stdinBuf, "Content-Length: %d\r\n\r\n%s", len(b), string(b))
	}
	writeRPC(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"rootUri": lsp.PathToURI(root)}})
	writeRPC(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}})
	writeRPC(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "shutdown"})
	writeRPC(map[string]any{"jsonrpc": "2.0", "method": "exit"})

	cmd := exec.Command(bin, "lsp", "--stdio")
	cmd.Stdin = &stdinBuf
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`lowlight lsp --stdio` exited with error: %v\noutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), `"name":"lowlight-lsp"`) {
		t.Fatalf("expected initialize response from `lowlight lsp --stdio`, got:\n%s", string(out))
	}
}

func TestLSPDuplicateDeclarationDiagnostics(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(root)}, &initRes)
	h.notify("initialized", map[string]any{})

	docPath := filepath.Join(t.TempDir(), "dup_test.ll")
	uri := lsp.PathToURI(docPath)

	dupSrc := `import "std/test.ll"

@test("pass")
bind t = () assert_eq(1, 1)

@test("fail")
bind t = () assert_eq(1, 2)
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       dupSrc,
		},
	})

	diags := h.waitDiagnostics(uri)
	if len(diags) == 0 {
		t.Fatalf("expected duplicate declaration diagnostic in LSP, got 0")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `duplicate declaration of "t"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic containing `duplicate declaration of \"t\"`, got %+v", diags)
	}

	// Fix test name via didChange -> 0 diagnostics
	fixedSrc := `import "std/test.ll"

@test("pass")
bind test_pass = () assert_eq(1, 1)

@test("fail")
bind test_fail = () assert_eq(1, 2)
`
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: fixedSrc}},
	})

	diags = h.waitDiagnostics(uri)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics after fixing duplicate declaration, got %+v", diags)
	}
}

func TestLSPDuplicateLocalDeclarationDiagnostics(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(root)}, &initRes)
	h.notify("initialized", map[string]any{})

	docPath := filepath.Join(t.TempDir(), "dup_local_test.ll")
	uri := lsp.PathToURI(docPath)

	dupSrc := `bind main = () {
	bind a = 1
	bind a = 2

	let b = 1
	let b = 2
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       dupSrc,
		},
	})

	diags := h.waitDiagnostics(uri)
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 duplicate declaration diagnostics in LSP, got %d: %+v", len(diags), diags)
	}
	joined := ""
	for _, d := range diags {
		joined += d.Message + "\n"
	}
	if !strings.Contains(joined, `duplicate declaration of "a"`) {
		t.Errorf("missing duplicate declaration of \"a\" in:\n%s", joined)
	}
	if !strings.Contains(joined, `duplicate declaration of "b"`) {
		t.Errorf("missing duplicate declaration of \"b\" in:\n%s", joined)
	}

	// Fix via didChange
	fixedSrc := `bind main = () {
	bind a1 = 1
	bind a2 = 2

	let b1 = 1
	let b2 = 2
}
`
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: fixedSrc}},
	})

	diags = h.waitDiagnostics(uri)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics after fixing duplicate local declarations, got %+v", diags)
	}
}
