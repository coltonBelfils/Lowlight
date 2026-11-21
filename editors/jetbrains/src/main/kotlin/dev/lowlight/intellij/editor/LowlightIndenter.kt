package dev.lowlight.intellij.editor

import com.intellij.codeInsight.editorActions.enter.EnterHandlerDelegate
import com.intellij.codeInsight.editorActions.enter.EnterHandlerDelegateAdapter
import com.intellij.openapi.actionSystem.DataContext
import com.intellij.openapi.editor.Editor
import com.intellij.psi.PsiFile
import dev.lowlight.intellij.LowlightLanguage

/**
 * Computes the indentation `lowlight fmt` would give a line, from the lines above it. This is a
 * port of `LowlightIndent` in `editors/nvim/init.lua`; keep the two in sync. Indents are counted
 * in levels of [unit] (a tab or a run of spaces).
 *
 *  - a top-level declaration (or the doc comments / attributes above one) outside any bracket
 *    starts at column 0, and lines inside a multi-line string keep their indent;
 *  - `else` aligns with the `if` it belongs to;
 *  - after a header ending in `:` (`if(...):`, `bind f = () int:`, `else:`, `when X:`,
 *    `match(x):`, a result chain `load():`) or an opening `{` / `(` / `[`, indent one level;
 *  - after a completed statement, continue where it started, unwinding every header whose
 *    single-expression body it completed; `when` arms stay put, and the terminal `default` arm
 *    returns to its `match` / chain header;
 *  - a `.method()` continuation is one level deeper than its statement, and a line starting
 *    with `}` / `)` / `]` closes one level.
 */
class LowlightIndenter(private val lines: List<String>, private val unit: String) {
    private class LineInfo(val depth: Int, val inString: Boolean, val code: String)

    private val info: List<LineInfo> = lex()

    private fun lex(): List<LineInfo> {
        val out = ArrayList<LineInfo>(lines.size)
        val stack = ArrayList<Char>() // '"' / '`' string, 'I' interpolation, 'B' brace
        var depth = 0
        for (line in lines) {
            val startDepth = depth
            val top0 = stack.lastOrNull()
            val inString = top0 == '"' || top0 == '`'
            val sb = StringBuilder()
            var j = 0
            loop@ while (j < line.length) {
                val ch = line[j]
                val top = stack.lastOrNull()
                if (top == '"' || top == '`') {
                    when {
                        ch == '\\' -> j++
                        ch == top -> { stack.removeAt(stack.size - 1); sb.append(ch) }
                        ch == '$' && j + 1 < line.length && line[j + 1] == '{' -> { stack.add('I'); j++ }
                    }
                } else when {
                    ch == '/' && j + 1 < line.length && line[j + 1] == '/' -> break@loop
                    ch == '"' || ch == '`' -> { stack.add(ch); sb.append(ch).append(ch) }
                    ch == '\'' -> {
                        val from = j + if (j + 1 < line.length && line[j + 1] == '\\') 3 else 2
                        val close = if (from < line.length) line.indexOf('\'', from) else -1
                        sb.append("''")
                        j = if (close >= 0) close else line.length
                    }
                    ch == '{' -> { stack.add('B'); depth++; sb.append(ch) }
                    ch == '}' && top == 'I' -> stack.removeAt(stack.size - 1)
                    ch == '}' || ch == ')' || ch == ']' -> {
                        if (ch == '}' && top == 'B') stack.removeAt(stack.size - 1)
                        depth = maxOf(depth - 1, 0)
                        sb.append(ch)
                    }
                    ch == '(' || ch == '[' -> { depth++; sb.append(ch) }
                    else -> sb.append(ch)
                }
                j++
            }
            out.add(LineInfo(depth = startDepth, inString = inString, code = sb.toString().trimEnd()))
        }
        return out
    }

    /** Indent level of line [i]: leading tabs plus leading spaces in units of [unit]'s width. */
    private fun indentOf(i: Int): Int {
        val line = lines[i]
        var tabs = 0
        var spaces = 0
        for (ch in line) {
            when (ch) {
                '\t' -> tabs++
                ' ' -> spaces++
                else -> break
            }
        }
        val width = if (unit == "\t") 4 else maxOf(unit.length, 1)
        return tabs + spaces / width
    }

