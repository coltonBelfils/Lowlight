package dev.lowlight.intellij.testing

import com.intellij.execution.filters.ConsoleFilterProvider
import com.intellij.execution.filters.Filter
import com.intellij.execution.filters.OpenFileHyperlinkInfo
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.project.Project
import com.intellij.openapi.project.guessProjectDir
import com.intellij.openapi.vfs.LocalFileSystem
import com.intellij.openapi.vfs.VirtualFile
import dev.lowlight.intellij.util.LowlightPaths
import java.io.File

/**
 * Turns `path/to/file.ll:line:col` and `file.ll:line` occurrences in Run/Test consoles
 * into clickable hyperlinks that jump directly to the exact file, line, and column.
 */
class LowlightConsoleFilter(private val project: Project) : Filter, DumbAware {
    companion object {
        // Matches e.g. `std/json/json.ll:541:2`, `C:\proj\a.ll:12:3`, or `test_file.ll:5:5`.
        private val FILE_LOC_REGEX = Regex("""((?:[A-Za-z]:[\\/])?[^\s:()"'<>]+\.ll):(\d+)(?::(\d+))?""")

        internal fun resolveVirtualFile(project: Project, rawPath: String): VirtualFile? {
            val fs = LocalFileSystem.getInstance()
            val cleaned = rawPath.replace('\\', File.separatorChar)
            val slashCleaned = rawPath.replace('\\', '/')
            val direct = File(cleaned)
            if (direct.isAbsolute && direct.isFile) {
                return fs.findFileByIoFile(direct)
            }

            project.guessProjectDir()?.findFileByRelativePath(slashCleaned)?.takeIf { !it.isDirectory }?.let {
                return it
            }

            val base = project.basePath
            if (!base.isNullOrBlank()) {
                val rel = File(base, cleaned)
                if (rel.isFile) {
                    return fs.findFileByIoFile(rel)
                }
            }

            val stdRoot = LowlightPaths.resolveStdlibRoot(project)
            if (!stdRoot.isNullOrBlank()) {
                val fromRoot = File(stdRoot, cleaned)
                if (fromRoot.isFile) {
                    return fs.findFileByIoFile(fromRoot)
                }
                val fromStd = File(File(stdRoot, "std"), cleaned)
                if (fromStd.isFile) {
                    return fs.findFileByIoFile(fromStd)
                }
            }
            return null
        }

        internal fun findMatches(line: String): List<MatchSpec> {
            val out = mutableListOf<MatchSpec>()
            for (m in FILE_LOC_REGEX.findAll(line)) {
                val path = m.groupValues[1]
                val lineNum = m.groupValues[2].toIntOrNull() ?: continue
                val colNum = m.groupValues[3].toIntOrNull() ?: 1
                out.add(
                    MatchSpec(
                        startInLine = m.range.first,
                        endInLine = m.range.last + 1,
                        rawPath = path,
                        lineOneBased = lineNum,
                        colOneBased = colNum
                    )
                )
            }
            return out
        }
    }

    internal data class MatchSpec(
        val startInLine: Int,
        val endInLine: Int,
        val rawPath: String,
        val lineOneBased: Int,
        val colOneBased: Int
    )

    override fun applyFilter(line: String, entireLength: Int): Filter.Result? {
        val matches = findMatches(line)
        if (matches.isEmpty()) return null

        val lineStartOffset = entireLength - line.length
        val items = mutableListOf<Filter.ResultItem>()
        for (spec in matches) {
            val vf = resolveVirtualFile(project, spec.rawPath) ?: continue
            val link = OpenFileHyperlinkInfo(
                project,
                vf,
                (spec.lineOneBased - 1).coerceAtLeast(0),
                (spec.colOneBased - 1).coerceAtLeast(0)
            )
            items.add(
                Filter.ResultItem(
                    lineStartOffset + spec.startInLine,
                    lineStartOffset + spec.endInLine,
                    link
                )
            )
        }
        return if (items.isEmpty()) null else Filter.Result(items)
    }
}

class LowlightConsoleFilterProvider : ConsoleFilterProvider {
    override fun getDefaultFilters(project: Project): Array<Filter> =
        arrayOf(LowlightConsoleFilter(project))
}
