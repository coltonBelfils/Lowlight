package dev.lowlight.intellij.testing

import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.icons.AllIcons
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.psi.PsiComment
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiWhiteSpace
import com.intellij.psi.util.PsiTreeUtil
import dev.lowlight.intellij.actions.LowlightProcessRunner
import dev.lowlight.intellij.syntax.LowlightTokenTypes
import java.util.function.Function

/**
 * Adds native gutter Run icons on `@test`, `@test("...")`, `bind test_main`, and `bind main` declarations.
 */
class LowlightRunLineMarkerContributor : RunLineMarkerContributor() {
    data class TestDeclarationTarget(
        val anchorElement: PsiElement,
        val fnName: String,
        val displayName: String,
        val isTestMain: Boolean
    )

    companion object {
        private fun isSkippable(el: PsiElement): Boolean =
            el is PsiWhiteSpace || el is PsiComment ||
                el.node?.elementType == LowlightTokenTypes.LINE_COMMENT ||
                el.node?.elementType == LowlightTokenTypes.BLOCK_COMMENT

        private fun nextCodeLeaf(el: PsiElement): PsiElement? {
            var cur = PsiTreeUtil.nextLeaf(el)
            while (cur != null && isSkippable(cur)) {
                cur = PsiTreeUtil.nextLeaf(cur)
            }
            return cur
        }

        private fun prevCodeLeaf(el: PsiElement): PsiElement? {
            var cur = PsiTreeUtil.prevLeaf(el)
            while (cur != null && isSkippable(cur)) {
                cur = PsiTreeUtil.prevLeaf(cur)
            }
            return cur
        }

        private fun advancePastParens(lparen: PsiElement): PsiElement? {
            var cur: PsiElement? = nextCodeLeaf(lparen)
            var depth = 1
            while (cur != null && depth > 0) {
                val tt = cur.node?.elementType
                if (tt == LowlightTokenTypes.LPAREN) depth++
                else if (tt == LowlightTokenTypes.RPAREN) depth--
                cur = nextCodeLeaf(cur)
            }
            return cur
        }

        /**
         * If `leaf` is the `@test` attribute leaf, resolves the decorated `bind <fnName>` declaration.
         */
        fun resolveTestAtAttributeLeaf(leaf: PsiElement): TestDeclarationTarget? {
            if (leaf.node?.elementType != LowlightTokenTypes.ATTRIBUTE || leaf.text != "@test") {
                return null
            }

            var desc = ""
            var cur: PsiElement? = nextCodeLeaf(leaf) ?: return null

            // Optional `("description")` argument right after `@test`.
            if (cur?.node?.elementType == LowlightTokenTypes.LPAREN) {
                val inner = nextCodeLeaf(cur)
                if (inner?.node?.elementType == LowlightTokenTypes.STRING && inner.text.length >= 2) {
                    desc = inner.text.substring(1, inner.text.length - 1)
                }
                cur = advancePastParens(cur)
            }

            // Skip stacked attributes such as `@track_caller` or `@test_only`.
            while (cur != null && cur.node?.elementType == LowlightTokenTypes.ATTRIBUTE) {
                cur = nextCodeLeaf(cur)
                if (cur?.node?.elementType == LowlightTokenTypes.LPAREN) {
                    cur = advancePastParens(cur)
                }
            }

            val bindLeaf = cur ?: return null
            if (bindLeaf.node?.elementType != LowlightTokenTypes.KEYWORD || bindLeaf.text != "bind") {
                return null
            }
            val nameLeaf = nextCodeLeaf(bindLeaf) ?: return null
            if (nameLeaf.node?.elementType != LowlightTokenTypes.IDENTIFIER) {
                return null
            }
            val fnName = nameLeaf.text
            return TestDeclarationTarget(
                anchorElement = leaf,
                fnName = fnName,
                displayName = desc.ifBlank { fnName },
                isTestMain = fnName == "test_main"
            )
        }

        /**
         * Finds the `@test` or `bind test_main` declaration at or immediately enclosing `element`.
         */
        fun findEnclosingOrAdjacentTest(element: PsiElement): TestDeclarationTarget? {
            if (element.node?.elementType == LowlightTokenTypes.ATTRIBUTE && element.text == "@test") {
                return resolveTestAtAttributeLeaf(element)
            }
            // Also check backward for an attribute if cursor is on `("desc")` right after `@test`.
            var cur: PsiElement? = element
            while (cur != null) {
                val tt = cur.node?.elementType
                if (tt == LowlightTokenTypes.ATTRIBUTE && cur.text == "@test") {
                    resolveTestAtAttributeLeaf(cur)?.let { return it }
                }
                if (tt == LowlightTokenTypes.IDENTIFIER && cur.text == "test_main") {
                    val prev = prevCodeLeaf(cur)
                    if (prev?.node?.elementType == LowlightTokenTypes.KEYWORD && prev.text == "bind") {
                        return TestDeclarationTarget(cur, "test_main", "test_main", true)
                    }
                }
                if (tt == LowlightTokenTypes.KEYWORD && cur.text == "bind") {
                    val nameLeaf = nextCodeLeaf(cur)
                    if (nameLeaf?.node?.elementType == LowlightTokenTypes.IDENTIFIER && nameLeaf.text == "test_main") {
                        return TestDeclarationTarget(nameLeaf, "test_main", "test_main", true)
                    }
                    // Look immediately before `bind` (over stacked attributes / optional parens) for `@test`.
                    var pre = prevCodeLeaf(cur)
                    while (pre != null) {
                        val pt = pre.node?.elementType
                        if (pt == LowlightTokenTypes.ATTRIBUTE) {
                            if (pre.text == "@test") {
                                resolveTestAtAttributeLeaf(pre)?.let { return it }
                            }
                            pre = prevCodeLeaf(pre)
                            continue
                        }
                        if (pt == LowlightTokenTypes.RPAREN) {
                            var depth = 1
                            pre = prevCodeLeaf(pre)
                            while (pre != null && depth > 0) {
                                val ptt = pre.node?.elementType
                                if (ptt == LowlightTokenTypes.RPAREN) depth++
                                else if (ptt == LowlightTokenTypes.LPAREN) depth--
                                pre = prevCodeLeaf(pre)
                            }
                            continue
                        }
                        break
                    }
                    return null
                }
                if (tt == LowlightTokenTypes.KEYWORD && (cur.text == "define" || cur.text == "import")) {
                    return null
                }
                cur = PsiTreeUtil.prevLeaf(cur)
            }
            return null
        }
    }

