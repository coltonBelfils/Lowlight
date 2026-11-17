package dev.lowlight.intellij.syntax

import com.intellij.lexer.LexerBase
import com.intellij.psi.TokenType
import com.intellij.psi.tree.IElementType

/**
 * Lightweight, allocation-conscious Lexer for instant syntax highlighting, brace matching,
 * comment toggling, and quote handling in JetBrains editors prior to LSP SemanticTokens.
 */
class LowlightLexer : LexerBase() {
    private var buffer: CharSequence = ""
    private var endOffset: Int = 0
    private var tokenStart: Int = 0
    private var tokenEnd: Int = 0
    private var tokenType: IElementType? = null

    companion object {
        private val KEYWORDS = setOf(
            "let", "bind", "define", "distinct", "struct", "enum", "interface",
            "if", "else", "for", "while", "in", "next", "match", "when", "func",
            "none", "true", "false", "self", "Self", "own",
            "and", "or", "not", "as", "unsafe", "extern", "unpack", "import"
        )

        private val PRIMITIVES = setOf(
            "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64",
            "float", "f32", "f64", "bool", "char", "string", "void", "never",
            "Arena", "ptr", "ref"
        )

        private val INTERFACES = setOf(
            "Printable", "Copyable", "Drop", "Equatable", "Comparable",
            "Hashable", "Indexable", "Collection", "Iterable"
        )

        private val BUILTINS = setOf(
            "print", "println", "eprint", "eprintln", "panic", "assert", "exit",
            "parse_int", "parse_float", "sqrt", "abs", "min", "max",
            "read_file", "write_file", "args",
            "caller_location", "caller_file", "caller_line", "caller_col",
            "size_of", "align_of", "alloc_raw", "realloc_raw", "free_raw",
            "null_ptr", "is_null", "addr_of", "ptr_add", "ptr_read",
            "ptr_write", "ptr_cast", "ptr_addr", "copy_bytes", "zero_bytes"
        )
    }

    override fun start(buffer: CharSequence, startOffset: Int, endOffset: Int, initialState: Int) {
        this.buffer = buffer
        this.endOffset = endOffset
        this.tokenStart = startOffset
        this.tokenEnd = startOffset
        this.tokenType = null
        advance()
    }

    override fun getState(): Int = 0
    override fun getTokenType(): IElementType? = tokenType
    override fun getTokenStart(): Int = tokenStart
    override fun getTokenEnd(): Int = tokenEnd
    override fun getBufferSequence(): CharSequence = buffer
    override fun getBufferEnd(): Int = endOffset

