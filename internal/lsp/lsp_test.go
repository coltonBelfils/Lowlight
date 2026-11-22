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
		if env.Method == "window/showMessage" || env.Method == "window/showDocument" {
			continue
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
	validSrc := `bind main = fn(): {
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
	errSrc := `bind consume = fn(s: own string): {
	println(s)
}

bind main = fn(): {
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
	fixedSrc := `bind consume = fn(s: own string): {
	println(s)
}

bind main = fn(): {
	let msg = "hello"
	consume(msg.copy())
	println(msg)
	let good_opt?: ?int = none
	if (good_opt?): {
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

	// 1. Import path completion (`import "std/|"` -> `std/math`, `std/fs`, `std/pool`)
	impItems := openAndComplete(`import "std//*CURSOR*/"
bind main = fn(): {}
`)
	for _, want := range []string{"std/math", "std/fs", "std/pool"} {
		if hasLabel(impItems, want) == nil {
			t.Errorf("import completion missing %q; got %+v", want, impItems)
		}
	}

	// 2. Struct dot-completion: fields + user methods + built-in `.copy()`
	structItems := openAndComplete(`define Point struct [
	x: float,
	y: float,
]

bind Point.translate = fn(&self, dx: float, dy: float): {
	self.x += dx
	self.y += dy
}

bind main = fn(): {
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
	vecItems := openAndComplete(`bind main = fn(): {
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
	poolItems := openAndComplete(`import "std/pool"

bind main = fn(): {
	let p = pool.Pool[int].new()
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

bind User.rename = fn(&self, next: string): {
	self.name = next
}

bind User.greet = fn(self) string: {
	"hi " + self.name
}

bind main = fn(): {
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
	enumItems := openAndComplete(`import "std/math"
define Color enum [Red, Green, Blue]
bind main = fn(): {
	let c = Color./*CURSOR*/
}
`)
	for _, want := range []string{"Red", "Green", "Blue"} {
		if hasLabel(enumItems, want) == nil {
			t.Errorf("enum completion missing %q", want)
		}
	}

	modItems := openAndComplete(`import "std/math"
bind main = fn(): {
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
bind main = fn(): {
	let pt = Point[x: 1.0, /*CURSOR*/]
}
`)
	if hasLabel(litItems, "y") == nil {
		t.Errorf("struct literal completion missing remaining field `y`; got %+v", litItems)
	}
	if hasLabel(litItems, "x") != nil {
		t.Errorf("struct literal completion should omit already-initialized field `x`")
	}

	// 8. Narrowed optional scope inside `if (account?): { acc| }`
	narrowItems := openAndComplete(`bind main = fn(): {
	let account?: ?string = "pro"
	if (account?): {
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

	src := `import "std/math"

define Counter struct [
	value: int,
]

bind Counter.add = fn(&self, delta: int, scale: int): {
	self.value += delta * scale
}

bind main = fn(): {
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
	if !strings.HasSuffix(lsp.URIToPath(defLoc.URI), filepath.Join("std", "math", "math.ll")) {
		t.Errorf("expected definition of `math.clamp_int` in std/math/math.ll, got URI %q", defLoc.URI)
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
	fixSrc := `bind bump = fn(n: &int): { n += 1 }
bind sink = fn(s: own string): { println(s) }
bind main = fn(): {
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

	dupSrc := `import "std/test"

@test("pass")
bind t = fn(): test.assert_eq(1, 1)
@test("fail")
bind t = fn(): test.assert_eq(1, 2)`
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
	fixedSrc := `import "std/test"

@test("pass")
bind test_pass = fn(): test.assert_eq(1, 1)
@test("fail")
bind test_fail = fn(): test.assert_eq(1, 2)`
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

	dupSrc := `bind main = fn(): {
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
	fixedSrc := `bind main = fn(): {
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

func TestLSPAdvancedFeatures(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initRes)
	if !initRes.Capabilities.DocumentFormattingProvider || !initRes.Capabilities.InlayHintProvider {
		t.Fatalf("missing new capabilities: %+v", initRes.Capabilities)
	}
	h.notify("initialized", map[string]any{})

	jsonSrc := `define JsonValue enum [
	Null,
	Bool: bool,
	Number: float,
	Str: string,
	Arr: [JsonValue],
	Obj: [
		keys: [string],
		values: [JsonValue],
	],
]

bind check = fn(val: JsonValue): {
	match (val):
}

bind test_calls = fn(): {
	let n = JsonValue.Number(3.14)
}
`
	tmpDir := t.TempDir()
	uri := lsp.PathToURI(filepath.Join(tmpDir, "json.ll"))
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       jsonSrc,
		},
	})

	diags := h.waitDiagnostics(uri)
	// There should be a non-exhaustive match diagnostic on `match (val)`
	var nonExhaustiveDiag *lsp.Diagnostic
	for i := range diags {
		if strings.Contains(diags[i].Message, "non-exhaustive `match` on enum JsonValue") {
			nonExhaustiveDiag = &diags[i]
			break
		}
	}
	if nonExhaustiveDiag == nil {
		t.Fatalf("expected non-exhaustive match diagnostic, got: %+v", diags)
	}

	// 1. Test Code Action: "Generate missing match arms"
	var actions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        nonExhaustiveDiag.Range,
		Context:      lsp.CodeActionContext{Diagnostics: []lsp.Diagnostic{*nonExhaustiveDiag}},
	}, &actions)

	foundArmGen := false
	for _, act := range actions {
		if strings.Contains(act.Title, "Generate missing match arms") {
			foundArmGen = true
			if act.Edit == nil || len(act.Edit.Changes) == 0 {
				t.Fatalf("expected workspace edit in match arms action: %+v", act)
			}
			edits := act.Edit.Changes[uri]
			if len(edits) == 0 || !strings.Contains(edits[0].NewText, "when Null:") || strings.Contains(edits[0].NewText, "JsonValue.Null") {
				t.Errorf("unexpected edit text: %+v", edits)
			}
		}
	}
	if !foundArmGen {
		t.Fatalf("missing 'Generate missing match arms' quickfix in %+v", actions)
	}

	// 2. Test Signature Help for Enum Variant Constructor `JsonValue.Number(`
	sigPos := strings.Index(jsonSrc, "JsonValue.Number(") + len("JsonValue.Number(")
	var sigHelp lsp.SignatureHelp
	h.request("textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(jsonSrc, sigPos),
	}, &sigHelp)

	if len(sigHelp.Signatures) == 0 {
		t.Fatalf("expected signature help for JsonValue.Number, got none")
	}
	if !strings.Contains(sigHelp.Signatures[0].Label, "JsonValue.Number") {
		t.Errorf("unexpected signature label: %s", sigHelp.Signatures[0].Label)
	}

	// 3. Test Inlay Hints
	var hints []lsp.InlayHint
	h.request("textDocument/inlayHint", lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 0},
			End:   lsp.Position{Line: 25, Character: 0},
		},
	}, &hints)

	foundTypeHint := false
	for _, h := range hints {
		if h.Kind == lsp.InlayHintType && strings.Contains(h.Label, "JsonValue") {
			foundTypeHint = true
		}
	}
	if !foundTypeHint {
		t.Errorf("expected type hint for `let n = JsonValue.Number(...)`, got %+v", hints)
	}

	// 4. Test Document Formatting
	messyURI := lsp.PathToURI(filepath.Join(tmpDir, "messy.ll"))
	messySrc := "bind main = fn(): {\nlet x = 1\nlet y = 2\n}\n"
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        messyURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       messySrc,
		},
	})
	_ = h.waitDiagnostics(messyURI)

	var edits []lsp.TextEdit
	h.request("textDocument/formatting", lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: messyURI},
		Options: lsp.FormattingOptions{
			TabSize:      4,
			InsertSpaces: true,
		},
	}, &edits)
	if len(edits) == 0 {
		t.Fatalf("expected formatting edits, got none")
	}
	if !strings.Contains(edits[0].NewText, "    let x = 1") {
		t.Errorf("expected 4-space indentation in formatted text, got: %q", edits[0].NewText)
	}

	// 4b. Test Match When Indentation and Nested Match Formatting
	matchURI := lsp.PathToURI(filepath.Join(tmpDir, "match_format.ll"))
	matchSrc := "bind foo = fn(): {\nmatch (self):\nwhen 1: match (other):\nwhen 1: true\ndefault: false\ndefault: false\n}\n"
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        matchURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       matchSrc,
		},
	})
	_ = h.waitDiagnostics(matchURI)
	var matchEdits []lsp.TextEdit
	h.request("textDocument/formatting", lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: matchURI},
		Options: lsp.FormattingOptions{
			TabSize:      4,
			InsertSpaces: true,
		},
	}, &matchEdits)
	if len(matchEdits) == 0 {
		t.Fatalf("expected match formatting edits, got none")
	}
	formattedMatch := matchEdits[0].NewText
	expectedLines := []string{
		"bind foo = fn():",
		"    match(self):",
		"        when 1:",
		"            match(other):",
		"                when 1: true",
		"                default: false",
		"        default: false",
	}
	expectedMatch := strings.Join(expectedLines, "\n") + "\n"
	if formattedMatch != expectedMatch {
		t.Errorf("unexpected match formatting:\nGOT:\n%s\nWANT:\n%s", formattedMatch, expectedMatch)
	}

	// 4c. Test Nested Closers Indentation (]) and })
	nestedURI := lsp.PathToURI(filepath.Join(tmpDir, "nested_closers.ll"))
	nestedSrc := `bind main = fn(): {
let json_value = Json.Object(#[
"abc": Json.Number(Number.Int(123)),
"def": Json.Object(#[
"abc": Json.Array([
Json.Number(Number.Int(1)),
Json.Bool(true),
]),
]),
])
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        nestedURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       nestedSrc,
		},
	})
	_ = h.waitDiagnostics(nestedURI)
	var nestedEdits []lsp.TextEdit
	h.request("textDocument/formatting", lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: nestedURI},
		Options: lsp.FormattingOptions{
			TabSize:      4,
			InsertSpaces: true,
		},
	}, &nestedEdits)
	if len(nestedEdits) == 0 {
		t.Fatalf("expected nested formatting edits, got none")
	}
	expectedNestedLines := []string{
		"bind main = fn(): {",
		"    let json_value = Json.Object(#[",
		"        \"abc\": Json.Number(Number.Int(123)),",
		"        \"def\": Json.Object(#[",
		"            \"abc\": Json.Array([",
		"                Json.Number(Number.Int(1)),",
		"                Json.Bool(true),",
		"            ])",
		"        ]),",
		"    ])",
		"}",
	}
	expectedNested := strings.Join(expectedNestedLines, "\n") + "\n"
	if nestedEdits[0].NewText != expectedNested {
		t.Errorf("unexpected nested closer formatting:\nGOT:\n%s\nWANT:\n%s", nestedEdits[0].NewText, expectedNested)
	}

	// 5. Test Prepare Rename & Rename
	renameTarget := strings.Index(jsonSrc, "check")
	var prepRange lsp.Range
	h.request("textDocument/prepareRename", lsp.PrepareRenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(jsonSrc, renameTarget),
	}, &prepRange)

	var wsEdit lsp.WorkspaceEdit
	h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(jsonSrc, renameTarget),
		NewName:      "validate_json",
	}, &wsEdit)

	if len(wsEdit.Changes[uri]) == 0 {
		t.Fatalf("expected rename edits for `check`, got none")
	}
	if wsEdit.Changes[uri][0].NewText != "validate_json" {
		t.Errorf("expected newText `validate_json`, got %s", wsEdit.Changes[uri][0].NewText)
	}
}

func TestLSPTestingFeatures(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initRes)
	if initRes.Capabilities.CodeLensProvider == nil || initRes.Capabilities.ExecuteCommandProvider == nil {
		t.Fatalf("missing test capabilities in server capabilities: %+v", initRes.Capabilities)
	}
	h.notify("initialized", map[string]any{})

	testSrc := `bind add = fn(a: int, b: int) int: { a + b }

@test("addition works")
bind test_add = fn(): {
	let r = add(2, 3)
	assert(r == 5)
}

@test
bind test_simple = fn(): {
	assert(true)
}
`
	tmpDir := t.TempDir()
	uri := lsp.PathToURI(filepath.Join(tmpDir, "math_test.ll"))
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       testSrc,
		},
	})

	_ = h.waitDiagnostics(uri)

	// 1. Code Lens: Check that CodeLenses are generated for the file and each @test
	var lenses []lsp.CodeLens
	h.request("textDocument/codeLens", lsp.CodeLensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}, &lenses)

	if len(lenses) != 3 {
		t.Fatalf("expected 3 code lenses, got %d: %+v", len(lenses), lenses)
	}
	if !strings.Contains(lenses[0].Command.Title, "Run All Tests in File") {
		t.Errorf("unexpected file-level code lens: %+v", lenses[0])
	}
	if !strings.Contains(lenses[1].Command.Title, "addition works") {
		t.Errorf("unexpected test code lens: %+v", lenses[1])
	}

	// 2. Hover on @test
	atTestOffset := strings.Index(testSrc, "@test") + 1
	var hov lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(testSrc, atTestOffset),
	}, &hov)
	if !strings.Contains(hov.Contents.Value, "`@test`") {
		t.Errorf("expected hover for @test, got: %s", hov.Contents.Value)
	}

	// 3. Code Actions: Check test execution action when on @test
	testFnOffset := strings.Index(testSrc, "test_add")
	var actions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.OffsetToLSP(testSrc, testFnOffset),
			End:   lsp.OffsetToLSP(testSrc, testFnOffset),
		},
		Context: lsp.CodeActionContext{Diagnostics: []lsp.Diagnostic{}},
	}, &actions)

	foundRunAction := false
	for _, act := range actions {
		if strings.Contains(act.Title, "Run test 'test_add'") {
			foundRunAction = true
			if act.Command == nil || act.Command.Command != "lowlight.runTest" {
				t.Errorf("unexpected command in run test code action: %+v", act)
			}
		}
	}
	if !foundRunAction {
		t.Errorf("missing 'Run test' code action in %+v", actions)
	}

	// 4. Code Action: Generate test for non-test function `add`
	addFnOffset := strings.Index(testSrc, "add =")
	var addActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.OffsetToLSP(testSrc, addFnOffset),
			End:   lsp.OffsetToLSP(testSrc, addFnOffset),
		},
		Context: lsp.CodeActionContext{Diagnostics: []lsp.Diagnostic{}},
	}, &addActions)

	foundGenAction := false
	for _, act := range addActions {
		if strings.Contains(act.Title, "Generate @test for 'add'") {
			foundGenAction = true
			if act.Edit == nil || len(act.Edit.Changes) == 0 {
				t.Errorf("expected edit in generate test action: %+v", act)
			}
		}
	}
	if !foundGenAction {
		t.Errorf("missing 'Generate @test for add' code action in %+v", addActions)
	}

	// 5. Document Symbols: Check that @test functions have @test detail in outline
	var docSyms []lsp.DocumentSymbol
	h.request("textDocument/documentSymbol", lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}, &docSyms)

	foundTestSym := false
	for _, sym := range docSyms {
		if sym.Name == "test_add" && strings.Contains(sym.Detail, "@test") {
			foundTestSym = true
		}
	}
	if !foundTestSym {
		t.Errorf("expected test_add to have @test detail in document symbols: %+v", docSyms)
	}
}