    private fun code(i: Int) = info[i].code
    private fun nonblank(i: Int) = lines[i].isNotBlank()
    private fun isComment(i: Int) = lines[i].trimStart().startsWith("//") && !info[i].inString
    private fun startsWithWord(s: String, w: String): Boolean {
        val t = s.trimStart()
        return t.startsWith(w) && (t.length == w.length || !(t[w.length].isLetterOrDigit() || t[w.length] == '_'))
    }

    private fun prevNonblank(i: Int): Int {
        var p = i - 1
        while (p >= 0 && !nonblank(p)) p--
        return p
    }

    private fun prevCodeLine(i: Int): Int {
        var p = prevNonblank(i)
        while (p >= 0 && isComment(p)) p = prevNonblank(p)
        return p
    }

    private fun headerOf(arm: Int): Int {
        val armInd = indentOf(arm)
        for (i in arm - 1 downTo 0) {
            if (nonblank(i) && indentOf(i) < armInd && !info[i].inString) return i
        }
        return arm
    }

    private val ifStart = Regex("""^\s*}?\s*if\s*\(""")
    private val ifInside = Regex("""[=:,(]\s*if\s*\(""")
    private val elseIf = Regex("""^\s*}?\s*else\s+if""")
    private val elseAny = Regex("""^\s*}?\s*else\b""")
    private val ifWithElse = Regex("""\)\s*:.*else\s*:""")

    private fun opensIf(c: String) =
        (ifStart.containsMatchIn(c) || ifInside.containsMatchIn(c) || elseIf.containsMatchIn(c)) && !ifWithElse.containsMatchIn(c)

    private fun matchingIf(from: Int, hint: Int?): Int? {
        var limit = indentOf(from)
        val closed = HashSet<Int>()
        var first: Int? = null
        for (i in from downTo 0) {
            if (!nonblank(i) || isComment(i) || info[i].inString) continue
            val ind = indentOf(i)
            if (ind > limit) continue
            val c = code(i)
            val isElse = elseAny.containsMatchIn(c)
            val isElseIf = elseIf.containsMatchIn(c)
            when {
                opensIf(c) && ind !in closed -> {
                    if (hint == null || ind == hint) return i
                    if (first == null) first = i
                }
                opensIf(c) && ind in closed && !isElseIf -> closed.remove(ind)
                isElse && !isElseIf -> closed.add(ind)
                !isElse && !opensIf(c) && ind < limit && !c.trimStart().startsWith("}") &&
                    !c.trimStart().startsWith(")") && !c.trimStart().startsWith("]") -> return first
            }
            limit = minOf(limit, ind)
        }
        return first
    }

    private fun statementStart(line: Int): Int {
        var cur = line
        repeat(200) {
            val c = code(cur).trimStart()
            val ind = indentOf(cur)
            var nxt: Int? = null
            if (info[cur].inString) {
                nxt = cur - 1
            } else if (c.startsWith("}") || c.startsWith(")") || c.startsWith("]")) {
                for (i in cur - 1 downTo 0) {
                    if (nonblank(i) && indentOf(i) <= ind && !info[i].inString && !isComment(i)) { nxt = i; break }
                }
            } else if (startsWithWord(c, "else")) {
                nxt = matchingIf(cur - 1, ind)
            } else if (c.startsWith(".")) {
                nxt = prevCodeLine(cur)
            }
            if (nxt == null || nxt < 0) return cur
            cur = nxt
        }
        return cur
    }