    override fun advance() {
        if (tokenEnd >= endOffset) {
            tokenStart = endOffset
            tokenEnd = endOffset
            tokenType = null
            return
        }

        tokenStart = tokenEnd
        val pos = tokenStart
        val ch = buffer[pos]

        // Whitespace
        if (ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n') {
            var p = pos + 1
            while (p < endOffset && buffer[p].let { it == ' ' || it == '\t' || it == '\r' || it == '\n' }) {
                p++
            }
            tokenEnd = p
            tokenType = TokenType.WHITE_SPACE
            return
        }

        // Single-line comment
        if (ch == '/' && pos + 1 < endOffset && buffer[pos + 1] == '/') {
            var p = pos + 2
            while (p < endOffset && buffer[p] != '\n') p++
            tokenEnd = p
            tokenType = LowlightTokenTypes.LINE_COMMENT
            return
        }

        // Multi-line comment
        if (ch == '/' && pos + 1 < endOffset && buffer[pos + 1] == '*') {
            var p = pos + 2
            while (p < endOffset) {
                if (buffer[p] == '*' && p + 1 < endOffset && buffer[p + 1] == '/') {
                    p += 2
                    break
                }
                p++
            }
            tokenEnd = p
            tokenType = LowlightTokenTypes.BLOCK_COMMENT
            return
        }

        // Strings ("..." and `...`)
        if (ch == '"' || ch == '`') {
            tokenEnd = scanString(pos, ch)
            tokenType = LowlightTokenTypes.STRING
            return
        }

        // Char literal
        if (ch == '\'') {
            var p = pos + 1
            while (p < endOffset && buffer[p] != '\n') {
                if (buffer[p] == '\\' && p + 1 < endOffset) {
                    p += 2
                    continue
                }
                if (buffer[p] == '\'') {
                    p++
                    break
                }
                p++
            }
            tokenEnd = p
            tokenType = LowlightTokenTypes.CHAR
            return
        }

        // Attributes (@test, @must_use, ...)
        if (ch == '@') {
            var p = pos + 1
            while (p < endOffset && isIdentPart(buffer[p])) p++
            tokenEnd = p
            tokenType = if (p > pos + 1) LowlightTokenTypes.ATTRIBUTE else LowlightTokenTypes.OPERATOR
            return
        }

        // Numbers
        if (ch in '0'..'9') {
            tokenEnd = scanNumber(pos)
            tokenType = LowlightTokenTypes.NUMBER
            return
        }

        // Identifiers / Keywords / Builtins / Optional idents
        if (isIdentStart(ch)) {
            var p = pos + 1
            while (p < endOffset && isIdentPart(buffer[p])) p++
            val word = buffer.subSequence(pos, p).toString()
            if (p < endOffset && buffer[p] == '?' && (p + 1 >= endOffset || (buffer[p + 1] != '?' && buffer[p + 1] != '.'))) {
                tokenEnd = p + 1
                tokenType = LowlightTokenTypes.OPTIONAL_IDENT
                return
            }
            tokenEnd = p
            tokenType = when {
                word in KEYWORDS -> LowlightTokenTypes.KEYWORD
                word in PRIMITIVES -> LowlightTokenTypes.PRIMITIVE_TYPE
                word in INTERFACES -> LowlightTokenTypes.INTERFACE_TYPE
                word in BUILTINS -> LowlightTokenTypes.BUILTIN_FUNC
                word[0].isUpperCase() -> LowlightTokenTypes.TYPE_IDENT
                else -> LowlightTokenTypes.IDENTIFIER
            }
            return
        }

        // Multi-char operators & delimiters
        if (pos + 1 < endOffset) {
            val next = buffer[pos + 1]
            when {
                ch == '<' && next == '-' -> {
                    tokenEnd = pos + 2; tokenType = LowlightTokenTypes.UNWIND_OP; return
                }
                ch == '+' && next == '[' -> {
                    tokenEnd = pos + 2; tokenType = LowlightTokenTypes.PLUS_BRACKET; return
                }
                ch == '#' && next == '[' -> {
                    tokenEnd = pos + 2; tokenType = LowlightTokenTypes.HASH_BRACKET; return
                }
                ch == '.' && next == '.' -> {
                    tokenEnd = if (pos + 2 < endOffset && buffer[pos + 2] == '=') pos + 3 else pos + 2
                    tokenType = LowlightTokenTypes.OPERATOR
                    return
                }
                (ch == '?' && (next == '?' || next == '.')) ||
                    (ch in charArrayOf('=', '!', '<', '>', '+', '-', '*', '/', '%', '&', '|', '^') && next == '=') ||
                    (ch == '<' && next == '<') ||
                    (ch == '>' && next == '>') -> {
                    val len = if ((ch == '<' && next == '<' || ch == '>' && next == '>') && pos + 2 < endOffset && buffer[pos + 2] == '=') 3 else 2
                    tokenEnd = pos + len
                    tokenType = LowlightTokenTypes.OPERATOR
                    return
                }
            }
        }

        when (ch) {
            '{' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.LBRACE }
            '}' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.RBRACE }
            '(' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.LPAREN }
            ')' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.RPAREN }
            '[' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.LBRACKET }
            ']' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.RBRACKET }
            ',' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.COMMA }
            ':' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.COLON }
            ';' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.SEMI }
            '.' -> { tokenEnd = pos + 1; tokenType = LowlightTokenTypes.DOT }
            '+', '-', '*', '/', '%', '=', '<', '>', '!', '&', '|', '^', '~', '?' -> {
                tokenEnd = pos + 1
                tokenType = LowlightTokenTypes.OPERATOR
            }
            else -> {
                tokenEnd = pos + 1
                tokenType = LowlightTokenTypes.BAD_CHARACTER
            }
        }
    }

    private fun scanString(start: Int, quote: Char): Int {
        var p = start + 1
        while (p < endOffset) {
            val c = buffer[p]
            if (c == '\\' && p + 1 < endOffset) {
                p += 2
                continue
            }
            if (c == quote) {
                return p + 1
            }
            if (quote == '"' && c == '\n') {
                return p
            }
            p++
        }
        return p
    }

    private fun scanNumber(start: Int): Int {
        var p = start
        if (p + 1 < endOffset && buffer[p] == '0' && (buffer[p + 1] == 'x' || buffer[p + 1] == 'X' || buffer[p + 1] == 'b' || buffer[p + 1] == 'B')) {
            p += 2
            while (p < endOffset && (buffer[p].isLetterOrDigit() || buffer[p] == '_')) p++
            return p
        }
        while (p < endOffset && (buffer[p] in '0'..'9' || buffer[p] == '_')) p++
        if (p + 1 < endOffset && buffer[p] == '.' && buffer[p + 1] in '0'..'9') {
            p++
            while (p < endOffset && (buffer[p] in '0'..'9' || buffer[p] == '_')) p++
        }
        if (p < endOffset && (buffer[p] == 'e' || buffer[p] == 'E')) {
            p++
            if (p < endOffset && (buffer[p] == '+' || buffer[p] == '-')) p++
            while (p < endOffset && (buffer[p] in '0'..'9' || buffer[p] == '_')) p++
        }
        return p
    }

    private fun isIdentStart(ch: Char): Boolean = ch == '_' || ch in 'a'..'z' || ch in 'A'..'Z'
    private fun isIdentPart(ch: Char): Boolean = isIdentStart(ch) || ch in '0'..'9'
}