func TestLSPInterfaceAndImplementationDiscovery(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	// Initialize LSP
	var initResp lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initResp)
	if !initResp.Capabilities.ImplementationProvider {
		t.Fatalf("expected ImplementationProvider capability to be true")
	}
	h.notify("initialized", map[string]any{})

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "ifc_test.ll")
	uri := lsp.PathToURI(docPath)

	src := `define Greeter interface [
    greet: fn(self),
]

define Bot struct [
    name: string,
]

bind Bot.greet = fn(self): {
    print(self.name)
}

bind main = fn(): {
    let b = Bot[name: "Alice"]
    b.greet()
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "lowlight",
			Version:    1,
			Text:       src,
		},
	})
	diags := h.waitDiagnostics(uri)
	for _, d := range diags {
		t.Logf("DIAG: %s", d.Message)
	}

	// 1. Hover on interface `Greeter` -> displays `**Implemented by:**` `Bot`
	ifcOffset := strings.Index(src, "Greeter")
	var ifcHover lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, ifcOffset),
	}, &ifcHover)
	if !strings.Contains(ifcHover.Contents.Value, "**Implemented by:**") || !strings.Contains(ifcHover.Contents.Value, "`Bot`") {
		t.Errorf("expected interface hover to list implementing types, got:\n%s", ifcHover.Contents.Value)
	}

	// 2. Hover on struct `Bot` -> displays `**Implements:**` `Greeter`
	botOffset := strings.Index(src, "Bot")
	var botHover lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, botOffset),
	}, &botHover)
	if !strings.Contains(botHover.Contents.Value, "**Implements:**") || !strings.Contains(botHover.Contents.Value, "`Greeter`") {
		t.Errorf("expected struct hover to list implemented interfaces, got:\n%s", botHover.Contents.Value)
	}

	// 3. textDocument/implementation on interface `Greeter` -> jumps to `Bot`
	var ifcImpls []lsp.Location
	h.request("textDocument/implementation", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, ifcOffset),
	}, &ifcImpls)
	if len(ifcImpls) == 0 {
		t.Fatalf("expected implementation locations for Greeter, got none")
	}
	botLine := 4 // 0-based line for struct Bot
	if ifcImpls[0].Range.Start.Line != botLine {
		t.Errorf("expected implementation location to point to line %d, got %d", botLine, ifcImpls[0].Range.Start.Line)
	}

	// 4. textDocument/implementation on struct `Bot` -> returns implemented interfaces (including Greeter)
	var structImpls []lsp.Location
	h.request("textDocument/implementation", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, botOffset),
	}, &structImpls)
	if len(structImpls) == 0 {
		t.Fatalf("expected implementation locations for Bot, got none")
	}
	foundGreeterLoc := false
	for _, loc := range structImpls {
		if loc.Range.Start.Line == 0 {
			foundGreeterLoc = true
			break
		}
	}
	if !foundGreeterLoc {
		t.Errorf("expected struct implementation locations to contain Greeter at line 0, got: %+v", structImpls)
	}

	// 5. textDocument/codeLens -> includes interface implementations count & struct implements lens
	var lenses []lsp.CodeLens
	h.request("textDocument/codeLens", lsp.CodeLensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}, &lenses)

	foundIfcLens := false
	foundStructLens := false
	for _, l := range lenses {
		if l.Command != nil {
			if strings.Contains(l.Command.Title, "1 implementation") {
				foundIfcLens = true
				if l.Command.Command != "lowlight.showImplementationsQuickPick" {
					t.Errorf("expected command lowlight.showImplementationsQuickPick, got %s", l.Command.Command)
				}
			}
			if strings.Contains(l.Command.Title, "implements") {
				foundStructLens = true
				if l.Command.Command != "lowlight.showImplementationsQuickPick" {
					t.Errorf("expected command lowlight.showImplementationsQuickPick, got %s", l.Command.Command)
				}
				raw, _ := json.Marshal(l.Command.Arguments)
				if !strings.Contains(string(raw), "Greeter") {
					t.Errorf("expected Bot dropdown items to include Greeter, got %s", string(raw))
				}
			}
		}
	}
	if !foundIfcLens {
		t.Errorf("expected interface CodeLens '1 implementation', got lenses: %+v", lenses)
	}
	if !foundStructLens {
		t.Errorf("expected struct CodeLens implements, got lenses: %+v", lenses)
	}
}

func TestLSPCoreListAndInterfacesDiscovery(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initResp lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initResp)
	h.notify("initialized", map[string]any{})

	listPath := filepath.Join(root, "std", "core", "list.ll")
	listBytes, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("failed to read list.ll: %v", err)
	}
	listSrc := string(listBytes)
	listURI := lsp.PathToURI(listPath)

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        listURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       listSrc,
		},
	})
	_ = h.waitDiagnostics(listURI)

	// 1. Check CodeLens on List[T] in list.ll
	var listLenses []lsp.CodeLens
	h.request("textDocument/codeLens", lsp.CodeLensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: listURI},
	}, &listLenses)

	foundListImplLens := false
	for _, l := range listLenses {
		if l.Command != nil && strings.Contains(l.Command.Title, "implements") {
			raw, _ := json.Marshal(l.Command.Arguments)
			if strings.Contains(string(raw), "Interfaces implemented by List") {
				foundListImplLens = true
				if l.Command.Command != "lowlight.showImplementationsQuickPick" {
					t.Errorf("expected lowlight.showImplementationsQuickPick command, got %s", l.Command.Command)
				}
				if !strings.Contains(string(raw), "Indexable[T]") {
					t.Errorf("expected List[T] dropdown items to include Indexable[T], got %s", string(raw))
				}
				break
			}
		}
	}
	if !foundListImplLens {
		t.Errorf("expected List[T] to have CodeLens implementing Indexable[T], got: %+v", listLenses)
	}

	// 2. Check Hover on List[T]
	listOffset := strings.Index(listSrc, "define List[T]")
	if listOffset < 0 {
		t.Fatalf("could not find 'define List[T]' in list.ll")
	}
	listNameOffset := listOffset + len("define ")
	var listHover lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: listURI},
		Position:     lsp.OffsetToLSP(listSrc, listNameOffset),
	}, &listHover)
	if !strings.Contains(listHover.Contents.Value, "**Implements:**") || !strings.Contains(listHover.Contents.Value, "`Indexable[T]`") {
		t.Errorf("expected List[T] hover to list implemented interfaces, got:\n%s", listHover.Contents.Value)
	}

	// 3. Check Go to Implementation on List[T] -> jumps to interfaces.ll (Indexable, Collectable, etc.)
	var listImpls []lsp.Location
	h.request("textDocument/implementation", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: listURI},
		Position:     lsp.OffsetToLSP(listSrc, listNameOffset),
	}, &listImpls)
	if len(listImpls) == 0 {
		t.Fatalf("expected implementation locations for List[T], got none")
	}
	foundIfcLocation := false
	for _, loc := range listImpls {
		if strings.HasSuffix(loc.URI, "interfaces.ll") {
			foundIfcLocation = true
			break
		}
	}
	if !foundIfcLocation {
		t.Errorf("expected List[T] implementations to point to interfaces.ll, got: %+v", listImpls)
	}

	// 4. Check interfaces.ll
	ifcPath := filepath.Join(root, "std", "core", "interfaces.ll")
	ifcBytes, err := os.ReadFile(ifcPath)
	if err != nil {
		t.Fatalf("failed to read interfaces.ll: %v", err)
	}
	ifcSrc := string(ifcBytes)
	ifcURI := lsp.PathToURI(ifcPath)

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        ifcURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       ifcSrc,
		},
	})
	_ = h.waitDiagnostics(ifcURI)

	// Check CodeLens on Indexable[T] in interfaces.ll
	var ifcLenses []lsp.CodeLens
	h.request("textDocument/codeLens", lsp.CodeLensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: ifcURI},
	}, &ifcLenses)

	foundIndexableImplCount := false
	for _, l := range ifcLenses {
		if l.Command != nil && strings.Contains(l.Command.Title, "implementations") {
			raw, _ := json.Marshal(l.Command.Arguments)
			if strings.Contains(string(raw), "Implementations of Indexable") {
				foundIndexableImplCount = true
				if l.Command.Command != "lowlight.showImplementationsQuickPick" {
					t.Errorf("expected lowlight.showImplementationsQuickPick command, got %s", l.Command.Command)
				}
				if !strings.Contains(string(raw), "List[T]") {
					t.Errorf("expected Indexable[T] dropdown items to include List[T], got %s", string(raw))
				}
				break
			}
		}
	}
	if !foundIndexableImplCount {
		t.Errorf("expected Indexable[T] summary CodeLens to list implementing types (including List[T]), got: %+v", ifcLenses)
	}

	// Check Hover on Indexable[T]
	idxOffset := strings.Index(ifcSrc, "define Indexable[T]")
	if idxOffset < 0 {
		t.Fatalf("could not find 'define Indexable[T]' in interfaces.ll")
	}
	idxNameOffset := idxOffset + len("define ")
	var idxHover lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: ifcURI},
		Position:     lsp.OffsetToLSP(ifcSrc, idxNameOffset),
	}, &idxHover)
	if !strings.Contains(idxHover.Contents.Value, "**Implemented by:**") || !strings.Contains(idxHover.Contents.Value, "`List[T]`") {
		t.Errorf("expected Indexable[T] hover to list implemented by List[T], got:\n%s", idxHover.Contents.Value)
	}

	// Verify that KeyIndexable does NOT list List[T]
	keyIdxOffset := strings.Index(ifcSrc, "define KeyIndexable[K, V]")
	if keyIdxOffset < 0 {
		t.Fatalf("could not find 'define KeyIndexable[K, V]' in interfaces.ll")
	}
	keyIdxNameOffset := keyIdxOffset + len("define ")
	var keyIdxHover lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: ifcURI},
		Position:     lsp.OffsetToLSP(ifcSrc, keyIdxNameOffset),
	}, &keyIdxHover)
	if strings.Contains(keyIdxHover.Contents.Value, "`List[T]`") {
		t.Errorf("KeyIndexable must NOT list List[T] as an implementation, got:\n%s", keyIdxHover.Contents.Value)
	}

	// 5. Check interface_test.ll discovery & reflection
	testPath := filepath.Join(root, "interface_test.ll")
	testBytes, err := os.ReadFile(testPath)
	if err == nil {
		testSrc := string(testBytes)
		testURI := lsp.PathToURI(testPath)
		h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{
				URI:        testURI,
				LanguageID: "lowlight",
				Version:    1,
				Text:       testSrc,
			},
		})
		_ = h.waitDiagnostics(testURI)

		// CodeLens on interface_test.ll -> type 't' has 'implements: Indexable[int]'
		var testLenses []lsp.CodeLens
		h.request("textDocument/codeLens", lsp.CodeLensParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		}, &testLenses)

		foundTLens := false
		for _, l := range testLenses {
			if l.Command != nil && strings.Contains(l.Command.Title, "implements") {
				raw, _ := json.Marshal(l.Command.Arguments)
				if strings.Contains(string(raw), "Indexable[int]") {
					foundTLens = true
					break
				}
			}
		}
		if !foundTLens {
			t.Errorf("expected type 't' in interface_test.ll to have CodeLens implementing Indexable[int], got: %+v", testLenses)
		}

		// Re-check Indexable[T] CodeLens -> should now include type 't' from interface_test.ll!
		var updatedIfcLenses []lsp.CodeLens
		h.request("textDocument/codeLens", lsp.CodeLensParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: ifcURI},
		}, &updatedIfcLenses)

		foundTInIfc := false
		for _, l := range updatedIfcLenses {
			if l.Command != nil && strings.Contains(l.Command.Title, "implementations") {
				raw, _ := json.Marshal(l.Command.Arguments)
				if strings.Contains(string(raw), "t") {
					foundTInIfc = true
					break
				}
			}
		}
		if !foundTInIfc {
			t.Errorf("expected Indexable[T] CodeLens dropdown to discover type 't' from interface_test.ll, got: %+v", updatedIfcLenses)
		}
	}
}

func TestLSPMainRunCodeLens(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initResp lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initResp)
	h.notify("initialized", map[string]any{})

	testPath := filepath.Join(root, "examples", "hello.ll")
	testBytes, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("could not read examples/hello.ll: %v", err)
	}
	testSrc := string(testBytes)
	testURI := lsp.PathToURI(testPath)

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        testURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       testSrc,
		},
	})
	_ = h.waitDiagnostics(testURI)

	var lenses []lsp.CodeLens
	h.request("textDocument/codeLens", lsp.CodeLensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
	}, &lenses)

	foundMainRun := false
	for _, l := range lenses {
		if l.Command != nil && l.Command.Title == "▶ Run" {
			if l.Command.Command == "lowlight.runFile" || l.Command.Command == "lowlight.runMain" {
				foundMainRun = true
				if len(l.Command.Arguments) == 0 {
					t.Errorf("expected run command to have file argument, got empty arguments: %+v", l.Command)
				}
				break
			}
		}
	}
	if !foundMainRun {
		t.Fatalf("expected '▶ Run' CodeLens on main in examples/hello.ll, got: %+v", lenses)
	}

	// Verify executing 'lowlight.runFile' via workspace/executeCommand
	argBytes, _ := json.Marshal(testPath)
	var execRes interface{}
	h.request("workspace/executeCommand", lsp.ExecuteCommandParams{
		Command:   "lowlight.runFile",
		Arguments: []json.RawMessage{argBytes},
	}, &execRes)
}

func TestLSPTuples(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)

	var initResp lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(root),
	}, &initResp)
	h.notify("initialized", map[string]any{})

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "tuple_lsp_test.ll")
	testSrc := `define MyPair +[int, string]
