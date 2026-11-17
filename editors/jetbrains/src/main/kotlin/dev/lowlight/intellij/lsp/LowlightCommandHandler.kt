package dev.lowlight.intellij.lsp

import com.google.gson.Gson
import com.google.gson.JsonElement
import com.google.gson.JsonObject
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.fileEditor.OpenFileDescriptor
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.openapi.vfs.VfsUtil
import com.intellij.openapi.vfs.VirtualFile
import dev.lowlight.intellij.actions.LowlightProcessRunner
import java.io.File
import java.net.URI

/**
 * Shared client-side executor for custom `lowlight.*` LSP commands dispatched by CodeLenses,
 * CodeActions, and quick picks across both Native LSP and LSP4IJ backends.
 */
object LowlightCommandHandler {
    private val gson = Gson()

    data class PickItem(
        val label: String,
        val description: String,
        val uri: String,
        val line: Int,
        val character: Int
    ) {
        override fun toString(): String =
            if (description.isNotBlank()) "$label  ($description)" else label
    }

    fun execute(
        project: Project,
        commandId: String,
        arguments: List<Any?>?,
        contextFile: VirtualFile?
    ) {
        val args = arguments.orEmpty()
        when (commandId) {
            "lowlight.runMain", "lowlight.runFile" -> {
                val path = resolveTargetPath(args.firstOrNull(), contextFile) ?: return
                LowlightProcessRunner.runInConsole(project, "Lowlight: ${File(path).name}", "run", path)
            }
            "lowlight.runTest" -> {
                val path = resolveTargetPath(args.getOrNull(0), contextFile) ?: return
                val testName = asString(args.getOrNull(1))
                if (testName != null && testName.isNotBlank()) {
                    LowlightProcessRunner.runInConsole(
                        project,
                        "Lowlight Test: $testName",
                        "test",
                        path,
                        "-run",
                        testName
                    )
                } else {
                    LowlightProcessRunner.runInConsole(project, "Lowlight Test: ${File(path).name}", "test", path)
                }
            }
            "lowlight.runAllTests" -> {
                val path = resolveTargetPath(args.firstOrNull(), contextFile)
                if (path != null) {
                    LowlightProcessRunner.runInConsole(project, "Lowlight Tests: ${File(path).name}", "test", path)
                } else {
                    LowlightProcessRunner.runInConsole(project, "Lowlight Tests: Project", "test")
                }
            }
            "lowlight.goToLocation" -> {
                val loc = parseLocationObj(args.firstOrNull())
                if (loc != null) {
                    navigateTo(project, loc.uri, loc.line, loc.character)
                }
            }
            "lowlight.showLocations" -> {
                val rawList = when {
                    args.size >= 3 -> args[2]
                    args.isNotEmpty() -> args[0]
                    else -> null
                }
                val items = parseLocationsList(rawList)
                showLocationChooser(project, "Select Location", items)
            }
            "lowlight.showImplementationsQuickPick" -> {
                val title = asString(args.getOrNull(0))?.takeIf { it.isNotBlank() } ?: "Select Implementation"
                val items = parseQuickPickItems(args.getOrNull(1))
                showLocationChooser(project, title, items)
            }
        }
    }

    private fun showLocationChooser(project: Project, title: String, items: List<PickItem>) {
        if (items.isEmpty()) {
            LowlightProcessRunner.notifyInfo(project, "No target locations found.")
            return
        }
        if (items.size == 1 && items[0].uri.isNotBlank()) {
            navigateTo(project, items[0].uri, items[0].line, items[0].character)
            return
        }
        ApplicationManager.getApplication().invokeLater {
            JBPopupFactory.getInstance()
                .createPopupChooserBuilder(items)
                .setTitle(title)
                .setItemChosenCallback { picked ->
                    if (picked.uri.isNotBlank()) {
                        navigateTo(project, picked.uri, picked.line, picked.character)
                    } else {
                        LowlightProcessRunner.notifyInfo(project, "${picked.label} is a built-in Lowlight primitive.")
                    }
                }
                .createPopup()
                .showCenteredInCurrentWindow(project)
        }
    }

    private fun navigateTo(project: Project, uriStr: String, line: Int, character: Int) {
        ApplicationManager.getApplication().invokeLater {
            val vf = resolveVirtualFile(uriStr) ?: return@invokeLater
            OpenFileDescriptor(project, vf, line.coerceAtLeast(0), character.coerceAtLeast(0)).navigate(true)
        }
    }

    private fun resolveVirtualFile(uriStr: String): VirtualFile? {
        return runCatching {
            val path = if (uriStr.startsWith("file://")) File(URI(uriStr)) else File(uriStr)
            VfsUtil.findFileByIoFile(path, true)
        }.getOrNull()
    }

    private fun resolveTargetPath(rawArg: Any?, fallback: VirtualFile?): String? {
        val s = asString(rawArg)
        if (!s.isNullOrBlank()) {
            if (s.startsWith("file://")) {
                return runCatching { File(URI(s)).absolutePath }.getOrNull()
            }
            return s
        }
        return fallback?.path
    }

    private fun asJson(arg: Any?): JsonElement? = when (arg) {
        null -> null
        is JsonElement -> arg
        else -> runCatching { gson.toJsonTree(arg) }.getOrNull()
    }

    private fun asString(arg: Any?): String? {
        if (arg is String) return arg
        val el = asJson(arg) ?: return null
        return if (el.isJsonPrimitive && el.asJsonPrimitive.isString) el.asString else null
    }

    private fun parseLocationObj(arg: Any?): PickItem? {
        val obj = asJson(arg)?.takeIf { it.isJsonObject }?.asJsonObject ?: return null
        return locationFromJson(obj)
    }

    private fun locationFromJson(obj: JsonObject): PickItem? {
        val uri = obj.get("uri")?.takeIf { it.isJsonPrimitive }?.asString ?: return null
        val range = obj.getAsJsonObject("range")
        val start = range?.getAsJsonObject("start")
        val line = start?.get("line")?.asInt ?: 0
        val charIdx = start?.get("character")?.asInt ?: 0
        val fileName = runCatching { File(URI(uri)).name }.getOrElse { uri }
        return PickItem(
            label = "$fileName:${line + 1}:${charIdx + 1}",
            description = uri.removePrefix("file://"),
            uri = uri,
            line = line,
            character = charIdx
        )
    }

    private fun parseLocationsList(arg: Any?): List<PickItem> {
        val arr = asJson(arg)?.takeIf { it.isJsonArray }?.asJsonArray ?: return emptyList()
        return arr.mapNotNull { el ->
            if (el.isJsonObject) locationFromJson(el.asJsonObject) else null
        }
    }

    private fun parseQuickPickItems(arg: Any?): List<PickItem> {
        val arr = asJson(arg)?.takeIf { it.isJsonArray }?.asJsonArray ?: return emptyList()
        return arr.mapNotNull { el ->
            if (!el.isJsonObject) return@mapNotNull null
            val o = el.asJsonObject
            val label = o.get("label")?.takeIf { it.isJsonPrimitive }?.asString ?: return@mapNotNull null
            val desc = o.get("description")?.takeIf { it.isJsonPrimitive }?.asString ?: ""
            val uri = o.get("uri")?.takeIf { it.isJsonPrimitive }?.asString ?: ""
            val line = o.get("line")?.takeIf { it.isJsonPrimitive }?.asInt ?: 0
            val ch = o.get("character")?.takeIf { it.isJsonPrimitive }?.asInt ?: 0
            PickItem(label, desc, uri, line, ch)
        }
    }
}
