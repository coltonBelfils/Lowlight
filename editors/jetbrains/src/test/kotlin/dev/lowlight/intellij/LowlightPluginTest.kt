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
            bind double_it = fn(x: int) int: x * 2
            """.trimIndent()
        )
        assertEquals(LowlightFileType, file.fileType)
        assertEquals(LowlightLanguage, file.language)
    }

    fun testCodeStyleDefaultsUseTabsAnd100Width() {
        val file = myFixture.configureByText("style.ll", "bind main = fn() void: println(\"hi\")\n")
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
            bind run = fn(opt?: int) void: {
            	let arr = +[1, 0xff, 0b101, 3.5]
            	let k = 2
            	let f = cl(x: int) int: x * k
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
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "fn" })
        assertTrue(tokens.any { it.first == LowlightTokenTypes.KEYWORD && it.second == "cl" })
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
            "bind label = fn(n: int) string:",
            "\tmatch(n):",
            "\t\twhen 1: \"one\"",
            "\t\twhen 2:",
            "\t\t\t\"two\"",
            "\t\tdefault: \"many\"",
            "",
            "bind use = fn(k: string) Result[string]: {",
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
        myFixture.configureByText("enter.ll", "bind f = fn(x: int) int:<caret>\n")
        myFixture.type("\n")
        myFixture.checkResult("bind f = fn(x: int) int:\n\t<caret>\n")
    }

    // `result` after `define Name`, `default panic`, and `name!` identifiers are lexed contextually.
    fun testLexerRecognizesResultSyntax() {
        val src = "define Lookup result [\n\tvalue: int,\n]\nbind f = fn(): {\n\tlet result = 1\n\tlet cfg! = load():\n\t\tdefault panic\n\tif(a!=b): panic(\"x\")\n}\n"
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

    fun testConsoleFilterMatchesFileLineAndCol() {
        val line = "    at std/json/json.ll:316:64 — error and sub/app.ll:12:3"
        val matches = dev.lowlight.intellij.testing.LowlightConsoleFilter.findMatches(line)
        assertEquals(2, matches.size)
        assertEquals("std/json/json.ll", matches[0].rawPath)
        assertEquals(316, matches[0].lineOneBased)
        assertEquals(64, matches[0].colOneBased)
        assertEquals("sub/app.ll", matches[1].rawPath)
        assertEquals(12, matches[1].lineOneBased)
        assertEquals(3, matches[1].colOneBased)

        myFixture.addFileToProject("sub/sample_test.ll", "@test\nbind ok = fn(): assert(true)\n")
        val filter = dev.lowlight.intellij.testing.LowlightConsoleFilter(project)
        val outLine = "    sub/sample_test.ll:2:15: assertion failed\n"
        val res = filter.applyFilter(outLine, 100 + outLine.length)
        assertNotNull("Expected console filter match when project file exists", res)
        assertEquals(1, res!!.resultItems.size)
        val item = res.resultItems[0]
        assertEquals(104, item.highlightStartOffset)
        assertEquals(104 + "sub/sample_test.ll:2:15".length, item.highlightEndOffset)
    }

    fun testTeamCityBridgeTranslatesNdjsonEvents() {
        val bridge = dev.lowlight.intellij.testing.LowlightTeamCityBridge()
        val runMsgs = bridge.processLine(
            """{"event":"run","package":"std/json","file":"/tmp/a.ll","relFile":"std/json/a.ll","line":10,"col":1,"name":"t_pass","desc":"passes cleanly"}"""
        )
        assertEquals(2, runMsgs.size)
        assertTrue(runMsgs[0].startsWith("##teamcity[testSuiteStarted name='std/json/a.ll'"))
        assertTrue(runMsgs[1].contains("testStarted name='passes cleanly'"))
        assertTrue(runMsgs[1].contains("locationHint='lowlight:///tmp/a.ll?line=10&col=1'"))

        val passMsgs = bridge.processLine(
            """{"event":"pass","file":"/tmp/a.ll","relFile":"std/json/a.ll","name":"t_pass","desc":"passes cleanly","durationUs":2500,"output":"hi\n"}"""
        )
        assertEquals(2, passMsgs.size)
        assertTrue(passMsgs[0].contains("testStdOut"))
        assertTrue(passMsgs[1].contains("testFinished name='passes cleanly' duration='2'"))

        bridge.processLine(
            """{"event":"run","package":"std/json","file":"/tmp/a.ll","relFile":"std/json/a.ll","line":20,"col":1,"name":"t_fail","desc":"diff fail"}"""
        )
        val failMsgs = bridge.processLine(
            """{"event":"fail","file":"/tmp/a.ll","relFile":"std/json/a.ll","name":"t_fail","desc":"diff fail","message":"std/json/a.ll:22:5: assertion failed\n  expected: 'a'\n       got: 'b'","expected":"'a'","actual":"'b'"}"""
        )
        assertEquals(2, failMsgs.size)
        assertTrue(failMsgs[0].contains("type='comparisonFailure'"))
        assertTrue(failMsgs[0].contains("expected='|'a|''"))
        assertTrue(failMsgs[0].contains("actual='|'b|''"))
        assertTrue(failMsgs[1].contains("testFinished name='diff fail'"))

        val endMsgs = bridge.finish()
        assertEquals(1, endMsgs.size)
        assertTrue(endMsgs[0].contains("testSuiteFinished name='std/json/a.ll'"))
    }

    fun testRunLineMarkerAndTestLocator() {
        val psiFile = myFixture.configureByText(
            "gutter_test.ll",
            """
            @test("adds numbers")
            bind test_add = fn(): {
            	assert(1 + 1 == 2)
            }

            bind test_main = fn(): {
            	test_add()
            }
            """.trimIndent()
        )
        val contributor = dev.lowlight.intellij.testing.LowlightRunLineMarkerContributor()
        val text = psiFile.text
        val atTestOffset = text.indexOf("@test")
        val atLeaf = psiFile.findElementAt(atTestOffset)!!
        val info = contributor.getInfo(atLeaf)
        assertNotNull("Expected gutter Run icon on @test attribute", info)

        val insideTestOffset = text.indexOf("assert")
        val insideLeaf = psiFile.findElementAt(insideTestOffset)!!
        val enclosing = dev.lowlight.intellij.testing.LowlightRunLineMarkerContributor.findEnclosingOrAdjacentTest(insideLeaf)
        assertNotNull(enclosing)
        assertEquals("test_add", enclosing!!.fnName)
        assertEquals("adds numbers", enclosing.displayName)

        val testMainOffset = text.indexOf("test_main")
        val testMainLeaf = psiFile.findElementAt(testMainOffset)!!
        assertNotNull("Expected gutter Run icon on test_main", contributor.getInfo(testMainLeaf))

        val parsed = dev.lowlight.intellij.testing.LowlightTestLocator.parsePathAndPos("/tmp/foo.ll?line=14&col=5")
        assertEquals("/tmp/foo.ll", parsed.filePath)
        assertEquals(14, parsed.line)
        assertEquals(5, parsed.col)
    }

    fun testComptimeKeywordsAndTargetConfig() {
        val src = "@comptime_only\nconst X = comptime { compile_error(\"hi\"); 1 }\n"
        val tokens = lexAll(src)
        assertEquals(LowlightTokenTypes.KEYWORD, tokenForWord(src, tokens, "const"))
        assertEquals(LowlightTokenTypes.KEYWORD, tokenForWord(src, tokens, "comptime"))
        assertEquals(LowlightTokenTypes.BUILTIN_FUNC, tokenForWord(src, tokens, "compile_error"))

        val state = dev.lowlight.intellij.settings.LowlightSettingsState()
        state.targetTriple = "x86_64-unknown-linux-gnu"
        val cfg = state.toLspConfigMap()
        assertEquals("x86_64-unknown-linux-gnu", cfg["target"])
    }
}
