// Package lsp implements a zero-dependency Language Server Protocol (LSP 3.17)
// server for Lowlight, providing real-time syntax/type/ownership diagnostics,
// context-aware autocomplete, hover, go-to-definition, signature help, document
// symbols, and quick-fix code actions.
package lsp

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"lowlight/internal/diag"
)

// -----------------------------------------------------------------------------
// JSON-RPC 2.0 Envelope Types
// -----------------------------------------------------------------------------

type RequestMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type ResponseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type ErrorResponseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *ResponseError  `json:"error"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type NotificationMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
	ErrCodeRequestFailed  = -32803
)

// -----------------------------------------------------------------------------
// Core LSP Positions, Ranges & Locations
// -----------------------------------------------------------------------------

type Position struct {
	Line      int `json:"line"`      // 0-based line
	Character int `json:"character"` // 0-based UTF-16 code unit offset
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes,omitempty"`
}

// -----------------------------------------------------------------------------
// Initialization & Capabilities
// -----------------------------------------------------------------------------

type InitializeParams struct {
	ProcessID             int               `json:"processId"`
	RootURI               string            `json:"rootUri"`
	RootPath              string            `json:"rootPath"`
	WorkspaceFolders      []WorkspaceFolder `json:"workspaceFolders"`
	InitializationOptions json.RawMessage   `json:"initializationOptions,omitempty"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	TextDocumentSync           TextDocumentSyncOptions `json:"textDocumentSync"`
	CompletionProvider         *CompletionOptions      `json:"completionProvider,omitempty"`
	HoverProvider              bool                    `json:"hoverProvider,omitempty"`
	DefinitionProvider         bool                    `json:"definitionProvider,omitempty"`
	TypeDefinitionProvider     bool                    `json:"typeDefinitionProvider,omitempty"`
	DeclarationProvider        bool                    `json:"declarationProvider,omitempty"`
	ImplementationProvider     bool                    `json:"implementationProvider,omitempty"`
	ReferencesProvider         bool                    `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider     bool                    `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider    bool                    `json:"workspaceSymbolProvider,omitempty"`
	SignatureHelpProvider      *SignatureHelpOptions   `json:"signatureHelpProvider,omitempty"`
	CodeActionProvider         bool                    `json:"codeActionProvider,omitempty"`
	CodeLensProvider           *CodeLensOptions        `json:"codeLensProvider,omitempty"`
	ExecuteCommandProvider     *ExecuteCommandOptions  `json:"executeCommandProvider,omitempty"`
	DocumentFormattingProvider bool                    `json:"documentFormattingProvider,omitempty"`
	InlayHintProvider          bool                    `json:"inlayHintProvider,omitempty"`
	FoldingRangeProvider       bool                    `json:"foldingRangeProvider,omitempty"`
	SelectionRangeProvider     bool                    `json:"selectionRangeProvider,omitempty"`
	SemanticTokensProvider     *SemanticTokensOptions  `json:"semanticTokensProvider,omitempty"`
	RenameProvider             any                     `json:"renameProvider,omitempty"`
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
}

type CodeLensOptions struct {
	ResolveProvider bool `json:"resolveProvider,omitempty"`
}

type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

type TextDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // 2 = Incremental document sync
	Save      bool `json:"save"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// -----------------------------------------------------------------------------
// Document Synchronization & Diagnostics
// -----------------------------------------------------------------------------

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DiagnosticSeverity int

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
	Code     string             `json:"code,omitempty"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// -----------------------------------------------------------------------------
// Completion, Hover, Definition, Symbols, SignatureHelp & CodeAction
// -----------------------------------------------------------------------------

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *CompletionContext     `json:"context,omitempty"`
}

type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type CompletionItemKind int

const (
	KindText          CompletionItemKind = 1
	KindMethod        CompletionItemKind = 2
	KindFunction      CompletionItemKind = 3
	KindConstructor   CompletionItemKind = 4
	KindField         CompletionItemKind = 5
	KindVariable      CompletionItemKind = 6
	KindClass         CompletionItemKind = 7
	KindInterface     CompletionItemKind = 8
	KindModule        CompletionItemKind = 9
	KindProperty      CompletionItemKind = 10
	KindEnum          CompletionItemKind = 13
	KindKeyword       CompletionItemKind = 14
	KindSnippet       CompletionItemKind = 15
	KindFile          CompletionItemKind = 17
	KindEnumMember    CompletionItemKind = 20
	KindConstant      CompletionItemKind = 21
	KindStruct        CompletionItemKind = 22
	KindTypeParameter CompletionItemKind = 25
)

type InsertTextFormat int

const (
	InsertPlainText InsertTextFormat = 1
	InsertSnippet   InsertTextFormat = 2
)

type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" or "plaintext"
	Value string `json:"value"`
}

type CompletionItem struct {
	Label               string             `json:"label"`
	Kind                CompletionItemKind `json:"kind,omitempty"`
	Detail              string             `json:"detail,omitempty"`
	Documentation       *MarkupContent     `json:"documentation,omitempty"`
	SortText            string             `json:"sortText,omitempty"`
	FilterText          string             `json:"filterText,omitempty"`
	InsertText          string             `json:"insertText,omitempty"`
	InsertTextFormat    InsertTextFormat   `json:"insertTextFormat,omitempty"`
	TextEdit            *TextEdit          `json:"textEdit,omitempty"`
	AdditionalTextEdits []TextEdit         `json:"additionalTextEdits,omitempty"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation *MarkupContent         `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters,omitempty"`
}

