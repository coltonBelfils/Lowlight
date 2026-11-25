package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lowlight/internal/ast"
	"lowlight/internal/diag"
	"lowlight/internal/docgen"
	"lowlight/internal/driver"
	"lowlight/internal/lexer"
	"lowlight/internal/manifest"
	"lowlight/internal/sema"
	"lowlight/internal/token"
)

// FormatConfig holds editor-supplied formatting preferences from LSP configuration.
type FormatConfig struct {
	Indent       string `json:"indent"`
	TabWidth     int    `json:"tabWidth"`
	LineWidth    int    `json:"lineWidth"`
	LineWidthSet bool   `json:"-"`
}

// InlayHintConfig controls optional LSP inlay hint categories.
type InlayHintConfig struct {
	Types      bool `json:"types"`
	Parameters bool `json:"parameters"`
}

// ServerConfig stores dynamic workspace/didChangeConfiguration settings.
type ServerConfig struct {
	Format     FormatConfig
	InlayHints InlayHintConfig
}

func defaultServerConfig() ServerConfig {
	return ServerConfig{
		InlayHints: InlayHintConfig{
			Types:      true,
			Parameters: true,
		},
	}
}

type cachedInlayEntry struct {
	cfg   InlayHintConfig
	hints []InlayHint
}

type cachedTokensEntry struct {
	src    string
	tokens []token.Token
}

// Server is a stateful Lowlight Language Server Protocol (LSP 3.17) instance.
type Server struct {
	conn *Conn

	mu              sync.RWMutex
	rootDir         string
	config          ServerConfig
	docs            map[string]string                            // canonical abs path -> open buffer content
	docURIs         map[string]string                            // canonical abs path -> exact client URI
	cachedProgs     map[string]*sema.CheckedProgram              // canonical abs path -> latest CheckedProgram
	docIndices      map[*sema.CheckedProgram]*docgen.SymbolIndex // CheckedProgram -> doc link index
	inlayCache      map[*sema.CheckedProgram]cachedInlayEntry    // CheckedProgram -> cached full-file inlay hints
	semTokCache     map[*sema.CheckedProgram]*SemanticTokens     // CheckedProgram -> cached semantic tokens
	tokenCache      map[string]cachedTokensEntry                 // canonical abs path -> cached lexed tokens
	latestDiags     map[string][]Diagnostic                      // canonical abs path -> latest published LSP diagnostics
	publishedImport map[string]map[string]bool                   // entry abs path -> imported abs paths last published for it
	dynamicWatchSupported bool
	registeredEmbedPaths  []string
	embedWatchRegID       string
	nextServerReqID       int64
	shutdownReq     bool
}

// NewServer creates a new Lowlight LSP server bound to `r` and `w` (typically stdin/stdout).
func NewServer(r io.Reader, w io.Writer, logW io.Writer) *Server {
	return &Server{
		conn:            NewConn(r, w, logW),
		config:          defaultServerConfig(),
		docs:            make(map[string]string),
		docURIs:         make(map[string]string),
		cachedProgs:     make(map[string]*sema.CheckedProgram),
		docIndices:      make(map[*sema.CheckedProgram]*docgen.SymbolIndex),
		inlayCache:      make(map[*sema.CheckedProgram]cachedInlayEntry),
		semTokCache:     make(map[*sema.CheckedProgram]*SemanticTokens),
		tokenCache:      make(map[string]cachedTokensEntry),
		latestDiags:     make(map[string][]Diagnostic),
		publishedImport: make(map[string]map[string]bool),
		nextServerReqID: 1,
	}
}