bind MyPair.first = fn(self) int: self.0
bind test_fn = fn(pair: +[int, string], count: int) int: { count }
bind main = fn() int: {
    let my_val = 42
    let pair = +[my_val, "hello"]
    let y = pair.0
    let res = test_fn(+[10, "arg"], 0)
    let p_dist: MyPair = +[100, "world"]
    let z = p_dist.0
    let unp_num, unp_str = unpack pair
    0
}
`
	if err := os.WriteFile(docPath, []byte(testSrc), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	testURI := lsp.PathToURI(docPath)

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        testURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       testSrc,
		},
	})
	_ = h.waitDiagnostics(testURI)

	// 1. Completion after `pair.` (line 6, character 17: right between . and 0)
	var compList lsp.CompletionList
	h.request("textDocument/completion", lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 6, Character: 17},
	}, &compList)

	found0 := false
	found1 := false
	foundCopy := false
	foundToStr := false
	for _, item := range compList.Items {
		if item.Label == "0" {
			found0 = true
			if !strings.Contains(item.Detail, "int") {
				t.Errorf("expected completion 0 to have detail with int, got %q", item.Detail)
			}
		}
		if item.Label == "1" {
			found1 = true
			if !strings.Contains(item.Detail, "string") {
				t.Errorf("expected completion 1 to have detail with string, got %q", item.Detail)
			}
		}
		if item.Label == "copy" {
			foundCopy = true
		}
		if item.Label == "to_string" {
			foundToStr = true
		}
	}
	if !found0 || !found1 {
		t.Errorf("expected completions '0' and '1' for tuple, got: %+v", compList.Items)
	}
	if !foundCopy || !foundToStr {
		t.Errorf("expected built-ins 'copy' and 'to_string' for tuple, got: %+v", compList.Items)
	}

	// 2. Hover inside tuple literal on `my_val` (line 5, column 19)
	var hov lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 5, Character: 19},
	}, &hov)
	if hov.Contents.Value == "" || !strings.Contains(hov.Contents.Value, "my_val") {
		t.Errorf("expected hover on 'my_val' inside tuple literal, got: %q", hov.Contents.Value)
	}

	// 3. Definition on `my_val` inside tuple literal (line 5, column 19)
	var defLoc lsp.Location
	h.request("textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 5, Character: 19},
	}, &defLoc)
	if defLoc.URI == "" {
		t.Errorf("expected definition location for 'my_val' inside tuple literal")
	} else if defLoc.Range.Start.Line != 4 {
		t.Errorf("expected definition to point to line 4 (let my_val), got line %d", defLoc.Range.Start.Line)
	}

	// 4. Hover on tuple dot element `pair.0` (line 6, character 17)
	var hovElem lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 6, Character: 17},
	}, &hovElem)
	if !strings.Contains(hovElem.Contents.Value, "tuple element") {
		t.Errorf("expected hover on 'pair.0' to describe tuple element, got: %q", hovElem.Contents.Value)
	}

	// 5. Signature Help inside `test_fn(+[10, "arg"], 0)` (line 7, column 36)
	var sigHelp lsp.SignatureHelp
	h.request("textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 7, Character: 36},
	}, &sigHelp)
	if len(sigHelp.Signatures) == 0 {
		t.Errorf("expected signature help for test_fn after tuple literal argument")
	} else if sigHelp.ActiveParameter != 1 {
		t.Errorf("expected active parameter 1 (count: int) after tuple argument, got parameter %d", sigHelp.ActiveParameter)
	}

	// 6. Distinct tuple completion after `p_dist.` (line 9, character 19)
	var distCompList lsp.CompletionList
	h.request("textDocument/completion", lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 9, Character: 19},
	}, &distCompList)

	foundDist0 := false
	foundDist1 := false
	foundDistFirst := false
	for _, item := range distCompList.Items {
		if item.Label == "0" {
			foundDist0 = true
		}
		if item.Label == "1" {
			foundDist1 = true
		}
		if item.Label == "first" {
			foundDistFirst = true
		}
	}
	if !foundDist0 || !foundDist1 || !foundDistFirst {
		t.Errorf("expected completions '0', '1', and 'first' for distinct tuple MyPair, got: %+v", distCompList.Items)
	}

	// 7. Distinct tuple hover on `p_dist.0` (line 9, character 19)
	var hovDistElem lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 9, Character: 19},
	}, &hovDistElem)
	if !strings.Contains(hovDistElem.Contents.Value, "MyPair.0") {
		t.Errorf("expected hover on 'p_dist.0' to describe MyPair.0, got: %q", hovDistElem.Contents.Value)
	}

	// 8. Hover on unpacked variable `unp_str` (line 10, character 18)
	var hovUnp lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Position:     lsp.Position{Line: 10, Character: 18},
	}, &hovUnp)
	if !strings.Contains(hovUnp.Contents.Value, "unp_str: string") {
		t.Errorf("expected hover on unpacked variable 'unp_str' to show 'unp_str: string', got: %q", hovUnp.Contents.Value)
	}
}

func TestLSPIncrementalSyncConfigAndCrossFileDeps(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	// Workspace OUTSIDE compiler repo (no local `std/` directory)
	wsDir := t.TempDir()

	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{
		RootURI: lsp.PathToURI(wsDir),
	}, &initRes)
	if initRes.Capabilities.TextDocumentSync.Change != 2 {
		t.Fatalf("expected incremental sync (2), got %d", initRes.Capabilities.TextDocumentSync.Change)
	}
	if !initRes.Capabilities.ReferencesProvider || !initRes.Capabilities.SemanticTokensProvider.Full ||
		!initRes.Capabilities.TypeDefinitionProvider || !initRes.Capabilities.DeclarationProvider ||
		!initRes.Capabilities.WorkspaceSymbolProvider || !initRes.Capabilities.FoldingRangeProvider ||
		!initRes.Capabilities.SelectionRangeProvider {
		t.Fatalf("expected all Phase 5b LSP capabilities enabled: %+v", initRes.Capabilities)
	}
	h.notify("initialized", map[string]any{})

	helperDir := filepath.Join(wsDir, "helper")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(helperDir, "helper.ll")
	appPath := filepath.Join(wsDir, "app.ll")
	helperURI := lsp.PathToURI(helperPath)
	appURI := lsp.PathToURI(appPath)

	helperSrc := "bind double_it = fn(x: int) int: x * 2\n"
	appSrc := "import \"./helper\"\nimport \"std/math\"\n\nbind main = fn(): {\n\tlet v = helper.double_it(math.clamp_int(-5, 0, 10))\n\tprintln(v)\n}\n"

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: helperURI, LanguageID: "lowlight", Version: 1, Text: helperSrc},
	})
	if diags := h.waitDiagnostics(helperURI); len(diags) != 0 {
		t.Fatalf("unexpected helper diagnostics: %+v", diags)
	}

	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: appURI, LanguageID: "lowlight", Version: 1, Text: appSrc},
	})
	if diags := h.waitDiagnostics(appURI); len(diags) != 0 {
		t.Fatalf("unexpected app diagnostics in standalone workspace: %+v", diags)
	}

	// Incremental edit in helper.ll breaking signature (`x: string`) should invalidate app.ll cache & re-publish error on app.ll!
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: helperURI, Version: 2},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{
				Range: &lsp.Range{
					Start: lsp.Position{Line: 0, Character: 23},
					End:   lsp.Position{Line: 0, Character: 26},
				},
				Text: "string",
			},
			{
				Range: &lsp.Range{
					Start: lsp.Position{Line: 0, Character: 36},
					End:   lsp.Position{Line: 0, Character: 41},
				},
				Text: "x.len()",
			},
		},
	})
	_ = h.waitDiagnostics(helperURI)
	if len(h.diags[appURI]) == 0 {
		t.Fatalf("expected cross-file diagnostic on app.ll after incremental incompatible signature change in helper.ll")
	}

	// Fix helper.ll back to `(x: int) int: x * 2` -> clears error on app.ll!
	h.notify("textDocument/didChange", lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: helperURI, Version: 3},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: helperSrc}},
	})
	_ = h.waitDiagnostics(helperURI)
	if len(h.diags[appURI]) != 0 {
		t.Fatalf("expected app.ll diagnostics cleared after fixing helper.ll, got: %+v", h.diags[appURI])
	}

	// Test workspace/didChangeConfiguration (formatting & inlayHints toggles)
	h.notify("workspace/didChangeConfiguration", lsp.DidChangeConfigurationParams{
		Settings: []byte(`{"lowlight":{"format":{"indent":"spaces","tabWidth":2,"lineWidth":80},"inlayHints":{"types":true,"parameters":false}}}`),
	})

	fmtPath := filepath.Join(wsDir, "fmt_cfg.ll")
	fmtURI := lsp.PathToURI(fmtPath)
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        fmtURI,
			LanguageID: "lowlight",
			Version:    1,
			Text:       "bind add = fn(a: int, b: int) int: {\nlet sum = a + b\nsum\n}\n",
		},
	})
	_ = h.waitDiagnostics(fmtURI)

	var fmtEdits []lsp.TextEdit
	h.request("textDocument/formatting", lsp.DocumentFormattingParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: fmtURI},
		Options:      lsp.FormattingOptions{TabSize: 4, InsertSpaces: false},
	}, &fmtEdits)
	if len(fmtEdits) == 0 || !strings.Contains(fmtEdits[0].NewText, "  let sum = a + b") {
		t.Errorf("expected workspace/didChangeConfiguration indent=spaces tabWidth=2 to apply, got: %+v", fmtEdits)
	}

	var hints []lsp.InlayHint
	h.request("textDocument/inlayHint", lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: fmtURI},
		Range:        lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 10, Character: 0}},
	}, &hints)
	if len(hints) == 0 || hints[0].Kind != lsp.InlayHintType {
		t.Errorf("expected type inlay hint when inlayHints.types=true, got: %+v", hints)
	}

	// Test lowlight.showLocations
	var showRes map[string]any
	h.request("workspace/executeCommand", lsp.ExecuteCommandParams{
		Command:   "lowlight.showLocations",
		Arguments: []json.RawMessage{[]byte(fmt.Sprintf(`[%q]`, appURI)), []byte(`{"line":0,"character":0}`), []byte(fmt.Sprintf(`[{"uri":%q,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":6}}}]`, helperURI))},
	}, &showRes)
	if showRes["count"] != float64(1) {
		t.Errorf("expected showLocations count=1, got: %+v", showRes)
	}
}

