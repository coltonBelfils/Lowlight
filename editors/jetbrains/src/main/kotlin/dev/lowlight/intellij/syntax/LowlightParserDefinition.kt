package dev.lowlight.intellij.syntax

import com.intellij.extapi.psi.PsiFileBase
import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiBuilder
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.fileTypes.FileType
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.impl.source.tree.LeafPsiElement
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet
import dev.lowlight.intellij.LowlightFileType
import dev.lowlight.intellij.LowlightLanguage

class LowlightPsiFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, LowlightLanguage) {
    override fun getFileType(): FileType = LowlightFileType
    override fun toString(): String = "Lowlight File"
}

/**
 * Lightweight ParserDefinition binding `.ll` files to [LowlightLanguage] so IntelliJ platform
 * editor features (highlighter, commenter, brace matcher, quote handler, code style, LSP semantic
 * tokens, code actions, and inlay hints) attach to the proper language PSI tree.
 */
class LowlightParserDefinition : ParserDefinition {
    companion object {
        val FILE = IFileElementType(LowlightLanguage)
        val COMMENTS = TokenSet.create(
            LowlightTokenTypes.LINE_COMMENT,
            LowlightTokenTypes.BLOCK_COMMENT
        )
    }

    override fun createLexer(project: Project?): Lexer = LowlightLexer()

    override fun createParser(project: Project?): PsiParser = PsiParser { root, builder: PsiBuilder ->
        val rootMarker = builder.mark()
        while (!builder.eof()) {
            builder.advanceLexer()
        }
        rootMarker.done(root)
        builder.treeBuilt
    }

    override fun getFileNodeType(): IFileElementType = FILE
    override fun getCommentTokens(): TokenSet = COMMENTS
    override fun getStringLiteralElements(): TokenSet = LowlightTokenTypes.STRINGS
    override fun createElement(node: ASTNode): PsiElement = LeafPsiElement(node.elementType, node.text)
    override fun createFile(viewProvider: FileViewProvider): PsiFile = LowlightPsiFile(viewProvider)
}
