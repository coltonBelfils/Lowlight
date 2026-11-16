package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lowlight/internal/diag"
	"lowlight/internal/driver"
	"lowlight/internal/sema"
)

// Server is a stateful Lowlight Language Server Protocol (LSP 3.17) instance.
type Server struct {
	conn *Conn

	mu          sync.RWMutex
	rootDir     string
	docs        map[string]string              // canonical abs path -> open buffer content
	docURIs     map[string]string              // canonical abs path -> exact client URI
	cachedProgs map[string]*sema.CheckedProgram // canonical abs path -> latest CheckedProgram
	latestDiags map[string][]Diagnostic        // canonical abs path -> latest published LSP diagnostics
	shutdownReq bool
}

// NewServer creates a new Lowlight LSP server bound to `r` and `w` (typically stdin/stdout).
func NewServer(r io.Reader, w io.Writer, logW io.Writer) *Server {
	return &Server{
		conn:        NewConn(r, w, logW),
		docs:        make(map[string]string),
		docURIs:     make(map[string]string),
		cachedProgs: make(map[string]*sema.CheckedProgram),
		latestDiags: make(map[string][]Diagnostic),
	}
}

// Serve runs the main JSON-RPC message loop until `exit` or EOF.
// Returns process exit code (0 on clean shutdown+exit or EOF, 1 if `exit` received without `shutdown`).
func (s *Server) Serve() int {
	for {
		msg, err := s.conn.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			s.conn.logf("transport read error: %v", err)
			return 1
		}
		if msg.Method == "exit" {
			if s.shutdownReq {
				return 0
			}
			return 1
		}
		s.handleMessage(msg)
	}
}