func isLowlightSourcePath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ll")
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
	if msg.Method == "" {
		// Response to a server-initiated request (`client/registerCapability`, `window/showDocument`, etc.).
		return
	}
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
		if len(params.InitializationOptions) > 0 {
			s.applyConfigLocked(params.InitializationOptions)
		}
		s.dynamicWatchSupported = params.Capabilities.Workspace != nil &&
			params.Capabilities.Workspace.DidChangeWatchedFiles != nil &&
			params.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration
		s.mu.Unlock()

		res := InitializeResult{
			Capabilities: ServerCapabilities{
				TextDocumentSync: TextDocumentSyncOptions{
					OpenClose: true,
					Change:    2, // Incremental sync
					Save:      true,
				},
				CompletionProvider: &CompletionOptions{
					TriggerCharacters: []string{".", "?", "[", ":", "\"", "/"},
				},
				HoverProvider:              true,
				DefinitionProvider:         true,
				TypeDefinitionProvider:     true,
				DeclarationProvider:        true,
				ImplementationProvider:     true,
				ReferencesProvider:         true,
				DocumentSymbolProvider:     true,
				WorkspaceSymbolProvider:    true,
				SignatureHelpProvider: &SignatureHelpOptions{
					TriggerCharacters:   []string{"(", ","},
					RetriggerCharacters: []string{","},
				},
				CodeActionProvider:         true,
				CodeLensProvider:           &CodeLensOptions{ResolveProvider: false},
				ExecuteCommandProvider:     &ExecuteCommandOptions{Commands: []string{"lowlight.runTest", "lowlight.runAllTests", "lowlight.runFile", "lowlight.runMain", "lowlight.goToLocation", "lowlight.showLocations", "lowlight.showImplementationsQuickPick"}},
				DocumentFormattingProvider: true,
				InlayHintProvider:          true,
				FoldingRangeProvider:       true,
				SelectionRangeProvider:     true,
				SemanticTokensProvider: &SemanticTokensOptions{
					Legend: semanticTokensLegend,
					Full:   true,
				},
				RenameProvider: RenameOptions{PrepareProvider: true},
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
		s.registerBaseFileWatchers()

	case "workspace/didChangeConfiguration":
		var params DidChangeConfigurationParams
		if err := json.Unmarshal(msg.Params, &params); err == nil && len(params.Settings) > 0 {
			s.mu.Lock()
			s.applyConfigLocked(params.Settings)
			s.mu.Unlock()
		}

	case "workspace/didChangeWatchedFiles":
		var payload struct {
			Changes []struct {
				URI string `json:"uri"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(msg.Params, &payload); err == nil {
			var openDeps []string
			seen := make(map[string]bool)
			s.mu.Lock()
			for _, ch := range payload.Changes {
				p := URIToPath(ch.URI)
				for _, dep := range s.invalidateDependentsLocked(p) {
					if !seen[dep] {
						seen[dep] = true
						openDeps = append(openDeps, dep)
					}
				}
			}
			s.mu.Unlock()
			s.reanalyzeOpenPaths(openDeps, "")
		}

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
		deps := s.invalidateDependentsLocked(path)
		s.mu.Unlock()
		s.reanalyzeOpenPaths(deps, path)
		if isLowlightSourcePath(path) {
			s.analyzeAndPublish(path, params.TextDocument.URI, params.TextDocument.Text)
		}

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
		deps := s.invalidateDependentsLocked(path)
		s.mu.Unlock()
		s.reanalyzeOpenPaths(deps, path)
		if isLowlightSourcePath(path) {
			s.analyzeAndPublish(path, params.TextDocument.URI, curText)
		}

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
		deps := s.invalidateDependentsLocked(path)
		s.mu.Unlock()
		if !ok {
			curText = s.readSourceForPath(path)
		}
		s.reanalyzeOpenPaths(deps, path)
		if isLowlightSourcePath(path) {
			s.analyzeAndPublish(path, params.TextDocument.URI, curText)
		}

	case "textDocument/didClose":
		var params DidCloseTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		path := URIToPath(params.TextDocument.URI)
		absClose := path
		if a, err := filepath.Abs(path); err == nil {
			absClose = a
		}
		s.mu.Lock()
		delete(s.docs, path)
		delete(s.docURIs, path)
		_, hadDiags := s.latestDiags[path]
		delete(s.latestDiags, path)
		delete(s.tokenCache, absClose)
		prevImports := s.publishedImport[path]
		delete(s.publishedImport, path)
		deps := s.invalidateDependentsLocked(path)
		s.mu.Unlock()
		// Reanalyze open dependents and clear unopened imports BEFORE clearing closed document
		s.reanalyzeOpenPaths(deps, path)
		s.syncEmbeddedFileWatchers()
		if isLowlightSourcePath(path) || hadDiags {
			for imp := range prevImports {
				if !s.isDocOpen(imp) {
					_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
						URI:         PathToURI(imp),
						Diagnostics: []Diagnostic{},
					})
				}
			}
			_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: []Diagnostic{},
			})
		}

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

	case "textDocument/typeDefinition":
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
		loc := s.computeTypeDefinition(path, src, params.Position)
		_ = s.conn.SendResponse(*msg.ID, loc, nil)

	case "textDocument/declaration":
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
		loc := s.computeDeclaration(path, src, params.Position)
		_ = s.conn.SendResponse(*msg.ID, loc, nil)

	case "textDocument/references":
		if msg.ID == nil {
			return
		}
		var params ReferenceParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		locs := s.computeReferences(path, src, params.Position, params.Context.IncludeDeclaration)
		if locs == nil {
			locs = []Location{}
		}
		_ = s.conn.SendResponse(*msg.ID, locs, nil)

	case "workspace/symbol":
		if msg.ID == nil {
			return
		}
		var params WorkspaceSymbolParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		syms := s.computeWorkspaceSymbols(params.Query)
		if syms == nil {
			syms = []SymbolInformation{}
		}
		_ = s.conn.SendResponse(*msg.ID, syms, nil)

	case "textDocument/foldingRange":
		if msg.ID == nil {
			return
		}
		var params FoldingRangeParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		ranges := s.computeFoldingRanges(path, src)
		if ranges == nil {
			ranges = []FoldingRange{}
		}
		_ = s.conn.SendResponse(*msg.ID, ranges, nil)

	case "textDocument/selectionRange":
		if msg.ID == nil {
			return
		}
		var params SelectionRangeParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		selRanges := s.computeSelectionRanges(path, src, params.Positions)
		if selRanges == nil {
			selRanges = []*SelectionRange{}
		}
		_ = s.conn.SendResponse(*msg.ID, selRanges, nil)

	case "textDocument/semanticTokens/full":
		if msg.ID == nil {
			return
		}
		var params SemanticTokensParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeInvalidParams, Message: err.Error()})
			return
		}
		path := URIToPath(params.TextDocument.URI)
		src := s.readSourceForPath(path)
		toks := s.computeSemanticTokens(path, src)
		if toks == nil {
			toks = &SemanticTokens{Data: []uint32{}}
		}
		if toks.Data == nil {
			toks.Data = []uint32{}
		}
		_ = s.conn.SendResponse(*msg.ID, toks, nil)

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
		edits := s.computeDocumentFormatting(path, src, params.Options)
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
			res, err := s.executeShowLocations(params.Arguments)
			if err != nil {
				_ = s.conn.SendResponse(*msg.ID, nil, &ResponseError{Code: ErrCodeRequestFailed, Message: err.Error()})
				return
			}
			_ = s.conn.SendResponse(*msg.ID, res, nil)
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
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		absFile = filePath
	}

	overlays := s.snapshotOverlays()
	overlays[absFile] = src

	rep := diag.NewReporter()
	prog := sema.CheckForIDE(absFile, overlays, s.projectRootFor(absFile), rep)

	diagsByFile := make(map[string][]Diagnostic)
	fileSrcCache := map[string]string{absFile: src}

	for _, d := range rep.Diagnostics() {
		dFile := d.Span.Start.File
		if dFile != "" {
			if dAbs, err2 := filepath.Abs(dFile); err2 == nil {
				dFile = dAbs
			}
		} else {
			dFile = absFile
		}

		dSrc, ok := fileSrcCache[dFile]
		if !ok {
			if ov, hasOv := overlays[dFile]; hasOv {
				dSrc = ov
			} else {
				dSrc = s.readSourceForPath(dFile)
			}
			fileSrcCache[dFile] = dSrc
		}

		sev := SeverityError
		switch d.Severity {
		case diag.Warning:
			sev = SeverityWarning
		case diag.Note:
			sev = SeverityInformation
		}
		fullMsg := d.Message
		if d.Hint != "" {
			fullMsg = fmt.Sprintf("%s\nhint: %s", d.Message, d.Hint)
		}
		var rng Range
		if d.Span.Start.Line > 0 {
			rng = SpanToLSP(dSrc, d.Span)
		}
		var data map[string]any
		if len(d.Data) > 0 || d.Full.Start.Line > 0 {
			data = map[string]any{}
			for k, v := range d.Data {
				data[k] = v
			}
			if d.Full.Start.Line > 0 && (d.Full.Start.File == "" || d.Full.Start.File == d.Span.Start.File) {
				data["fullRange"] = SpanToLSP(dSrc, d.Full)
			}
		}
		var related []DiagnosticRelatedInformation
		for _, rel := range d.Related {
			rFile := rel.Span.Start.File
			if rFile == "" || rel.Span.Start.Line <= 0 {
				continue
			}
			if rAbs, err2 := filepath.Abs(rFile); err2 == nil {
				rFile = rAbs
			}
			rSrc, ok := fileSrcCache[rFile]
			if !ok {
				if ov, hasOv := overlays[rFile]; hasOv {
					rSrc = ov
				} else {
					rSrc = s.readSourceForPath(rFile)
				}
				fileSrcCache[rFile] = rSrc
			}
			related = append(related, DiagnosticRelatedInformation{
				Location: Location{
					URI:   PathToURI(rFile),
					Range: SpanToLSP(rSrc, rel.Span),
				},
				Message: rel.Message,
			})
		}
		diagsByFile[dFile] = append(diagsByFile[dFile], Diagnostic{
			Range:              rng,
			Severity:           sev,
			Source:             "lowlight",
			Message:            fullMsg,
			RelatedInformation: related,
			Data:               data,
		})
	}

	entryDiags := diagsByFile[absFile]
	if entryDiags == nil {
		entryDiags = []Diagnostic{}
	}

	newImports := make(map[string]bool)
	for dFile := range diagsByFile {
		if dFile != absFile {
			newImports[dFile] = true
		}
	}

	var clearedImports []string
	var importPublishList []struct {
		uri   string
		diags []Diagnostic
	}

	s.mu.Lock()
	if old, ok := s.cachedProgs[absFile]; ok && old != prog {
		s.dropProgCachesLocked(old)
	}
	s.cachedProgs[absFile] = prog
	s.latestDiags[absFile] = entryDiags
	if clientURI == "" {
		clientURI = s.docURIs[absFile]
	}
	prevImports := s.publishedImport[absFile]
	s.publishedImport[absFile] = newImports

	for imp := range prevImports {
		if !newImports[imp] {
			_, isOpen := s.docs[imp]
			if !isOpen || len(s.latestDiags[imp]) == 0 {
				delete(s.latestDiags, imp)
				clearedImports = append(clearedImports, imp)
			}
		}
	}
	for imp := range newImports {
		impURI := s.docURIs[imp]
		if impURI == "" {
			impURI = PathToURI(imp)
		}
		impDiags := diagsByFile[imp]
		s.latestDiags[imp] = impDiags
		importPublishList = append(importPublishList, struct {
			uri   string
			diags []Diagnostic
		}{uri: impURI, diags: impDiags})
	}
	s.mu.Unlock()

	if clientURI == "" {
		clientURI = PathToURI(absFile)
	}
	for _, pub := range importPublishList {
		_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
			URI:         pub.uri,
			Diagnostics: pub.diags,
		})
	}
	for _, cleared := range clearedImports {
		_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
			URI:         PathToURI(cleared),
			Diagnostics: []Diagnostic{},
		})
	}
	s.syncEmbeddedFileWatchers()
	_ = s.conn.SendNotification("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         clientURI,
		Diagnostics: entryDiags,
	})
}

func (s *Server) dropProgCachesLocked(prog *sema.CheckedProgram) {
	if prog == nil {
		return
	}
	delete(s.docIndices, prog)
	delete(s.inlayCache, prog)
	delete(s.semTokCache, prog)
}

func canonicalWatchPath(p string) string {
	clean := filepath.Clean(p)
	cur := clean
	var suffix []string
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			parts := make([]string, 0, 1+len(suffix))
			parts = append(parts, filepath.Clean(real))
			for i := len(suffix) - 1; i >= 0; i-- {
				parts = append(parts, suffix[i])
			}
			return filepath.Clean(filepath.Join(parts...))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	return canonicalWatchPath(ca) == canonicalWatchPath(cb)
}

func (s *Server) registerBaseFileWatchers() {
	s.mu.Lock()
	if !s.dynamicWatchSupported {
		s.mu.Unlock()
		return
	}
	reqID := s.nextServerReqID
	s.nextServerReqID++
	s.mu.Unlock()

	_ = s.conn.SendRequest(reqID, "client/registerCapability", RegistrationParams{
		Registrations: []Registration{{
			ID:     "lowlight-base-watchers",
			Method: "workspace/didChangeWatchedFiles",
			RegisterOptions: DidChangeWatchedFilesRegistrationOptions{
				Watchers: []FileSystemWatcher{
					{GlobPattern: "**/*.ll"},
					{GlobPattern: "**/*.lowlightfmt"},
					{GlobPattern: "**/lowlight.toml"},
					{GlobPattern: "**/lowlight.lock"},
				},
			},
		}},
	})
}

func (s *Server) syncEmbeddedFileWatchers() {
	s.mu.Lock()
	if !s.dynamicWatchSupported {
		s.mu.Unlock()
		return
	}
	pathSet := make(map[string]bool)
	for _, p := range s.cachedProgs {
		if p == nil {
			continue
		}
		for _, ef := range p.EmbeddedFiles {
			if ef.Path != "" {
				pathSet[filepath.Clean(ef.Path)] = true
			}
		}
		for _, ep := range p.AttemptedEmbedPaths {
			if ep != "" {
				pathSet[filepath.Clean(ep)] = true
			}
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	same := len(paths) == len(s.registeredEmbedPaths)
	if same {
		for i := range paths {
			if paths[i] != s.registeredEmbedPaths[i] {
				same = false
				break
			}
		}
	}
	if same {
		s.mu.Unlock()
		return
	}

	oldRegID := s.embedWatchRegID
	var oldUnregReqID int64
	if oldRegID != "" {
		oldUnregReqID = s.nextServerReqID
		s.nextServerReqID++
	}
	newRegID := ""
	var newRegReqID int64
	var watchers []FileSystemWatcher
	if len(paths) > 0 {
		newRegReqID = s.nextServerReqID
		s.nextServerReqID++
		newRegID = fmt.Sprintf("lowlight-embed-watchers-%d", newRegReqID)
		watchers = make([]FileSystemWatcher, len(paths))
		for i, p := range paths {
			watchers[i] = FileSystemWatcher{GlobPattern: filepath.ToSlash(p)}
		}
	}
	s.registeredEmbedPaths = paths
	s.embedWatchRegID = newRegID
	s.mu.Unlock()

	if oldRegID != "" {
		_ = s.conn.SendRequest(oldUnregReqID, "client/unregisterCapability", UnregistrationParams{
			Unregisterations: []Unregistration{{
				ID:     oldRegID,
				Method: "workspace/didChangeWatchedFiles",
			}},
		})
	}
	if newRegID != "" {
		_ = s.conn.SendRequest(newRegReqID, "client/registerCapability", RegistrationParams{
			Registrations: []Registration{{
				ID:     newRegID,
				Method: "workspace/didChangeWatchedFiles",
				RegisterOptions: DidChangeWatchedFilesRegistrationOptions{
					Watchers: watchers,
				},
			}},
		})
	}
}

func (s *Server) invalidateDependentsLocked(changedPath string) []string {
	absChanged, err := filepath.Abs(changedPath)
	if err != nil {
		absChanged = changedPath
	}
	if old, ok := s.cachedProgs[absChanged]; ok && old != nil {
		s.dropProgCachesLocked(old)
	}
	delete(s.cachedProgs, absChanged)
	delete(s.tokenCache, absChanged)

	changedDir := filepath.Clean(filepath.Dir(absChanged))
	changedDirCanon := canonicalWatchPath(changedDir)
	changedDirSlash := filepath.ToSlash(changedDir)
	changedBase := filepath.Base(absChanged)
	isManifestChange := changedBase == "lowlight.toml" || changedBase == "lowlight.lock"

	var openDependents []string
	for k, p := range s.cachedProgs {
		if k == absChanged {
			continue
		}
		depends := false
		if filepath.Clean(filepath.Dir(k)) == changedDir || canonicalWatchPath(filepath.Dir(k)) == changedDirCanon {
			depends = true
		}
		if !depends && isManifestChange {
			kDirCanon := canonicalWatchPath(filepath.Dir(k))
			if kDirCanon == changedDirCanon || strings.HasPrefix(kDirCanon, changedDirCanon+string(filepath.Separator)) {
				depends = true
			}
		}
		if !depends && p != nil {
			for _, m := range p.Modules {
				if m == nil {
					continue
				}
				mAbs, err := filepath.Abs(m.Path)
				if err != nil {
					mAbs = m.Path
				}
				if sameFilePath(mAbs, absChanged) || canonicalWatchPath(filepath.Dir(mAbs)) == changedDirCanon {
					depends = true
					break
				}
			}
			if !depends {
				for _, ef := range p.EmbeddedFiles {
					efAbs, err := filepath.Abs(ef.Path)
					if err != nil {
						efAbs = ef.Path
					}
					if sameFilePath(efAbs, absChanged) {
						depends = true
						break
					}
				}
			}
			if !depends {
				for _, ep := range p.AttemptedEmbedPaths {
					epAbs, err := filepath.Abs(ep)
					if err != nil {
						epAbs = ep
					}
					if sameFilePath(epAbs, absChanged) {
						depends = true
						break
					}
				}
			}
			// Also catch unresolved imports matching the changed package directory.
			if !depends && p.EntryMod != nil && p.EntryMod.File != nil {
				for _, decl := range p.EntryMod.File.Decls {
					imp, ok := decl.(*ast.ImportDecl)
					if !ok || imp.Path == "" {
						continue
					}
					impClean := strings.TrimPrefix(filepath.ToSlash(strings.TrimSuffix(imp.Path, ".ll")), "./")
					if impClean != "" && (filepath.Base(changedDirSlash) == filepath.Base(impClean) || strings.HasSuffix(changedDirSlash, "/"+impClean)) {
						depends = true
						break
					}
				}
			}
		}
		if depends {
			s.dropProgCachesLocked(p)
			delete(s.cachedProgs, k)
			if _, isOpen := s.docs[k]; isOpen {
				openDependents = append(openDependents, k)
			}
		}
	}
	return openDependents
}

func (s *Server) reanalyzeOpenPaths(paths []string, skipPath string) {
	absSkip := skipPath
	if skipPath != "" {
		if a, err := filepath.Abs(skipPath); err == nil {
			absSkip = a
		}
	}
	for _, p := range paths {
		if !isLowlightSourcePath(p) {
			continue
		}
		if absSkip != "" && sameFilePath(p, absSkip) {
			continue
		}
		s.mu.RLock()
		src, ok := s.docs[p]
		uri := s.docURIs[p]
		s.mu.RUnlock()
		if !ok {
			continue
		}
		s.analyzeAndPublish(p, uri, src)
	}
}

func (s *Server) isDocOpen(absPath string) bool {
	s.mu.RLock()
	_, ok := s.docs[absPath]
	s.mu.RUnlock()
	return ok
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
	if old, ok := s.cachedProgs[abs]; ok && old != prog {
		s.dropProgCachesLocked(old)
	}
	s.cachedProgs[abs] = prog
	s.mu.Unlock()
	return prog
}

func (s *Server) getLexedTokens(filePath string, src string) []token.Token {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	s.mu.RLock()
	entry, ok := s.tokenCache[abs]
	s.mu.RUnlock()
	if ok && entry.src == src {
		return entry.tokens
	}
	toks := lexer.Lex(abs, src, diag.NewReporter())
	s.mu.Lock()
	if len(s.tokenCache) > 64 {
		s.tokenCache = make(map[string]cachedTokensEntry)
	}
	s.tokenCache[abs] = cachedTokensEntry{src: src, tokens: toks}
	s.mu.Unlock()
	return toks
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

	if filePath != "" {
		absFile, err := filepath.Abs(filePath)
		if err != nil {
			absFile = filePath
		}
		if ctx, err := manifest.DiscoverProject(absFile); err == nil && ctx != nil && ctx.RootDir() != "" {
			return ctx.RootDir()
		}
		// Walk up from file directory looking for project markers (lowlight.toml, .lowlightfmt, std/).
		dir := filepath.Dir(absFile)
		for dir != "" {
			for _, marker := range []string{"lowlight.toml", ".lowlightfmt", "std"} {
				if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
					return dir
				}
			}
			if root != "" && filepath.Clean(dir) == filepath.Clean(root) {
				return root
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if root != "" {
		return root
	}
	if filePath != "" {
		if absFile, err := filepath.Abs(filePath); err == nil {
			return filepath.Dir(absFile)
		}
		return filepath.Dir(filePath)
	}
	return ""
}

func (s *Server) applyConfigLocked(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return
	}
	// Accept either `{"lowlight": {...}}` or flat `{...}`.
	if sub, ok := top["lowlight"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(sub, &nested); err == nil {
			top = nested
		}
	}
	if fmtRaw, ok := top["format"]; ok {
		var fmtMap map[string]json.RawMessage
		if err := json.Unmarshal(fmtRaw, &fmtMap); err == nil {
			if v, ok := fmtMap["indent"]; ok {
				var ind string
				if json.Unmarshal(v, &ind) == nil && (ind == "tabs" || ind == "spaces") {
					s.config.Format.Indent = ind
				}
			}
			if v, ok := fmtMap["tabWidth"]; ok {
				var tw int
				if json.Unmarshal(v, &tw) == nil && tw > 0 {
					s.config.Format.TabWidth = tw
				}
			}
			if v, ok := fmtMap["lineWidth"]; ok {
				var lw int
				if json.Unmarshal(v, &lw) == nil && lw >= 0 {
					s.config.Format.LineWidth = lw
					s.config.Format.LineWidthSet = true
				}
			}
		}
	}
	if ihRaw, ok := top["inlayHints"]; ok {
		var ihMap map[string]json.RawMessage
		if err := json.Unmarshal(ihRaw, &ihMap); err == nil {
			if v, ok := ihMap["types"]; ok {
				var b bool
				if json.Unmarshal(v, &b) == nil {
					s.config.InlayHints.Types = b
				}
			}
			if v, ok := ihMap["parameters"]; ok {
				var b bool
				if json.Unmarshal(v, &b) == nil {
					s.config.InlayHints.Parameters = b
				}
			}
		}
	}
}

func (s *Server) getFormatConfig() FormatConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Format
}

func (s *Server) getInlayHintConfig() InlayHintConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.InlayHints
}

func (s *Server) executeShowLocations(rawArgs []json.RawMessage) (any, error) {
	var locs []Location
	switch {
	case len(rawArgs) >= 3:
		_ = json.Unmarshal(rawArgs[2], &locs)
	case len(rawArgs) == 1:
		_ = json.Unmarshal(rawArgs[0], &locs)
	}
	if len(locs) > 0 && locs[0].URI != "" {
		_ = s.conn.SendRequest(time.Now().UnixNano(), "window/showDocument", ShowDocumentParams{
			URI:       locs[0].URI,
			TakeFocus: true,
			Selection: &locs[0].Range,
		})
	}
	if len(locs) > 1 {
		var summary []string
		for _, loc := range locs {
			summary = append(summary, fmt.Sprintf("%s:%d:%d", filepath.Base(URIToPath(loc.URI)), loc.Range.Start.Line+1, loc.Range.Start.Character+1))
		}
		_ = s.conn.SendNotification("window/showMessage", ShowMessageParams{
			Type:    MessageTypeInfo,
			Message: "Locations: " + strings.Join(summary, ", "),
		})
	}
	return map[string]any{
		"success":   true,
		"count":     len(locs),
		"locations": locs,
	}, nil
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