    /** Indent level for (0-based) line [lnum], or null to keep the line as written. */
    fun levelFor(lnum: Int): Int? {
        if (lnum <= 0 || lnum >= lines.size) return if (lnum == 0) 0 else null
        if (info[lnum].inString) return null
        val cur = lines[lnum]
        var decl = lnum
        while (decl < lines.size && lines[decl].trimStart().startsWith("//")) decl++
        val dline = if (decl < lines.size) lines[decl] else ""
        if (info[lnum].depth == 0 && (startsWithWord(dline, "bind") || startsWithWord(dline, "define") ||
                startsWithWord(dline, "import") || startsWithWord(dline, "extern") || dline.trimStart().startsWith("@"))) {
            return 0
        }
        val above = prevNonblank(lnum)
        if (cur.trimStart().startsWith("//")) {
            if (above >= 0 && isComment(above)) return indentOf(above)
            if (info[lnum].depth == 0 && above < lnum - 1) return 0
        }
        val prev = prevCodeLine(lnum)
        if (prev < 0) return 0
        val t = cur.trimStart()
        val closes = if (t.startsWith("}") || t.startsWith(")") || t.startsWith("]")) 1 else 0
        if (startsWithWord(cur, "else")) {
            matchingIf(prev, indentOf(lnum))?.let { return indentOf(it) }
        }
        val pcode = code(prev)
        if (pcode.endsWith(":") || pcode.endsWith("{") || pcode.endsWith("(") || pcode.endsWith("[")) {
            return indentOf(prev) + 1 - closes
        }
        if (t.startsWith(".")) {
            return if (pcode.trimStart().startsWith(".")) indentOf(prev) else indentOf(statementStart(prev)) + 1
        }
        if (pcode.endsWith(",")) return indentOf(prev) - closes
        var start = statementStart(prev)
        var ind = indentOf(start)
        repeat(50) {
            val scode = code(start)
            if (startsWithWord(scode, "when")) return maxOf(ind - closes, 0)
            if (startsWithWord(scode, "default")) {
                start = statementStart(headerOf(start))
                ind = indentOf(start)
            } else {
                val pp = prevCodeLine(start)
                if (pp < 0) return maxOf(ind - closes, 0)
                val ppcode = code(pp)
                if (!(ppcode.endsWith(":") && indentOf(pp) < indentOf(start))) return maxOf(ind - closes, 0)
                when {
                    startsWithWord(ppcode, "when") -> return maxOf(indentOf(pp) - closes, 0)
                    startsWithWord(ppcode, "default") -> { start = statementStart(headerOf(pp)); ind = indentOf(start) }
                    else -> { start = statementStart(pp); ind = indentOf(start) }
                }
            }
        }
        return maxOf(ind - closes, 0)
    }
}

/** Re-indents the new line after Enter so non-block bodies (`if(x):` + Enter) are indented. */
class LowlightEnterHandler : EnterHandlerDelegateAdapter() {
    override fun postProcessEnter(file: PsiFile, editor: Editor, dataContext: DataContext): EnterHandlerDelegate.Result {
        if (file.language != LowlightLanguage) return EnterHandlerDelegate.Result.Continue
        val doc = editor.document
        val caret = editor.caretModel.offset
        val line = doc.getLineNumber(caret)
        if (line == 0) return EnterHandlerDelegate.Result.Continue
        val lineStart = doc.getLineStartOffset(line)
        val lineEnd = doc.getLineEndOffset(line)
        val text = doc.charsSequence
        val lines = (0..line).map { doc.getText(com.intellij.openapi.util.TextRange(doc.getLineStartOffset(it), doc.getLineEndOffset(it))) }
        val unit = detectUnit(lines)
        val level = LowlightIndenter(lines, unit).levelFor(line) ?: return EnterHandlerDelegate.Result.Continue
        var wsEnd = lineStart
        while (wsEnd < lineEnd && (text[wsEnd] == ' ' || text[wsEnd] == '\t')) wsEnd++
        val indent = unit.repeat(level)
        doc.replaceString(lineStart, wsEnd, indent)
        editor.caretModel.moveToOffset(lineStart + indent.length + maxOf(caret - wsEnd, 0))
        return EnterHandlerDelegate.Result.Continue
    }

    private fun detectUnit(lines: List<String>): String {
        for (l in lines) {
            if (l.startsWith("\t")) return "\t"
            if (l.startsWith(" ")) {
                val n = l.takeWhile { it == ' ' }.length
                if (n in 2..8 && l.isNotBlank()) return " ".repeat(n)
            }
        }
        return "\t"
    }
}
