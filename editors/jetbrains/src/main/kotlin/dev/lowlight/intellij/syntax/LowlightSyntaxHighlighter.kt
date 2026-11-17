package dev.lowlight.intellij.syntax

import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.tree.IElementType

object LowlightTextAttributes {
    val LINE_COMMENT = TextAttributesKey.createTextAttributesKey("LOWLIGHT_LINE_COMMENT", DefaultLanguageHighlighterColors.LINE_COMMENT)
    val BLOCK_COMMENT = TextAttributesKey.createTextAttributesKey("LOWLIGHT_BLOCK_COMMENT", DefaultLanguageHighlighterColors.BLOCK_COMMENT)
    val KEYWORD = TextAttributesKey.createTextAttributesKey("LOWLIGHT_KEYWORD", DefaultLanguageHighlighterColors.KEYWORD)
    val PRIMITIVE = TextAttributesKey.createTextAttributesKey("LOWLIGHT_PRIMITIVE", DefaultLanguageHighlighterColors.KEYWORD)
    val TYPE = TextAttributesKey.createTextAttributesKey("LOWLIGHT_TYPE", DefaultLanguageHighlighterColors.CLASS_NAME)
    val BUILTIN = TextAttributesKey.createTextAttributesKey("LOWLIGHT_BUILTIN", DefaultLanguageHighlighterColors.PREDEFINED_SYMBOL)
    val ATTRIBUTE = TextAttributesKey.createTextAttributesKey("LOWLIGHT_ATTRIBUTE", DefaultLanguageHighlighterColors.METADATA)
    val OPTIONAL = TextAttributesKey.createTextAttributesKey("LOWLIGHT_OPTIONAL", DefaultLanguageHighlighterColors.LOCAL_VARIABLE)
    val IDENTIFIER = TextAttributesKey.createTextAttributesKey("LOWLIGHT_IDENTIFIER", DefaultLanguageHighlighterColors.IDENTIFIER)
    val STRING = TextAttributesKey.createTextAttributesKey("LOWLIGHT_STRING", DefaultLanguageHighlighterColors.STRING)
    val CHAR = TextAttributesKey.createTextAttributesKey("LOWLIGHT_CHAR", DefaultLanguageHighlighterColors.STRING)
    val NUMBER = TextAttributesKey.createTextAttributesKey("LOWLIGHT_NUMBER", DefaultLanguageHighlighterColors.NUMBER)
    val OPERATOR = TextAttributesKey.createTextAttributesKey("LOWLIGHT_OPERATOR", DefaultLanguageHighlighterColors.OPERATION_SIGN)
    val UNWIND = TextAttributesKey.createTextAttributesKey("LOWLIGHT_UNWIND", DefaultLanguageHighlighterColors.KEYWORD)
    val BRACES = TextAttributesKey.createTextAttributesKey("LOWLIGHT_BRACES", DefaultLanguageHighlighterColors.BRACES)
    val PARENTHESES = TextAttributesKey.createTextAttributesKey("LOWLIGHT_PARENTHESES", DefaultLanguageHighlighterColors.PARENTHESES)
    val BRACKETS = TextAttributesKey.createTextAttributesKey("LOWLIGHT_BRACKETS", DefaultLanguageHighlighterColors.BRACKETS)
    val COMMA = TextAttributesKey.createTextAttributesKey("LOWLIGHT_COMMA", DefaultLanguageHighlighterColors.COMMA)
    val DOT = TextAttributesKey.createTextAttributesKey("LOWLIGHT_DOT", DefaultLanguageHighlighterColors.DOT)
    val SEMICOLON = TextAttributesKey.createTextAttributesKey("LOWLIGHT_SEMICOLON", DefaultLanguageHighlighterColors.SEMICOLON)
    val BAD_CHAR = TextAttributesKey.createTextAttributesKey("LOWLIGHT_BAD_CHAR", HighlighterColors.BAD_CHARACTER)
}

class LowlightSyntaxHighlighter : SyntaxHighlighterBase() {
    override fun getHighlightingLexer(): Lexer = LowlightLexer()

    override fun getTokenHighlights(tokenType: IElementType): Array<TextAttributesKey> = when (tokenType) {
        LowlightTokenTypes.LINE_COMMENT -> arrayOf(LowlightTextAttributes.LINE_COMMENT)
        LowlightTokenTypes.BLOCK_COMMENT -> arrayOf(LowlightTextAttributes.BLOCK_COMMENT)
        LowlightTokenTypes.KEYWORD -> arrayOf(LowlightTextAttributes.KEYWORD)
        LowlightTokenTypes.PRIMITIVE_TYPE -> arrayOf(LowlightTextAttributes.PRIMITIVE)
        LowlightTokenTypes.INTERFACE_TYPE, LowlightTokenTypes.TYPE_IDENT -> arrayOf(LowlightTextAttributes.TYPE)
        LowlightTokenTypes.BUILTIN_FUNC -> arrayOf(LowlightTextAttributes.BUILTIN)
        LowlightTokenTypes.ATTRIBUTE -> arrayOf(LowlightTextAttributes.ATTRIBUTE)
        LowlightTokenTypes.OPTIONAL_IDENT -> arrayOf(LowlightTextAttributes.OPTIONAL)
        LowlightTokenTypes.IDENTIFIER -> arrayOf(LowlightTextAttributes.IDENTIFIER)
        LowlightTokenTypes.STRING -> arrayOf(LowlightTextAttributes.STRING)
        LowlightTokenTypes.CHAR -> arrayOf(LowlightTextAttributes.CHAR)
        LowlightTokenTypes.NUMBER -> arrayOf(LowlightTextAttributes.NUMBER)
        LowlightTokenTypes.UNWIND_OP -> arrayOf(LowlightTextAttributes.UNWIND)
        LowlightTokenTypes.OPERATOR -> arrayOf(LowlightTextAttributes.OPERATOR)
        LowlightTokenTypes.LBRACE, LowlightTokenTypes.RBRACE -> arrayOf(LowlightTextAttributes.BRACES)
        LowlightTokenTypes.LPAREN, LowlightTokenTypes.RPAREN -> arrayOf(LowlightTextAttributes.PARENTHESES)
        LowlightTokenTypes.LBRACKET, LowlightTokenTypes.RBRACKET,
        LowlightTokenTypes.PLUS_BRACKET, LowlightTokenTypes.HASH_BRACKET -> arrayOf(LowlightTextAttributes.BRACKETS)
        LowlightTokenTypes.COMMA -> arrayOf(LowlightTextAttributes.COMMA)
        LowlightTokenTypes.DOT -> arrayOf(LowlightTextAttributes.DOT)
        LowlightTokenTypes.SEMI, LowlightTokenTypes.COLON -> arrayOf(LowlightTextAttributes.SEMICOLON)
        LowlightTokenTypes.BAD_CHARACTER -> arrayOf(LowlightTextAttributes.BAD_CHAR)
        else -> emptyArray()
    }
}

class LowlightSyntaxHighlighterFactory : SyntaxHighlighterFactory() {
    override fun getSyntaxHighlighter(project: Project?, virtualFile: VirtualFile?): SyntaxHighlighter =
        LowlightSyntaxHighlighter()
}
