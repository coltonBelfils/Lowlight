package dev.lowlight.intellij

import com.intellij.application.options.CodeStyle
import com.intellij.psi.tree.IElementType
import com.intellij.testFramework.fixtures.BasePlatformTestCase
import dev.lowlight.intellij.editor.LowlightBraceMatcher
import dev.lowlight.intellij.editor.LowlightCommenter
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
}