func (s *Server) handleMessage(msg *RequestMessage) {
	switch msg.Method {
	case "initialize":
		var params InitializeParams
		if len(msg.Params) > 0 {
			_ = json.Unmarshal(msg.Params, &params)
		}
		s.mu.Lock()
		if params.RootURI != "" {
			s.rootDir = URIToPath(params.RootURI)
		} else if params.RootPath != "" {
			s.rootDir = params.RootPath
		} else if len(params.WorkspaceFolders) > 0 {
			s.rootDir = URIToPath(params.WorkspaceFolders[0].URI)
		}
		s.mu.Unlock()

		res := InitializeResult{
			Capabilities: ServerCapabilities{
				TextDocumentSync: TextDocumentSyncOptions{
					OpenClose: true,
					Change:    1, // Full sync
					Save:      true,
				},
				CompletionProvider: &CompletionOptions{
					TriggerCharacters: []string{".", "?", "[", ":", "\"", "/"},
				},
				HoverProvider:          true,
				DefinitionProvider:     true,
				ImplementationProvider: true,
				DocumentSymbolProvider: true,
				SignatureHelpProvider: &SignatureHelpOptions{
					TriggerCharacters:   []string{"(", ","},
					RetriggerCharacters: []string{","},
				},
				CodeActionProvider:         true,
				CodeLensProvider:           &CodeLensOptions{ResolveProvider: false},
				ExecuteCommandProvider:     &ExecuteCommandOptions{Commands: []string{"lowlight.runTest", "lowlight.runAllTests", "lowlight.runFile", "lowlight.runMain", "lowlight.goToLocation", "lowlight.showLocations", "lowlight.showImplementationsQuickPick"}},
				DocumentFormattingProvider: true,
				InlayHintProvider:          true,
				RenameProvider:             RenameOptions{PrepareProvider: true},
			},
			ServerInfo: &ServerInfo{
				Name:    "lowlight-lsp",
				Version: driver.Version,
			},
		}
		if msg.ID != nil {
			_ = s.conn.SendResponse(*msg.ID, res, nil)
		}

	case "initialized":
		// Notification; nothing further required

	case "shutdown":
		s.mu.Lock()
		s.shutdownReq = true
		s.mu.Unlock()
		if msg.ID != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, nil)
		}

	case "textDocument/didOpen":
		var params DidOpenTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		path := URIToPath(params.TextDocument.URI)
		s.mu.Lock()
		s.docs[path] = params.TextDocument.Text
		s.docURIs[path] = params.TextDocument.URI
		s.mu.Unlock()
		s.analyzeAndPublish(path, params.TextDocument.URI, params.TextDocument.Text)

	case "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		if len(params.ContentChanges) == 0 {
			return
		}
		path := URIToPath(params.TextDocument.URI)
		s.mu.Lock()
		curText := s.docs[path]
		for _, ch := range params.ContentChanges {
			if ch.Range == nil {
				curText = ch.Text
			} else {
				curText = applyIncrementalChange(curText, *ch.Range, ch.Text)
			}
		}
		s.docs[path] = curText
		s.docURIs[path] = params.TextDocument.URI
		s.mu.Unlock()
		s.analyzeAndPublish(path, params.TextDocument.URI, curText)

	case "textDocument/didSave":
		var params DidSaveTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		path := URIToPath(params.TextDocument.URI)
		s.mu.Lock()
		if params.Text != nil {
			s.docs[path] = *params.Text
		}
		curText, ok := s.docs[path]
		s.mu.Unlock()
		if !ok {
			curText = s.readSourceForPath(path)
		}
		s.analyzeAndPublish(path, params.TextDocument.URI, curText)

	case "textDocument/didClose":
		var params DidCloseTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		path := URIToPath(params.TextDocument.URI)
		s.mu.Lock()
		delete(s.docs, path)
		delete(s.docURIs, path)
		delete(s.cachedProgs, path)
		delete(s.latestDiags, path)
		s.mu.Unlock()
		// Clear squiggles for closed document
		_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []Diagnostic{},
		})

	case "textDocument/completion":
		if msg.ID == nil {
			return
		}
		var params CompletionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		list := s.computeCompletions(path, src, params.Position)
		if list != nil && list.Items == nil {
			list.Items = []CompletionItem{}
		}
		_ = s.conn.SendResponse(*msg.ID, list, nil)

	case "textDocument/hover":
		if msg.ID == nil {
			return
		}
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		hov := s.computeHover(path, src, params.Position)
		_ = s.conn.SendResponse(*msg.ID, hov, nil)

	case "textDocument/definition":
		if msg.ID == nil {
			return
		}
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		loc := s.computeDefinition(path, src, params.Position)
		_ = s.conn.SendResponse(*msg.ID, loc, nil)

	case "textDocument/implementation":
		if msg.ID == nil {
			return
		}
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		locs := s.computeImplementation(path, src, params.Position)
		if locs == nil {
			locs = []Location{}
		}
		_ = s.conn.SendResponse(*msg.ID, locs, nil)

	case "textDocument/documentSymbol":
		if msg.ID == nil {
			return
		}
		var params DocumentSymbolParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		syms := s.computeDocumentSymbols(path, src)
		if syms == nil {
			syms = []DocumentSymbol{}
		}
		_ = s.conn.SendResponse(*msg.ID, syms, nil)

	case "textDocument/signatureHelp":
		if msg.ID == nil {
			return
		}
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		sh := s.computeSignatureHelp(path, src, params.Position)
		_ = s.conn.SendResponse(*msg.ID, sh, nil)

	case "textDocument/codeAction":
		if msg.ID == nil {
			return
		}
		var params CodeActionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		acts := s.computeCodeActions(path, src, params)
		if acts == nil {
			acts = []CodeAction{}
		}
		_ = s.conn.SendResponse(*msg.ID, acts, nil)

	case "textDocument/formatting":
		if msg.ID == nil {
			return
		}
		var params DocumentFormattingParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		edits := s.computeDocumentFormatting(src, params.Options)
		if edits == nil {
			edits = []TextEdit{}
		}
		_ = s.conn.SendResponse(*msg.ID, edits, nil)

	case "textDocument/inlayHint":
		if msg.ID == nil {
			return
		}
		var params InlayHintParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		hints := s.computeInlayHints(path, src, params.Range)
		if hints == nil {
			hints = []InlayHint{}
		}
		_ = s.conn.SendResponse(*msg.ID, hints, nil)

	case "textDocument/prepareRename":
		if msg.ID == nil {
			return
		}
		var params PrepareRenameParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		rng, err := s.computePrepareRename(path, src, params.Position)
		if err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
			return
		}
		_ = s.conn.SendResponse(*msg.ID, rng, nil)

	case "textDocument/rename":
		if msg.ID == nil {
			return
		}
		var params RenameParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		edit, err := s.computeRename(path, src, params.Position, params.NewName)
		if err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
			return
		}
		_ = s.conn.SendResponse(*msg.ID, edit, nil)

	case "textDocument/codeLens":
		if msg.ID == nil {
			return
		}
		var params CodeLensParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		lenses := s.computeCodeLenses(path, src)
		if lenses == nil {
			lenses = []CodeLens{}
		}
		_ = s.conn.SendResponse(*msg.ID, lenses, nil)

	case "workspace/executeCommand":
		if msg.ID == nil {
			return
		}
		var params ExecuteCommandParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		switch params.Command {
		case "lowlight.goToLocation":
			res, err := s.executeGoToLocation(params.Arguments)
			if err != nil {
				_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
				return
			}
			_ = s.conn.SendResponse(*msg.ID, res, nil)
		case "lowlight.showLocations":
			_ = s.conn.SendResponse(*msg.ID, map[string]any{"success": true}, nil)
		case "lowlight.showImplementationsQuickPick":
			if len(params.Arguments) > 1 {
				var items []QuickPickLocationItem
				if err := json.Unmarshal(params.Arguments[1], &items); err == nil && len(items) == 1 && items[0].URI != "" {
					_ = s.conn.SendRequest(time.Now().UnixNano(), "window/showDocument", ShowDocumentParams{
						URI:       items[0].URI,
						TakeFocus: true,
						Selection: &Range{
							Start: Position{Line: items[0].Line, Character: items[0].Character},
							End:   Position{Line: items[0].Line, Character: items[0].Character},
						},
					})
				}
			}
			_ = s.conn.SendResponse(*msg.ID, map[string]any{"success": true}, nil)
		case "lowlight.runFile", "lowlight.runMain":
			res, err := s.executeRunCommand(params.Command, params.Arguments)
			if err != nil {
				_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
				return
			}
			_ = s.conn.SendResponse(*msg.ID, res, nil)
		case "lowlight.runTest", "lowlight.runAllTests":
			res, err := s.executeTestCommand(params.Command, params.Arguments)
			if err != nil {
				_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
				return
			}
			_ = s.conn.SendResponse(*msg.ID, res, nil)
		default:
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{
				Code:    ErrCodeMethodNotFound,
				Message: fmt.Sprintf("unsupported command %q", params.Command),
			})
		}

	default:
		// Ignore unknown notifications (`$/cancelRequest`, `$/setTrace`, etc.); respond to unknown requests cleanly
		if msg.ID != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{
				Code:    ErrCodeMethodNotFound,
				Message: fmt.Sprintf("unsupported LSP method %q", msg.Method),
			})
		}
	}
}

