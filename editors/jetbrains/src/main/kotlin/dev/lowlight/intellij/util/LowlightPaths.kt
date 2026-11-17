package dev.lowlight.intellij.util

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.PathEnvironmentVariableUtil
import com.intellij.ide.plugins.PluginManagerCore
import com.intellij.openapi.application.ApplicationInfo
import com.intellij.openapi.application.PathManager
import com.intellij.openapi.extensions.PluginId
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.SystemInfo
import dev.lowlight.intellij.settings.LowlightSettingsState
import java.io.File
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.jar.JarFile

/**
 * Resolves the `lowlight` compiler/LSP executable and `LOWLIGHT_ROOT` standard library root.
 */
object LowlightPaths {
    const val PLUGIN_ID = "dev.lowlight.intellij"
    private val stdManifest = listOf(
        "io.ll", "fmt.ll", "strings.ll", "math.ll", "time.ll", "rand.ll", "fs.ll",
        "path.ll", "os.ll", "net.ll", "http.ll", "json.ll", "sync.ll", "test.ll",
        "pool.ll", "arena.ll", "bytes.ll", "crypto.ll", "regex.ll",
        "core/core.ll", "core/option.ll", "core/result.ll", "core/list.ll",
        "core/map.ll", "core/set.ll"
    )

    fun exeName(): String = if (SystemInfo.isWindows) "lowlight.exe" else "lowlight"

    fun resolveBinary(project: Project?, settings: LowlightSettingsState = LowlightSettingsState.getInstance()): String {
        val configured = settings.binaryPath.trim()
        if (configured.isNotEmpty()) {
            return configured
        }

        val exe = exeName()
        project?.basePath?.let { base ->
            findInAncestors(File(base), exe)?.let { return it.absolutePath }
        }

        pluginRootPath()?.let { root ->
            val bundled = root.resolve("bin").resolve(exe).toFile()
            if (bundled.isFile) {
                ensureExecutable(bundled)
                return bundled.absolutePath
            }
        }

        extractBundledBinary()?.let { return it.absolutePath }

        PathEnvironmentVariableUtil.findExecutableInPathOnAnyOS(exe)?.let {
            return it.absolutePath
        }
        if (SystemInfo.isUnix) {
            listOf("/usr/local/bin/$exe", "/opt/homebrew/bin/$exe", "${System.getProperty("user.home")}/.local/bin/$exe")
                .map(::File)
                .firstOrNull { it.isFile && it.canExecute() }
                ?.let { return it.absolutePath }
        }

        return exe
    }

    fun resolveStdlibRoot(project: Project?, settings: LowlightSettingsState = LowlightSettingsState.getInstance()): String? {
        val configured = settings.stdlibPath.trim()
        if (configured.isNotEmpty()) {
            val f = File(configured)
            return if (f.name == "std" && hasStdMarker(f)) f.parentFile?.absolutePath ?: f.absolutePath else f.absolutePath
        }

        project?.basePath?.let { base ->
            findStdRootInAncestors(File(base))?.let { return it.absolutePath }
        }

        System.getenv("LOWLIGHT_ROOT")?.takeIf { it.isNotBlank() }?.let { envRoot ->
            val f = File(envRoot)
            if (hasStdMarker(File(f, "std"))) return f.absolutePath
        }

        pluginRootPath()?.let { root ->
            val stdDir = root.resolve("std").toFile()
            if (hasStdMarker(stdDir)) {
                return root.toFile().absolutePath
            }
        }

        return extractBundledStdRoot()?.absolutePath
    }

    fun createLspCommandLine(project: Project?): GeneralCommandLine {
        val settings = LowlightSettingsState.getInstance()
        val bin = resolveBinary(project, settings)
        val cmd = GeneralCommandLine(bin, "lsp", "--stdio")
        cmd.withCharset(Charsets.UTF_8)
        project?.basePath?.takeIf { it.isNotBlank() }?.let { base ->
            val dir = File(base)
            if (dir.isDirectory) {
                cmd.withWorkDirectory(dir)
            }
        }
        resolveStdlibRoot(project, settings)?.let { stdRoot ->
            cmd.environment["LOWLIGHT_ROOT"] = stdRoot
        }
        return cmd
    }

    fun createCliCommandLine(project: Project?, vararg args: String): GeneralCommandLine {
        val settings = LowlightSettingsState.getInstance()
        val bin = resolveBinary(project, settings)
        val cmd = GeneralCommandLine(bin, *args)
        cmd.withCharset(Charsets.UTF_8)
        project?.basePath?.takeIf { it.isNotBlank() }?.let { base ->
            val dir = File(base)
            if (dir.isDirectory) {
                cmd.withWorkDirectory(dir)
            }
        }
        resolveStdlibRoot(project, settings)?.let { stdRoot ->
            cmd.environment["LOWLIGHT_ROOT"] = stdRoot
        }
        return cmd
    }

