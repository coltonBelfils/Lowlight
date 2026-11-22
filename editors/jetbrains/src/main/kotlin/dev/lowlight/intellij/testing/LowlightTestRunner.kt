package dev.lowlight.intellij.testing

import com.intellij.execution.Executor
import com.intellij.execution.Location
import com.intellij.execution.PsiLocation
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.process.KillableProcessHandler
import com.intellij.execution.process.ProcessOutputTypes
import com.intellij.execution.testframework.sm.runner.SMTRunnerConsoleProperties
import com.intellij.execution.testframework.sm.runner.SMTestLocator
import com.intellij.openapi.application.ReadAction
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Key
import com.intellij.openapi.vfs.LocalFileSystem
import com.intellij.psi.PsiDocumentManager
import com.intellij.psi.PsiManager
import com.intellij.psi.search.GlobalSearchScope
import java.io.File
import java.net.URI

/**
 * Intercepts streaming `lowlight test --json` stdout from [GeneralCommandLine] and feeds
 * translated TeamCity service messages to the attached `SMTestRunner` console.
 */
class LowlightTestProcessHandler(cmd: GeneralCommandLine) : KillableProcessHandler(cmd) {
    private val bridge = LowlightTeamCityBridge()
    private val stdoutBuffer = StringBuilder()
    private val lock = Any()

    override fun notifyTextAvailable(text: String, outputType: Key<*>) {
        if (outputType != ProcessOutputTypes.STDOUT) {
            super.notifyTextAvailable(text, outputType)
            return
        }

        val emitted = mutableListOf<String>()
        synchronized(lock) {
            stdoutBuffer.append(text)
            while (true) {
                val idx = stdoutBuffer.indexOf("\n")
                if (idx < 0) break
                val line = stdoutBuffer.substring(0, idx).trimEnd('\r')
                stdoutBuffer.delete(0, idx + 1)
                emitted.addAll(bridge.processLine(line))
            }
        }
        for (tcLine in emitted) {
            super.notifyTextAvailable("$tcLine\n", ProcessOutputTypes.STDOUT)
        }
    }

    override fun notifyProcessTerminated(exitCode: Int) {
        val remaining = mutableListOf<String>()
        synchronized(lock) {
            val tail = stdoutBuffer.toString().trimEnd('\r')
            stdoutBuffer.setLength(0)
            if (tail.isNotBlank()) {
                remaining.addAll(bridge.processLine(tail))
            }
            remaining.addAll(bridge.finish())
        }
        for (tcLine in remaining) {
            super.notifyTextAvailable("$tcLine\n", ProcessOutputTypes.STDOUT)
        }
        super.notifyProcessTerminated(exitCode)
    }
}

/**
 * Resolves `lowlight://<file>?line=<L>&col=<C>` locationHints back to exact PSI positions
 * so double-clicking a test node in IntelliJ's test tree jumps to `@test` / `bind <fn>`.
 */
object LowlightTestLocator : SMTestLocator {
    private const val PROTOCOL = "lowlight"

    override fun getLocation(
        protocol: String,
        path: String,
        project: Project,
        scope: GlobalSearchScope
    ): List<Location<*>> {
        if (protocol != PROTOCOL || path.isBlank()) {
            return emptyList()
        }

        val parsed = parsePathAndPos(path)
        return ReadAction.compute<List<Location<*>>, Throwable> {
            val vf = LocalFileSystem.getInstance().findFileByIoFile(File(parsed.filePath))
                ?: LowlightConsoleFilter.resolveVirtualFile(project, parsed.filePath)
                ?: return@compute emptyList()
            val psiFile = PsiManager.getInstance(project).findFile(vf) ?: return@compute emptyList()
            val doc = PsiDocumentManager.getInstance(project).getDocument(psiFile)
            val offset = if (doc != null && doc.lineCount > 0) {
                val ln = (parsed.line - 1).coerceIn(0, doc.lineCount - 1)
                val lineStart = doc.getLineStartOffset(ln)
                val lineEnd = doc.getLineEndOffset(ln)
                (lineStart + (parsed.col - 1).coerceAtLeast(0)).coerceAtMost(lineEnd)
            } else {
                0
            }
            val leaf = psiFile.findElementAt(offset) ?: psiFile
            listOf(PsiLocation.fromPsiElement(project, leaf))
        }
    }

    internal data class ParsedTarget(val filePath: String, val line: Int, val col: Int)

    internal fun parsePathAndPos(raw: String): ParsedTarget {
        val qIdx = raw.indexOf('?')
        val filePart = if (qIdx >= 0) raw.substring(0, qIdx) else raw
        val normalizedFile = if (filePart.startsWith("file://")) {
            runCatching { File(URI(filePart)).absolutePath }.getOrDefault(filePart)
        } else {
            filePart
        }
        var line = 1
        var col = 1
        if (qIdx >= 0 && qIdx + 1 < raw.length) {
            for (pair in raw.substring(qIdx + 1).split('&')) {
                val eq = pair.indexOf('=')
                if (eq <= 0) continue
                val k = pair.substring(0, eq)
                val v = pair.substring(eq + 1).toIntOrNull() ?: continue
                when (k) {
                    "line" -> line = v.coerceAtLeast(1)
                    "col" -> col = v.coerceAtLeast(1)
                }
            }
        }
        return ParsedTarget(normalizedFile, line, col)
    }
}

class LowlightTestConsoleProperties(
    config: LowlightTestRunConfiguration,
    executor: Executor
) : SMTRunnerConsoleProperties(config, "Lowlight", executor) {
    init {
        isIdBasedTestTree = false
        addStackTraceFilter(LowlightConsoleFilter(config.project))
    }

    override fun getTestLocator(): SMTestLocator = LowlightTestLocator
}
