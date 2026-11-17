package dev.lowlight.intellij.editor

import com.intellij.codeInsight.editorActions.SimpleTokenSetQuoteHandler
import com.intellij.lang.BracePair
import com.intellij.lang.Commenter
import com.intellij.lang.PairedBraceMatcher
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType
import dev.lowlight.intellij.syntax.LowlightTokenTypes

class LowlightCommenter : Commenter {
    override fun getLineCommentPrefix(): String = "// "
    override fun getBlockCommentPrefix(): String = "/* "
    override fun getBlockCommentSuffix(): String = " */"
    override fun getCommentedBlockCommentPrefix(): String? = null
    override fun getCommentedBlockCommentSuffix(): String? = null
}

class LowlightBraceMatcher : PairedBraceMatcher {
    companion object {
        private val PAIRS = arrayOf(
            BracePair(LowlightTokenTypes.LBRACE, LowlightTokenTypes.RBRACE, true),
            BracePair(LowlightTokenTypes.LPAREN, LowlightTokenTypes.RPAREN, false),
            BracePair(LowlightTokenTypes.LBRACKET, LowlightTokenTypes.RBRACKET, false),
            BracePair(LowlightTokenTypes.PLUS_BRACKET, LowlightTokenTypes.RBRACKET, false),
            BracePair(LowlightTokenTypes.HASH_BRACKET, LowlightTokenTypes.RBRACKET, false)
        )
    }

    override fun getPairs(): Array<BracePair> = PAIRS
    override fun isPairedBracesAllowedBeforeType(lbraceType: IElementType, contextType: IElementType?): Boolean = true
    override fun getCodeConstructStart(file: PsiFile, openingBraceOffset: Int): Int = openingBraceOffset
}

class LowlightQuoteHandler : SimpleTokenSetQuoteHandler(LowlightTokenTypes.STRINGS)