type ParameterInformation struct {
	Label         string         `json:"label"`
	Documentation *MarkupContent `json:"documentation,omitempty"`
}

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter int    `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   int    `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

type SelectionRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}

type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}

type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

type DidChangeConfigurationParams struct {
	Settings json.RawMessage `json:"settings"`
}

type CodeActionKind = string

type SymbolKind int

const (
	SymFile        SymbolKind = 1
	SymModule      SymbolKind = 2
	SymStruct      SymbolKind = 23
	SymInterface   SymbolKind = 11
	SymEnum        SymbolKind = 10
	SymEnumMember  SymbolKind = 22
	SymFunction    SymbolKind = 12
	SymMethod      SymbolKind = 6
	SymField       SymbolKind = 8
	SymVariable    SymbolKind = 13
	SymConstant    SymbolKind = 14
	SymTypeParam   SymbolKind = 26
)

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"` // "quickfix", "refactor", "run"
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
}

type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type CodeLensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type CodeLens struct {
	Range   Range    `json:"range"`
	Command *Command `json:"command,omitempty"`
	Data    any      `json:"data,omitempty"`
}

type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

type MessageType int

const (
	MessageTypeError   MessageType = 1
	MessageTypeWarning MessageType = 2
	MessageTypeInfo    MessageType = 3
	MessageTypeLog     MessageType = 4
)

type ShowMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}

type ShowDocumentParams struct {
	URI       string `json:"uri"`
	External  bool   `json:"external,omitempty"`
	TakeFocus bool   `json:"takeFocus,omitempty"`
	Selection *Range `json:"selection,omitempty"`
}

type ShowDocumentResult struct {
	Success bool `json:"success"`
}

// -----------------------------------------------------------------------------
// Formatting, Inlay Hints & Rename
// -----------------------------------------------------------------------------

type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider,omitempty"`
}

type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

type FormattingOptions struct {
	TabSize                int  `json:"tabSize"`
	InsertSpaces           bool `json:"insertSpaces"`
	TrimTrailingWhitespace bool `json:"trimTrailingWhitespace,omitempty"`
	InsertFinalNewline     bool `json:"insertFinalNewline,omitempty"`
}

type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type InlayHintKind int

const (
	InlayHintType      InlayHintKind = 1
	InlayHintParameter InlayHintKind = 2
)