func TestLSPDocLinksSignatureAndCompletionDocs(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	wsDir := t.TempDir()
	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(wsDir)}, &initRes)
	h.notify("initialized", map[string]any{})

	docFile := filepath.Join(wsDir, "docs.ll")
	uri := lsp.PathToURI(docFile)
	src := `// 2D coordinate vector.
define Vec2 struct [
	// Horizontal offset.
	x: float,
	// Vertical offset.
	y: float,
]

// Scales [` + "`Vec2`" + `] components by factor ` + "`k`" + `.
//
// - ` + "`v`" + `: Input vector to scale.
// - ` + "`k`" + `: Scalar multiplier.
bind scale = fn(v: Vec2, k: float) Vec2: Vec2[x: v.x * k, y: v.y * k]

/* See [` + "`scale`" + `] and [` + "`Vec2.x`" + `] for scaling details. */
bind main = fn(): {
	let p = Vec2[x: 1.0, y: 2.0]
	let q = scale(p, 3.0)
	let d = q.x
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: src},
	})
	if diags := h.waitDiagnostics(uri); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	// 1. Hover on `scale(p, 3.0)` renders resolved Markdown link to `Vec2` + `[View source]`
	scaleCallOff := strings.Index(src, "scale(p")
	var hov lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, scaleCallOff),
	}, &hov)
	if !strings.Contains(hov.Contents.Value, "[`Vec2`]("+uri+"#L2)") || !strings.Contains(hov.Contents.Value, "[View source]("+uri+"#L13)") {
		t.Errorf("expected resolved intra-doc link and [View source] in hover, got:\n%s", hov.Contents.Value)
	}

	// 2. SignatureHelp inside `scale(p, |3.0)` includes Iteration B doc & parameter doc!
	sigOff := strings.Index(src, "scale(p, ") + len("scale(p, ")
	var sig lsp.SignatureHelp
	h.request("textDocument/signatureHelp", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, sigOff),
	}, &sig)
	if len(sig.Signatures) == 0 || sig.Signatures[0].Documentation == nil || !strings.Contains(sig.Signatures[0].Documentation.Value, "Scales") {
		t.Fatalf("expected rendered doc on SignatureInformation, got: %+v", sig)
	}
	if len(sig.Signatures[0].Parameters) < 2 || sig.Signatures[0].Parameters[1].Documentation == nil || !strings.Contains(sig.Signatures[0].Parameters[1].Documentation.Value, "Scalar multiplier") {
		t.Errorf("expected per-parameter doc on ParameterInformation[1], got: %+v", sig.Signatures[0].Parameters)
	}

	// 3. Completion on `q.` includes `x` field doc comment
	qDotOff := strings.Index(src, "q.x") + 2
	var comp lsp.CompletionList
	h.request("textDocument/completion", lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, qDotOff),
	}, &comp)
	foundFieldDoc := false
	for _, it := range comp.Items {
		if it.Label == "x" && it.Documentation != nil && strings.Contains(it.Documentation.Value, "Horizontal offset") {
			foundFieldDoc = true
		}
	}
	if !foundFieldDoc {
		t.Errorf("expected field doc in completion for `q.x`, got: %+v", comp.Items)
	}

	// 4. Cursor INSIDE `/* See [`scale`] ... */` comment: Definition + Hover!
	docLinkOff := strings.Index(src, "[`scale`]") + 3
	var docDef lsp.Location
	h.request("textDocument/definition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, docLinkOff),
	}, &docDef)
	if docDef.URI != uri || docDef.Range.Start.Line != 12 {
		t.Errorf("expected go-to-definition inside comment [`scale`] to jump to line 12, got: %+v", docDef)
	}

	var docHov lsp.Hover
	h.request("textDocument/hover", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.OffsetToLSP(src, docLinkOff),
	}, &docHov)
	if !strings.Contains(docHov.Contents.Value, "bind scale") || !strings.Contains(docHov.Contents.Value, "[View source]") {
		t.Errorf("expected hover inside comment [`scale`] to render target signature & source link, got: %q", docHov.Contents.Value)
	}
}

func TestLSPSemanticTokensReferencesRenameAndNavigation(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	wsDir := t.TempDir()
	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(wsDir)}, &initRes)
	h.notify("initialized", map[string]any{})

	shapesDir := filepath.Join(wsDir, "shapes")
	if err := os.MkdirAll(shapesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(shapesDir, "shapes.ll")
	mainPath := filepath.Join(wsDir, "main.ll")
	libURI := lsp.PathToURI(libPath)
	mainURI := lsp.PathToURI(mainPath)

	libSrc := `// Printable geometry.