    fun isNativeLspAvailable(): Boolean {
        val baseline = runCatching { ApplicationInfo.getInstance().build.baselineVersion }.getOrDefault(0)
        if (baseline < 242) {
            return false
        }
        val id = PluginId.getId("com.intellij.modules.lsp")
        if (PluginManagerCore.getPlugin(id) == null || PluginManagerCore.isDisabled(id)) {
            return false
        }
        return try {
            Class.forName("com.intellij.platform.lsp.api.customization.LspCustomization", false, LowlightPaths::class.java.classLoader)
            true
        } catch (_: Throwable) {
            false
        }
    }

    fun shouldUseNativeLsp(): Boolean {
        return when (LowlightSettingsState.getInstance().preferLspBackend) {
            "lsp4ij" -> false
            "native" -> isNativeLspAvailable()
            else -> isNativeLspAvailable()
        }
    }

    private fun pluginRootPath(): Path? {
        val id = PluginId.getId(PLUGIN_ID)
        return PluginManagerCore.getPlugin(id)?.pluginPath
    }

    private fun findInAncestors(startDir: File, targetName: String): File? {
        var dir: File? = startDir
        while (dir != null) {
            val candidate = File(dir, targetName)
            if (candidate.isFile && (SystemInfo.isWindows || candidate.canExecute())) {
                return candidate
            }
            dir = dir.parentFile
        }
        return null
    }

    private fun findStdRootInAncestors(startDir: File): File? {
        var dir: File? = startDir
        while (dir != null) {
            if (hasStdMarker(File(dir, "std"))) {
                return dir
            }
            dir = dir.parentFile
        }
        return null
    }

    private fun hasStdMarker(stdDir: File): Boolean =
        stdDir.isDirectory && (File(stdDir, "core/core.ll").isFile || File(stdDir, "math.ll").isFile)

    private fun ensureExecutable(f: File) {
        if (!SystemInfo.isWindows && !f.canExecute()) {
            f.setExecutable(true, false)
        }
    }

    private fun extractBundledBinary(): File? {
        val exe = exeName()
        val resourcePath = "lowlight-assets/bin/$exe"
        val stream = LowlightPaths::class.java.classLoader.getResourceAsStream(resourcePath) ?: return null
        return stream.use { input ->
            val targetDir = File(PathManager.getSystemPath(), "lowlight-plugin/bin")
            targetDir.mkdirs()
            val out = File(targetDir, exe)
            Files.copy(input, out.toPath(), StandardCopyOption.REPLACE_EXISTING)
            ensureExecutable(out)
            out
        }
    }

    private fun extractBundledStdRoot(): File? {
        val cl = LowlightPaths::class.java.classLoader
        val marker = cl.getResource("lowlight-assets/std/core/core.ll") ?: return null
        val targetRoot = File(PathManager.getSystemPath(), "lowlight-plugin")
        val targetStd = File(targetRoot, "std")

        if (marker.protocol == "file") {
            val markerFile = File(marker.toURI())
            val stdDir = markerFile.parentFile?.parentFile
            if (stdDir != null && hasStdMarker(stdDir)) {
                return stdDir.parentFile
            }
        }

        if (marker.protocol == "jar") {
            val jarUrlPath = marker.path.substringBefore("!/")
            val jarPath = File(java.net.URI.create(jarUrlPath))
            if (jarPath.isFile) {
                JarFile(jarPath).use { jar ->
                    val prefix = "lowlight-assets/std/"
                    val entries = jar.entries()
                    while (entries.hasMoreElements()) {
                        val entry = entries.nextElement()
                        if (!entry.isDirectory && entry.name.startsWith(prefix)) {
                            val rel = entry.name.removePrefix(prefix)
                            val dest = File(targetStd, rel)
                            dest.parentFile?.mkdirs()
                            jar.getInputStream(entry).use { input ->
                                Files.copy(input, dest.toPath(), StandardCopyOption.REPLACE_EXISTING)
                            }
                        }
                    }
                }
                if (hasStdMarker(targetStd)) return targetRoot
            }
        }

        var copiedAny = false
        for (rel in stdManifest) {
            val stream = cl.getResourceAsStream("lowlight-assets/std/$rel") ?: continue
            stream.use { input ->
                val dest = File(targetStd, rel)
                dest.parentFile?.mkdirs()
                Files.copy(input, dest.toPath(), StandardCopyOption.REPLACE_EXISTING)
                copiedAny = true
            }
        }
        return if (copiedAny && hasStdMarker(targetStd)) targetRoot else null
    }
}
