package dev.lowlight.intellij.actions

import com.intellij.execution.executors.DefaultRunExecutor
import com.intellij.execution.filters.TextConsoleBuilderFactory
import com.intellij.execution.process.KillableColoredProcessHandler
import com.intellij.execution.process.ProcessTerminatedListener
import com.intellij.execution.ui.RunContentDescriptor
import com.intellij.execution.ui.RunContentManager
import com.intellij.icons.AllIcons
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.actionSystem.ActionUpdateThread
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.command.WriteCommandAction
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.openapi.fileEditor.FileEditorManager
import com.intellij.openapi.progress.ProgressIndicator
import com.intellij.openapi.progress.Task
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.testFramework.LightVirtualFile
import dev.lowlight.intellij.LowlightFileType
import dev.lowlight.intellij.LowlightLanguage
import dev.lowlight.intellij.lsp.LowlightLspBackendBridge
import dev.lowlight.intellij.settings.LowlightSettingsListener
import dev.lowlight.intellij.settings.LowlightSettingsState
import dev.lowlight.intellij.testing.LowlightConsoleFilter
import dev.lowlight.intellij.testing.LowlightTestLauncher
import dev.lowlight.intellij.util.LowlightPaths
import java.awt.BorderLayout
import java.nio.charset.StandardCharsets
import javax.swing.JPanel

internal object LowlightProcessRunner {
    fun runInConsole(project: Project, title: String, vararg args: String) {
        FileDocumentManager.getInstance().saveAllDocuments()
        val cmd = LowlightPaths.createCliCommandLine(project, *args)
        val handler = KillableColoredProcessHandler(cmd)
        ProcessTerminatedListener.attach(handler)

        val console = TextConsoleBuilderFactory.getInstance().createBuilder(project).console
        console.addMessageFilter(LowlightConsoleFilter(project))
        console.attachToProcess(handler)

        val panel = JPanel(BorderLayout())
        panel.add(console.component, BorderLayout.CENTER)

        val descriptor = RunContentDescriptor(console, handler, panel, title)
        RunContentManager.getInstance(project).showRunContent(DefaultRunExecutor.getRunExecutorInstance(), descriptor)
        handler.startNotify()
    }

    fun isLowlightFile(file: VirtualFile?): Boolean =
        file != null && !file.isDirectory && (file.fileType == LowlightFileType || file.extension == "ll")

    fun notifyInfo(project: Project?, msg: String) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("Lowlight")
            .createNotification(msg, NotificationType.INFORMATION)
            .notify(project)
    }

    fun notifyError(project: Project?, msg: String) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("Lowlight")
            .createNotification(msg, NotificationType.ERROR)
            .notify(project)
    }
}

class LowlightRunFileAction : AnAction("Run Lowlight File", "Execute `lowlight run` on current file", AllIcons.Actions.Execute) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE)
        e.presentation.isEnabledAndVisible = e.project != null && LowlightProcessRunner.isLowlightFile(file)
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE) ?: return
        LowlightProcessRunner.runInConsole(project, "Lowlight: ${file.name}", "run", file.path)
    }
}

class LowlightRunFileTestsAction : AnAction("Run Lowlight Tests in File", "Execute `lowlight test` on current file", AllIcons.RunConfigurations.TestState.Run) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE)
        e.presentation.isEnabledAndVisible = e.project != null && LowlightProcessRunner.isLowlightFile(file)
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE) ?: return
        LowlightTestLauncher.runTests(project, "Lowlight Tests: ${file.name}", file.path)
    }
}

class LowlightRunAllTestsAction : AnAction("Run All Lowlight Tests", "Execute `lowlight test` across project", AllIcons.RunConfigurations.TestState.Run_run) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabledAndVisible = e.project != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        LowlightTestLauncher.runTests(project, "Lowlight Tests: Project", null)
    }
}