define Shape interface [
	area: fn(self) float,
]

define Square struct [
	side: float,
]

bind Square.area = fn(self) float: self.side * self.side

bind helper_sum = fn(val: int) int: {
	let tmp = val + 1
	tmp
}
`
	mainSrc := `import "./shapes"

bind use_square = fn(s: shapes.Square) float: s.area()

bind main = fn(): {
	let tmp = 99
	let x = shapes.helper_sum(tmp)
	let sq = shapes.Square[side: 3.0]
	println(use_square(sq) + float(x))
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: libURI, LanguageID: "lowlight", Version: 1, Text: libSrc},
	})
	_ = h.waitDiagnostics(libURI)
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: mainURI, LanguageID: "lowlight", Version: 1, Text: mainSrc},
	})
	if diags := h.waitDiagnostics(mainURI); len(diags) != 0 {
		t.Fatalf("unexpected main diagnostics: %+v", diags)
	}

	// 1. Semantic Tokens full
	var semToks lsp.SemanticTokens
	h.request("textDocument/semanticTokens/full", lsp.SemanticTokensParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
	}, &semToks)
	if len(semToks.Data) == 0 || len(semToks.Data)%5 != 0 {
		t.Fatalf("expected valid 5-tuple semantic tokens, got len=%d", len(semToks.Data))
	}

	// 2. Declaration on `Square.area` method -> jumps to `Shape.area` interface method!
	areaMethOff := strings.Index(libSrc, "Square.area") + len("Square.")
	var declLoc lsp.Location
	h.request("textDocument/declaration", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
		Position:     lsp.OffsetToLSP(libSrc, areaMethOff),
	}, &declLoc)
	if declLoc.URI != libURI || declLoc.Range.Start.Line != 2 {
		t.Errorf("expected textDocument/declaration on Square.area -> Shape.area line 2, got: %+v", declLoc)
	}

	// 3. TypeDefinition on parameter `s` of `use_square` -> jumps to `define Square struct`!
	sParamOff := strings.Index(mainSrc, "(s: shapes.Square)") + 1
	var tdLoc lsp.Location
	h.request("textDocument/typeDefinition", lsp.TextDocumentPositionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: mainURI},
		Position:     lsp.OffsetToLSP(mainSrc, sParamOff),
	}, &tdLoc)
	if tdLoc.URI != libURI || tdLoc.Range.Start.Line != 5 {
		t.Errorf("expected typeDefinition on `s` -> define Square (line 5 of shapes.ll), got: %+v", tdLoc)
	}

	// 4. Cross-file References + Rename on `helper_sum`
	helperOff := strings.Index(libSrc, "helper_sum")
	var refs []lsp.Location
	h.request("textDocument/references", lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
		Position:     lsp.OffsetToLSP(libSrc, helperOff),
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}, &refs)
	if len(refs) < 2 {
		t.Errorf("expected >= 2 references across shapes.ll & main.ll, got: %+v", refs)
	}

	var crossRename lsp.WorkspaceEdit
	h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
		Position:     lsp.OffsetToLSP(libSrc, helperOff),
		NewName:      "add_one",
	}, &crossRename)
	if len(crossRename.Changes[libURI]) == 0 || len(crossRename.Changes[mainURI]) == 0 {
		t.Errorf("expected cross-file rename edits in both shapes.ll and main.ll, got: %+v", crossRename.Changes)
	}

	// 5. Scope-aware local rename: renaming `tmp` in `shapes.ll` MUST NOT touch `tmp` in `main.ll`!
	localTmpOff := strings.Index(libSrc, "let tmp") + 4
	var localRename lsp.WorkspaceEdit
	h.request("textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
		Position:     lsp.OffsetToLSP(libSrc, localTmpOff),
		NewName:      "accum",
	}, &localRename)
	if len(localRename.Changes[libURI]) != 2 || len(localRename.Changes[mainURI]) != 0 {
		t.Errorf("expected scoped rename ONLY in shapes.ll (2 edits) and 0 in main.ll, got: %+v", localRename.Changes)
	}

	// 6. Workspace Symbols, Folding Ranges, Selection Ranges
	var wsSyms []lsp.SymbolInformation
	h.request("workspace/symbol", lsp.WorkspaceSymbolParams{Query: "Square"}, &wsSyms)
	if len(wsSyms) == 0 {
		t.Errorf("expected workspace/symbol match for 'Square'")
	}

	var folds []lsp.FoldingRange
	h.request("textDocument/foldingRange", lsp.FoldingRangeParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
	}, &folds)
	if len(folds) < 3 {
		t.Errorf("expected folding ranges in shapes.ll, got: %+v", folds)
	}

	var selRngs []*lsp.SelectionRange
	h.request("textDocument/selectionRange", lsp.SelectionRangeParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: libURI},
		Positions:    []lsp.Position{lsp.OffsetToLSP(libSrc, localTmpOff)},
	}, &selRngs)
	if len(selRngs) != 1 || selRngs[0].Parent == nil {
		t.Errorf("expected nested SelectionRange chain, got: %+v", selRngs)
	}
}

