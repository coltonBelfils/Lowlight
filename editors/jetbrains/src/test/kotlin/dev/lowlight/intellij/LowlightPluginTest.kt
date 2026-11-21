package dev.lowlight.intellij

import com.intellij.application.options.CodeStyle
import com.intellij.psi.tree.IElementType
import com.intellij.testFramework.fixtures.BasePlatformTestCase
import dev.lowlight.intellij.editor.LowlightBraceMatcher
import dev.lowlight.intellij.editor.LowlightCommenter
import dev.lowlight.intellij.editor.LowlightIndenter
import dev.lowlight.intellij.settings.LowlightSettingsState
import dev.lowlight.intellij.syntax.LowlightLexer
import dev.lowlight.intellij.syntax.LowlightTokenTypes
import dev.lowlight.intellij.util.LowlightPaths

class LowlightPluginTest : BasePlatformTestCase() {

    fun testFileTypeAndLanguage() {
        val file = myFixture.configureByText(
            "sample.ll",
            """
            import "std/fmt"

            // Computes doubled value.
            bind double_it = (x: int) int: x * 2
            """.trimIndent()
        )
        assertEquals(LowlightFileType, file.fileType)
        assertEquals(LowlightLanguage, file.language)
    }

    fun testCodeStyleDefaultsUseTabsAnd100Width() {
        val file = myFixture.configureByText("style.ll", "bind main = () void: println(\"hi\")\n")
        val settings = CodeStyle.getSettings(project)
        val common = settings.getCommonSettings(LowlightLanguage)
        val indent = settings.getIndentOptionsByFile(file)
        assertTrue("Expected tab indentation by default", indent.USE_TAB_CHARACTER)
        assertEquals(4, indent.TAB_SIZE)
        assertEquals(4, indent.INDENT_SIZE)
        assertEquals(100, common.RIGHT_MARGIN)
    }

