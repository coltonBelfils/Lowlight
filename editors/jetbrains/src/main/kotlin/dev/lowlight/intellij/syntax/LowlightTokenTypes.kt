package dev.lowlight.intellij.syntax

import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet
import dev.lowlight.intellij.LowlightLanguage

class LowlightTokenType(debugName: String) : IElementType(debugName, LowlightLanguage)

object LowlightTokenTypes {
    val LINE_COMMENT = LowlightTokenType("LINE_COMMENT")
    val BLOCK_COMMENT = LowlightTokenType("BLOCK_COMMENT")
    val DOC_HEADING = LowlightTokenType("DOC_HEADING")
    val DOC_LINK = LowlightTokenType("DOC_LINK")

    val KEYWORD = LowlightTokenType("KEYWORD")
    val PRIMITIVE_TYPE = LowlightTokenType("PRIMITIVE_TYPE")
    val INTERFACE_TYPE = LowlightTokenType("INTERFACE_TYPE")
    val TYPE_IDENT = LowlightTokenType("TYPE_IDENT")
    val BUILTIN_FUNC = LowlightTokenType("BUILTIN_FUNC")
    val ATTRIBUTE = LowlightTokenType("ATTRIBUTE")
    val OPTIONAL_IDENT = LowlightTokenType("OPTIONAL_IDENT")
    val IDENTIFIER = LowlightTokenType("IDENTIFIER")

    val STRING = LowlightTokenType("STRING")
    val CHAR = LowlightTokenType("CHAR")
    val INTERP = LowlightTokenType("INTERP")
    val NUMBER = LowlightTokenType("NUMBER")

    val OPERATOR = LowlightTokenType("OPERATOR")
    val UNWIND_OP = LowlightTokenType("UNWIND_OP")

    val LBRACE = LowlightTokenType("LBRACE")
    val RBRACE = LowlightTokenType("RBRACE")
    val LPAREN = LowlightTokenType("LPAREN")
    val RPAREN = LowlightTokenType("RPAREN")
    val LBRACKET = LowlightTokenType("LBRACKET")
    val RBRACKET = LowlightTokenType("RBRACKET")
    val PLUS_BRACKET = LowlightTokenType("PLUS_BRACKET")
    val HASH_BRACKET = LowlightTokenType("HASH_BRACKET")
    val COMMA = LowlightTokenType("COMMA")
    val COLON = LowlightTokenType("COLON")
    val SEMI = LowlightTokenType("SEMI")
    val DOT = LowlightTokenType("DOT")

    val BAD_CHARACTER = com.intellij.psi.TokenType.BAD_CHARACTER

    val STRINGS = TokenSet.create(STRING, CHAR)
}