    override fun getInfo(element: PsiElement): Info? {
        // Gutter line markers must only attach to leaf elements.
        if (element.firstChild != null) return null

        val tt = element.node?.elementType ?: return null

        if (tt == LowlightTokenTypes.ATTRIBUTE && element.text == "@test") {
            val target = resolveTestAtAttributeLeaf(element) ?: return null
            val vf = element.containingFile?.virtualFile ?: return null
            val filter = if (target.isTestMain) "" else "^${Regex.escape(target.fnName)}$"
            val action = object : AnAction("Run '${target.displayName}'", "Run Lowlight test '${target.displayName}'", AllIcons.RunConfigurations.TestState.Run) {
                override fun actionPerformed(e: AnActionEvent) {
                    val project = e.project ?: return
                    LowlightTestLauncher.runTests(project, "Lowlight Test: ${target.displayName}", vf.path, filter)
                }
            }
            return Info(
                AllIcons.RunConfigurations.TestState.Run,
                arrayOf(action),
                Function { "Run test '${target.displayName}'" }
            )
        }

        if (tt == LowlightTokenTypes.IDENTIFIER && (element.text == "main" || element.text == "test_main")) {
            val prev = prevCodeLeaf(element) ?: return null
            if (prev.node?.elementType != LowlightTokenTypes.KEYWORD || prev.text != "bind") {
                return null
            }
            val vf = element.containingFile?.virtualFile ?: return null
            if (element.text == "test_main") {
                val action = object : AnAction("Run 'test_main'", "Run Lowlight test entry point", AllIcons.RunConfigurations.TestState.Run) {
                    override fun actionPerformed(e: AnActionEvent) {
                        val project = e.project ?: return
                        LowlightTestLauncher.runTests(project, "Lowlight Test: ${vf.name}", vf.path)
                    }
                }
                return Info(
                    AllIcons.RunConfigurations.TestState.Run,
                    arrayOf(action),
                    Function { "Run 'test_main'" }
                )
            }
            val runMainAction = object : AnAction("Run '${vf.name}'", "Execute `lowlight run ${vf.name}`", AllIcons.RunConfigurations.TestState.Run) {
                override fun actionPerformed(e: AnActionEvent) {
                    val project = e.project ?: return
                    LowlightProcessRunner.runInConsole(project, "Lowlight: ${vf.name}", "run", vf.path)
                }
            }
            return Info(
                AllIcons.RunConfigurations.TestState.Run,
                arrayOf(runMainAction),
                Function { "Run '${vf.name}'" }
            )
        }

        return null
    }
}