    fun testLexerRecognizesLowlightConstructs() {
        val src = """
            // doc comment
            /* block */
            @test
            define Color enum [ Red, Green ]
            bind run = (opt?: int) void: {
            	let arr = +[1, 0xff, 0b101, 3.5]
            	if(opt?) println("val=${'$'}opt")
            	<- none
            }
        """.trimIndent()

        val lexer = LowlightLexer()
        lexer.start(src)
        val tokens = mutableListOf<Pair<IElementType, String>>()
        while (lexer.tokenType != null) {
            val tt = lexer.tokenType!!
            tokens.add(tt to src.substring(lexer.tokenStart, lexer.tokenEnd))
            lexer.advance()
        }

        assertTrue(tokens.any { it.first == LowlightTokenTypes.LINE_COMMENT })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.BLOCK_COMMENT })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.ATTRIBUTE && it.second == "@test" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "define" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "enum" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.OPTIONAL_IDENT && it.second == "opt?" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.PRIMITIVE_TYPE && it.second == "int" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.PLUS_BRACKET && it.second == "+[" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.NUMBER && it.second == "0xff" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.NUMBER && it.second == "0b101" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.NUMBER && it.second == "3.5" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.BUILTIN_FUNC && it.second == "println" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.UNWIND_OP && it.second == "<-" })
        assertFalse(tokens.any { it.first == LowlightTokenTypes.BAD_CHARACTER })
    }

    fun testCommenterAndBraceMatcher() {
        val commenter = LowlightCommenter()
        assertEquals("// ", commenter.lineCommentPrefix)
        assertEquals("/* ", commenter.blockCommentPrefix)
        assertEquals(" */", commenter.blockCommentSuffix)

        val matcher = LowlightBraceMatcher()
        assertTrue(matcher.pairs.any { it.leftBraceType == LowlightTokenTypes.LBRACE && it.rightBraceType == LowlightTokenTypes.RBRACE })
        assertTrue(matcher.pairs.any { it.leftBraceType == LowlightTokenTypes.PLUS_BRACKET && it.rightBraceType == LowlightTokenTypes.RBRACKET })
    }

    fun testSettingsMapAndLspCommand() {
        val state = LowlightSettingsState()
        state.formatIndent = "spaces"
        state.formatTabWidth = 2
        state.formatLineWidth = 90
        state.inlayHintsTypes = false
        state.inlayHintsParameters = true

        val map = state.toLspConfigMap()
        val fmt = map["format"] as Map<*, *>
        val hints = map["inlayHints"] as Map<*, *>
        assertEquals("spaces", fmt["indent"])
        assertEquals(2, fmt["tabWidth"])
        assertEquals(90, fmt["lineWidth"])
        assertEquals(false, hints["types"])
        assertEquals(true, hints["parameters"])

        val cmd = LowlightPaths.createLspCommandLine(project)
        assertTrue(cmd.parametersList.parameters.containsAll(listOf("lsp", "--stdio")))

        val bridges = dev.lowlight.intellij.lsp.LowlightLspBackendBridge.EP_NAME.extensionList
        assertTrue("Expected Native LSP bridge", bridges.any { it.backendId == "native" })
        assertTrue("Expected LSP4IJ bridge", bridges.any { it.backendId == "lsp4ij" })
    }

    // The Enter indenter reproduces `lowlight fmt` indentation for every line of formatted code.
    fun testIndenterMatchesFormattedCode() {
        val src = listOf(
            "define Lookup result [",
            "\tvalue: string,",
            "\tMissing: string,",
            "]",
            "",
            "bind label = (n: int) string:",
            "\tmatch(n):",
            "\t\twhen 1: \"one\"",
            "\t\twhen 2:",
            "\t\t\t\"two\"",
            "\t\tdefault: \"many\"",
            "",
            "bind use = (k: string) Result[string]: {",
            "\tlet item = find(k):",
            "\t\twhen Missing(why): func <- Err(Error.new(why))",
            "\t\tdefault panic",
            "\tif(item == \"x\"):",
            "\t\tprintln(\"x: http://example.com\")",
            "\telse:",
            "\t\tprintln(\"other\")",
            "\tfor(i in 0..3):",
            "\t\tif(i > 1): {",
            "\t\t\tprintln(i)",
            "\t\t} else: println(0)",
            "\tvalue(item)",
            "}",
        )
        val indenter = LowlightIndenter(src, "\t")
        for ((i, line) in src.withIndex()) {
            if (line.isBlank()) continue
            val want = line.takeWhile { it == '\t' }.length
            assertEquals("line ${i + 1}: $line", want, indenter.levelFor(i))
        }
    }

    // Pressing Enter after a `:` header indents the non-block body.
    fun testEnterIndentsAfterColonHeader() {
        myFixture.configureByText("enter.ll", "bind f = (x: int) int:<caret>\n")
        myFixture.type("\n")
        myFixture.checkResult("bind f = (x: int) int:\n\t<caret>\n")
    }

    // `result` after `define Name`, `default panic`, and `name!` identifiers are lexed contextually.
    fun testLexerRecognizesResultSyntax() {
        val src = "define Lookup result [\n\tvalue: int,\n]\nbind f = (): {\n\tlet result = 1\n\tlet cfg! = load():\n\t\tdefault panic\n\tif(a!=b): panic(\"x\")\n}\n"
        val lexer = LowlightLexer()
        lexer.start(src)
        val tokens = mutableListOf<Pair<IElementType, String>>()
        while (lexer.tokenType != null) {
            tokens.add(lexer.tokenType!! to src.substring(lexer.tokenStart, lexer.tokenEnd))
            lexer.advance()
        }
        assertEquals(1, tokens.count { it.first == LowlightTokenTypes.KEYWORD && it.second == "result" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.IDENTIFIER && it.second == "result" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.RESULT_IDENT && it.second == "cfg!" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "default" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "panic" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.BUILTIN_FUNC && it.second == "panic" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.IDENTIFIER && it.second == "a" })
    }
}