type InlayHint struct {
	Position     Position      `json:"position"`
	Label        string        `json:"label"`
	Kind         InlayHintKind `json:"kind,omitempty"`
	PaddingLeft  bool          `json:"paddingLeft,omitempty"`
	PaddingRight bool          `json:"paddingRight,omitempty"`
}

type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type PrepareRenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// -----------------------------------------------------------------------------
// URI <-> Filesystem Path & UTF-8 <-> UTF-16 Coordinate Conversion
// -----------------------------------------------------------------------------

// URIToPath converts a `file://` URI into a clean local filesystem path.
func URIToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		if abs, err := filepath.Abs(uri); err == nil {
			return abs
		}
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		raw := strings.TrimPrefix(uri, "file://")
		return filepath.Clean(raw)
	}
	p := u.Path
	if unescaped, err2 := url.PathUnescape(p); err2 == nil {
		p = unescaped
	}
	// Handle Windows `/c:/foo` -> `c:\foo`
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	p = filepath.FromSlash(p)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// PathToURI converts a local filesystem path into a canonical `file://` URI.
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{
		Scheme: "file",
		Path:   slashed,
	}
	return u.String()
}

// shouldSkipWorkspaceDir reports whether a directory should be excluded from
// LSP workspace walks (caches, build outputs, editor plugin sandboxes, hidden dirs).
func shouldSkipWorkspaceDir(name string) bool {
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}
	switch name {
	case "node_modules", "build", "target", "bin", "dist", "vendor", "scratch", "editors", "benchmarks":
		return true
	}
	return false
}

// lineBoundsAt returns the byte bounds [start, end) of 0-based `targetLine` in `src`
// (stripping any trailing `\r`), plus the number of lines encountered.
// If `targetLine` is beyond the last line, `start`/`end` describe the last line and `found` is false.
func lineBoundsAt(src string, targetLine int) (start, end, lastLine int, found bool) {
	curLine := 0
	pos := 0
	for {
		nl := strings.IndexByte(src[pos:], '\n')
		if nl < 0 {
			lineEnd := len(src)
			if lineEnd > pos && src[lineEnd-1] == '\r' {
				lineEnd--
			}
			return pos, lineEnd, curLine, curLine == targetLine
		}
		if curLine == targetLine {
			lineEnd := pos + nl
			if lineEnd > pos && src[lineEnd-1] == '\r' {
				lineEnd--
			}
			return pos, lineEnd, curLine, true
		}
		curLine++
		pos += nl + 1
	}
}

// PosToLSP converts a 1-based Lowlight `diag.Pos` (where `Col` is 1-based rune column, or byte/rune offset)
// into a 0-based LSP UTF-16 `(line, character)` Position against `src`.
func PosToLSP(src string, p diag.Pos) Position {
	if p.Line <= 0 {
		return Position{Line: 0, Character: 0}
	}
	targetLine := p.Line - 1
	// Fast zero-allocation path when `p.Offset` is populated and consistent with `p.Line`.
	if p.Offset >= 0 && p.Offset <= len(src) {
		if p.Offset == 0 && p.Line == 1 && p.Col <= 1 {
			return Position{Line: 0, Character: 0}
		}
		if p.Offset > 0 {
			lineStart := strings.LastIndexByte(src[:p.Offset], '\n') + 1
			lineMatches := ((p.Line == 1 && lineStart == 0) || (p.Line > 1 && lineStart > 0)) &&
				(p.Col > 1 || p.Offset == lineStart)
			if lineMatches {
				seg := src[lineStart:p.Offset]
				if len(seg) > 0 && seg[len(seg)-1] == '\r' && (p.Offset == len(src) || src[p.Offset] == '\n') {
					seg = seg[:len(seg)-1]
				}
				return Position{Line: targetLine, Character: utf16Len(seg)}
			}
		}
	}

	// Fallback path for synthetic `diag.Pos` without byte `Offset`.
	start, end, lastLine, found := lineBoundsAt(src, targetLine)
	if !found {
		return Position{Line: lastLine, Character: utf16Len(src[start:end])}
	}
	lineText := src[start:end]
	// In Lowlight's lexer, `Col` increments by 1 per UTF-8 rune (`l.advance()`).
	targetRuneCol := p.Col - 1
	if targetRuneCol <= 0 {
		return Position{Line: targetLine, Character: 0}
	}
	utf16Units := 0
	runeIdx := 0
	for _, r := range lineText {
		if runeIdx >= targetRuneCol {
			break
		}
		if r > 0xFFFF {
			utf16Units += 2
		} else {
			utf16Units++
		}
		runeIdx++
	}
	return Position{Line: targetLine, Character: utf16Units}
}