class LowlightFormatFileAction : AnAction("Format with Lowlight", "Format current buffer using canonical `lowlight fmt` style", AllIcons.Actions.ReformatCode) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE)
        val editor = e.getData(CommonDataKeys.EDITOR)
        e.presentation.isEnabledAndVisible = e.project != null && editor != null && LowlightProcessRunner.isLowlightFile(file)
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val document = editor.document
        val original = document.text
        val settings = LowlightSettingsState.getInstance()

        object : Task.Backgroundable(project, "Formatting Lowlight file", false) {
            override fun run(indicator: ProgressIndicator) {
                val cmd = LowlightPaths.createCliCommandLine(
                    project,
                    "fmt",
                    "--indent=${settings.formatIndent.ifBlank { "tabs" }}",
                    "--tab-width=${settings.formatTabWidth}",
                    "--line-width=${settings.formatLineWidth}"
                )
                val process = cmd.createProcess()
                process.outputStream.use { out ->
                    out.write(original.toByteArray(StandardCharsets.UTF_8))
                }
                val formatted = process.inputStream.readBytes().toString(StandardCharsets.UTF_8)
                val errText = process.errorStream.readBytes().toString(StandardCharsets.UTF_8)
                val exit = process.waitFor()
                ApplicationManager.getApplication().invokeLater {
                    if (exit != 0) {
                        LowlightProcessRunner.notifyError(project, "lowlight fmt failed: ${errText.ifBlank { "exit code $exit" }}")
                        return@invokeLater
                    }
                    if (formatted != original && document.text == original) {
                        WriteCommandAction.runWriteCommandAction(project) {
                            document.setText(formatted)
                        }
                    }
                }
            }
        }.queue()
    }
}

class LowlightRestartLspAction : AnAction("Restart Lowlight Language Server", "Restart active Lowlight LSP backend(s)", AllIcons.Actions.Restart) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabledAndVisible = e.project != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        LowlightLspBackendBridge.restartAll(project)
        LowlightProcessRunner.notifyInfo(project, "Restarted Lowlight Language Server.")
    }
}

class LowlightExpandDocumentAction : AnAction(
    "Expand Comptime & @derive",
    "Run `lowlight expand` on the current file and open expanded preview",
    AllIcons.Actions.Preview
) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE)
        e.presentation.isEnabledAndVisible = e.project != null && LowlightProcessRunner.isLowlightFile(file)
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val file = e.getData(CommonDataKeys.VIRTUAL_FILE) ?: return
        FileDocumentManager.getInstance().saveAllDocuments()
        val target = LowlightSettingsState.getInstance().targetTriple.trim()

        object : Task.Backgroundable(project, "Expanding Lowlight file", false) {
            override fun run(indicator: ProgressIndicator) {
                val args = mutableListOf("expand")
                if (target.isNotEmpty()) {
                    args.add("--target=$target")
                }
                args.add(file.path)
                val cmd = LowlightPaths.createCliCommandLine(project, *args.toTypedArray())
                val process = cmd.createProcess()
                val expanded = process.inputStream.readBytes().toString(StandardCharsets.UTF_8)
                val errText = process.errorStream.readBytes().toString(StandardCharsets.UTF_8)
                val exit = process.waitFor()
                ApplicationManager.getApplication().invokeLater {
                    if (exit != 0) {
                        LowlightProcessRunner.notifyError(project, "lowlight expand failed: ${errText.ifBlank { "exit code $exit" }}")
                        return@invokeLater
                    }
                    val targetLabel = if (target.isNotEmpty()) target else "host"
                    val header = "// Expanded by Lowlight (target: $targetLabel)\n"
                    val previewFile = LightVirtualFile("${file.nameWithoutExtension}.expanded.ll", LowlightLanguage, header + expanded)
                    previewFile.isWritable = false
                    FileEditorManager.getInstance(project).openFile(previewFile, true)
                }
            }
        }.queue()
    }
}

class LowlightSwitchTargetAction : AnAction(
    "Switch Lowlight Target",
    "Select compilation & comptime target triple for LSP analysis",
    AllIcons.General.Settings
) {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabledAndVisible = e.project != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val state = LowlightSettingsState.getInstance()
        val presets = arrayOf(
            "",
            "x86_64-unknown-linux-gnu",
            "aarch64-unknown-linux-gnu",
            "aarch64-apple-darwin",
            "x86_64-apple-darwin",
            "x86_64-pc-windows-msvc",
            "wasm32-unknown-unknown",
            "riscv64-unknown-linux-gnu"
        )
        val initial = state.targetTriple.trim()
        val chosen = Messages.showEditableChooseDialog(
            "Enter target triple (empty = host default):",
            "Switch Lowlight Compilation Target",
            null,
            presets,
            initial,
            null
        ) ?: return
        val next = chosen.trim()
        if (next != state.targetTriple) {
            state.targetTriple = next
            ApplicationManager.getApplication().messageBus
                .syncPublisher(LowlightSettingsListener.TOPIC)
                .settingsChanged(state, false)
        }
        LowlightProcessRunner.notifyInfo(project, "Lowlight target: ${next.ifBlank { "host default" }}")
    }
}