func TestLSPCodeGenerationAndRefactorings(t *testing.T) {
	h := newHarness(t)
	root := repoRoot(t)
	os.Setenv("LOWLIGHT_ROOT", root)
	t.Cleanup(func() { os.Unsetenv("LOWLIGHT_ROOT") })

	wsDir := t.TempDir()
	var initRes lsp.InitializeResult
	h.request("initialize", lsp.InitializeParams{RootURI: lsp.PathToURI(wsDir)}, &initRes)
	h.notify("initialized", map[string]any{})

	genPath := filepath.Join(wsDir, "codegen.ll")
	uri := lsp.PathToURI(genPath)

	src := `define Rect struct [
	w: float,
	h: float,
]

define Shape interface [
	area: fn(self) float,
]

@must_use
bind make_code = fn() int: 42

let bad_global = 10

bind use_all = fn(flag: bool) int: {
	let r = Rect[]
	let p = null_ptr[u8]()
	make_code()
	let sum = 1 + 2 * 3
	if(flag): {
		10
	} else: {
		20
	}
}
`
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "lowlight", Version: 1, Text: src},
	})
	diags := h.waitDiagnostics(uri)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics on codegen.ll sample")
	}

	var allActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 30, Character: 0}},
		Context:      lsp.CodeActionContext{Diagnostics: diags},
	}, &allActions)

	titles := make(map[string]lsp.CodeAction)
	for _, a := range allActions {
		titles[a.Title] = a
	}

	// Quick fixes
	for _, want := range []string{
		"Convert top-level 'let' to 'bind'",
		"Wrap in 'unsafe'",
		"Bind return value ('let res = ...')",
		"Explicitly ignore return value ('let _ = ...')",
		"Populate missing fields for 'Rect'",
	} {
		if _, ok := titles[want]; !ok {
			t.Errorf("expected diagnostic quickfix %q, got titles: %v", want, allActions)
		}
	}
	if act, ok := titles["Populate missing fields for 'Rect'"]; ok && act.Edit != nil {
		edits := act.Edit.Changes[uri]
		if len(edits) == 0 || !strings.Contains(edits[0].NewText, `panic("todo")`) {
			t.Errorf("expected minimal panic(\"todo\") struct field stub, got: %+v", edits)
		}
	}

	// Cursor on `Rect` declaration: Implement interfaces (`Shape`, `Printable`), constructor `Rect.new`, doc comment!
	rectOff := strings.Index(src, "Rect struct")
	var rectActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: lsp.OffsetToLSP(src, rectOff), End: lsp.OffsetToLSP(src, rectOff)},
		Context:      lsp.CodeActionContext{},
	}, &rectActions)
	rectTitles := make(map[string]lsp.CodeAction)
	for _, a := range rectActions {
		rectTitles[a.Title] = a
	}
	if implAct, ok := rectTitles["Implement Shape for Rect"]; !ok {
		t.Errorf("expected 'Implement Shape for Rect' code action, got: %+v", rectActions)
	} else {
		ed := implAct.Edit.Changes[uri][0].NewText
		if !strings.Contains(ed, `bind Rect.area = fn(self) float: panic("todo")`) {
			t.Errorf("unexpected interface stub: %q", ed)
		}
	}
	if _, ok := rectTitles["Generate constructor 'Rect.new'"]; !ok {
		t.Errorf("expected 'Generate constructor Rect.new' action")
	}
	if _, ok := rectTitles["Generate doc comment for 'Rect'"]; !ok {
		t.Errorf("expected 'Generate doc comment for Rect' action")
	}

	// Refactorings inside `use_all`: unwrap single-expression `{ 10 }` block + invert `if/else` + extract variable `2 * 3`
	tenOff := strings.Index(src, "10\n\t} else")
	var blockActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: lsp.OffsetToLSP(src, tenOff), End: lsp.OffsetToLSP(src, tenOff)},
		Context:      lsp.CodeActionContext{},
	}, &blockActions)
	blockTitles := make(map[string]bool)
	for _, a := range blockActions {
		blockTitles[a.Title] = true
	}
	if !blockTitles["Unwrap single-expression '{ ... }' block"] || !blockTitles["Invert 'if' / 'else' branches"] {
		t.Errorf("expected unwrap block and invert if/else actions, got: %+v", blockActions)
	}

	extStart := strings.Index(src, "2 * 3")
	extEnd := extStart + len("2 * 3")
	var extActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: lsp.OffsetToLSP(src, extStart), End: lsp.OffsetToLSP(src, extEnd)},
		Context:      lsp.CodeActionContext{},
	}, &extActions)
	extTitles := make(map[string]bool)
	for _, a := range extActions {
		extTitles[a.Title] = true
	}
	if !extTitles["Extract expression into 'let extracted'"] || !extTitles["Extract selection into function 'extracted_fn'"] {
		t.Errorf("expected extract variable and extract function actions, got: %+v", extActions)
	}

	// Auto-import missing symbol (`clamp_int` -> `std/math`)
	impPath := filepath.Join(wsDir, "autoimp.ll")
	impURI := lsp.PathToURI(impPath)
	impSrc := "bind main = fn(): {\n\tlet v = clamp_int(5, 0, 10)\n\tprintln(v)\n}\n"
	h.notify("textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: impURI, LanguageID: "lowlight", Version: 1, Text: impSrc},
	})
	impDiags := h.waitDiagnostics(impURI)
	var impActions []lsp.CodeAction
	h.request("textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: impURI},
		Range:        lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 4, Character: 0}},
		Context:      lsp.CodeActionContext{Diagnostics: impDiags},
	}, &impActions)
	foundMathImp := false
	for _, a := range impActions {
		if strings.Contains(a.Title, `Import "std/math"`) {
			foundMathImp = true
			break
		}
	}
	if !foundMathImp {
		t.Errorf("expected auto-import 'Import \"std/math\"' action, got: %+v", impActions)
	}
}