// SpanToLSP converts a Lowlight `diag.Span` into a non-empty LSP `Range` (highlighting at least 1 character for point errors).
func SpanToLSP(src string, sp diag.Span) Range {
	start := PosToLSP(src, sp.Start)
	end := PosToLSP(src, sp.End)
	if end.Line < start.Line || (end.Line == start.Line && end.Character <= start.Character) {
		var lineStart, lineEnd int
		var haveLine bool
		if sp.Start.Line == start.Line+1 && sp.Start.Offset >= 0 && sp.Start.Offset <= len(src) {
			ls := strings.LastIndexByte(src[:sp.Start.Offset], '\n') + 1
			if (sp.Start.Line == 1 && ls == 0) || (sp.Start.Line > 1 && ls > 0) {
				lineStart = ls
				nl := strings.IndexByte(src[lineStart:], '\n')
				if nl < 0 {
					lineEnd = len(src)
				} else {
					lineEnd = lineStart + nl
				}
				if lineEnd > lineStart && src[lineEnd-1] == '\r' {
					lineEnd--
				}
				haveLine = true
			}
		}
		if !haveLine {
			s, e, _, found := lineBoundsAt(src, start.Line)
			if found {
				lineStart, lineEnd = s, e
				haveLine = true
			}
		}
		if haveLine && start.Character < utf16Len(src[lineStart:lineEnd]) {
			end = Position{Line: start.Line, Character: start.Character + 1}
		} else if start.Character > 0 {
			start = Position{Line: start.Line, Character: start.Character - 1}
			end = Position{Line: start.Line, Character: start.Character + 1}
		} else {
			end = start
		}
	}
	return Range{Start: start, End: end}
}

// ExactSpanToLSP converts a `diag.Span` into an exact LSP `Range` without expanding zero-width spans.
func ExactSpanToLSP(src string, sp diag.Span) Range {
	return Range{
		Start: PosToLSP(src, sp.Start),
		End:   PosToLSP(src, sp.End),
	}
}

// LSPToOffset converts a 0-based LSP UTF-16 `Position` into a 0-based UTF-8 byte offset in `src`.
func LSPToOffset(src string, pos Position) int {
	if pos.Line < 0 {
		return 0
	}
	curLine := 0
	i := 0
	for i < len(src) && curLine < pos.Line {
		if src[i] == '\n' {
			curLine++
		}
		i++
	}
	if curLine < pos.Line {
		return len(src)
	}
	utf16Col := 0
	for i < len(src) && src[i] != '\n' && utf16Col < pos.Character {
		if src[i] == '\r' {
			break
		}
		r, sz := utf8.DecodeRuneInString(src[i:])
		if r > 0xFFFF {
			utf16Col += 2
		} else {
			utf16Col++
		}
		i += sz
	}
	return i
}

// OffsetToLSP converts a 0-based UTF-8 byte offset in `src` into a 0-based LSP `Position`.
func OffsetToLSP(src string, offset int) Position {
	if offset <= 0 {
		return Position{Line: 0, Character: 0}
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 0
	utf16Col := 0
	i := 0
	for i < offset {
		if src[i] == '\n' {
			line++
			utf16Col = 0
			i++
			continue
		}
		if src[i] == '\r' {
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(src[i:])
		if r > 0xFFFF {
			utf16Col += 2
		} else {
			utf16Col++
		}
		i += sz
	}
	return Position{Line: line, Character: utf16Col}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