func (s *Server) analyzeAndPublish(filePath string, clientURI string, src string) {
	overlays := s.snapshotOverlays()
	overlays[filePath] = src

	rep := diag.NewReporter()
	prog := sema.CheckForIDE(filePath, overlays, s.projectRootFor(filePath), rep)

	absFile, err := filepath.Abs(filePath)
	if err != nil {
		absFile = filePath
	}

	lspDiags := make([]Diagnostic, 0)
	for _, d := range rep.Diagnostics() {
		dFile := d.Span.Start.File
		if dFile != "" {
			if dAbs, err2 := filepath.Abs(dFile); err2 == nil {
				dFile = dAbs
			}
		}
		if dFile != "" && dFile != absFile {
			continue
		}
		sev := SeverityError
		if d.Severity == diag.Warning {
			sev = SeverityWarning
		}
		fullMsg := d.Message
		if d.Hint != "" {
			fullMsg = fmt.Sprintf("%s\nhint: %s", d.Message, d.Hint)
		}
		var rng Range
		if d.Span.Start.Line > 0 {
			rng = SpanToLSP(src, d.Span)
		}
		lspDiags = append(lspDiags, Diagnostic{
			Range:    rng,
			Severity: sev,
			Source:   "lowlight",
			Message:  fullMsg,
		})
	}

	s.mu.Lock()
	s.cachedProgs[absFile] = prog
	s.latestDiags[absFile] = lspDiags
	if clientURI == "" {
		clientURI = s.docURIs[absFile]
	}
	s.mu.Unlock()

	if clientURI == "" {
		clientURI = PathToURI(absFile)
	}
	_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         clientURI,
		Diagnostics: lspDiags,
	})
}

func (s *Server) getOrCheckProgram(filePath string, src string) *sema.CheckedProgram {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	s.mu.RLock()
	cached := s.cachedProgs[abs]
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	overlays := s.snapshotOverlays()
	overlays[abs] = src
	rep := diag.NewReporter()
	prog := sema.CheckForIDE(abs, overlays, s.projectRootFor(abs), rep)
	s.mu.Lock()
	s.cachedProgs[abs] = prog
	s.mu.Unlock()
	return prog
}

func (s *Server) latestDiagnosticsFor(filePath string) []Diagnostic {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestDiags[abs]
}

func (s *Server) snapshotOverlays() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]string, len(s.docs))
	for k, v := range s.docs {
		cp[k] = v
	}
	return cp
}

func (s *Server) readSourceForPath(filePath string) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	s.mu.RLock()
	if txt, ok := s.docs[abs]; ok {
		s.mu.RUnlock()
		return txt
	}
	s.mu.RUnlock()
	b, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) projectRootFor(filePath string) string {
	s.mu.RLock()
	root := s.rootDir
	s.mu.RUnlock()
	if root != "" {
		if _, err := os.Stat(filepath.Join(root, "std")); err == nil {
			return root
		}
	}
	// Let sema's `findProjectRoot` discover `LOWLIGHT_ROOT`, ancestor `std/`, or executable dir `std/`
	_ = filePath
	return ""
}

func applyIncrementalChange(src string, rng Range, newText string) string {
	start := LSPToOffset(src, rng.Start)
	end := LSPToOffset(src, rng.End)
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(src) {
		end = len(src)
	}
	return src[:start] + newText + src[end:]
}
