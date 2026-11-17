package dev.lowlight.intellij.lsp.nativeclient

import com.intellij.codeInsight.daemon.DaemonCodeAnalyzer
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspServer
import com.intellij.platform.lsp.api.LspServerDescriptor
import com.intellij.platform.lsp.api.LspServerManager
import com.intellij.platform.lsp.api.LspServerSupportProvider
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor
import com.intellij.platform.lsp.api.customization.LspCommandsCustomizer
import com.intellij.platform.lsp.api.customization.LspCommandsSupport
import com.intellij.platform.lsp.api.customization.LspFormattingCustomizer
import com.intellij.platform.lsp.api.customization.LspFormattingSupport
import com.intellij.platform.lsp.api.customization.LspInlayHintCustomizer
import com.intellij.platform.lsp.api.customization.LspInlayHintSupport
import com.intellij.platform.lsp.api.customization.LspSemanticTokensCustomizer
import com.intellij.platform.lsp.api.customization.LspSemanticTokensSupport
import dev.lowlight.intellij.LowlightFileType
import dev.lowlight.intellij.lsp.LowlightCommandHandler
import dev.lowlight.intellij.lsp.LowlightLspBackendBridge
import dev.lowlight.intellij.settings.LowlightSettingsState
import dev.lowlight.intellij.syntax.LowlightTextAttributes
import dev.lowlight.intellij.util.LowlightPaths
import org.eclipse.lsp4j.Command
import org.eclipse.lsp4j.DidChangeConfigurationParams

class LowlightNativeLspSupportProvider : LspServerSupportProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        serverStarter: LspServerSupportProvider.LspServerStarter
    ) {
        if (!LowlightPaths.shouldUseNativeLsp()) {
            return
        }
        if (file.fileType == LowlightFileType || file.extension == "ll") {
            LowlightNativeLspStarter.start(project, serverStarter)
        }
    }
}

private object LowlightNativeLspStarter {
    fun start(project: Project, serverStarter: LspServerSupportProvider.LspServerStarter) {
        serverStarter.ensureServerStarted(LowlightNativeLspDescriptor(project))
    }
}

class LowlightNativeLspDescriptor(project: Project) : ProjectWideLspServerDescriptor(project, "Lowlight") {
    override fun isSupportedFile(file: VirtualFile): Boolean =
        file.fileType == LowlightFileType || file.extension == "ll"

    override fun getLanguageId(file: VirtualFile): String = "lowlight"

    override fun createCommandLine(): GeneralCommandLine = LowlightPaths.createLspCommandLine(project)

    override fun createInitializationOptions(): Any =
        LowlightSettingsState.getInstance().toLspConfigMap()

    override val lspCustomization = object : com.intellij.platform.lsp.api.customization.LspCustomization() {
        override val commandsCustomizer: LspCommandsCustomizer = object : LspCommandsSupport() {
            override fun executeCommand(server: LspServer, contextFile: VirtualFile, command: Command) {
                val cmdId = command.command ?: return
                if (cmdId.startsWith("lowlight.")) {
                    LowlightCommandHandler.execute(project, cmdId, command.arguments, contextFile)
                    return
                }
                super.executeCommand(server, contextFile, command)
            }
        }

        override val formattingCustomizer: LspFormattingCustomizer = object : LspFormattingSupport() {
            override fun shouldFormatThisFileExclusivelyByServer(
                file: VirtualFile,
                ideCanFormatThisFileItself: Boolean,
                serverExplicitlyWishesToFormatThisFile: Boolean
            ): Boolean = isSupportedFile(file)
        }

        override val inlayHintCustomizer: LspInlayHintCustomizer = object : LspInlayHintSupport() {
            override fun shouldAskServerForInlayHints(file: VirtualFile): Boolean {
                val state = LowlightSettingsState.getInstance()
                return isSupportedFile(file) && (state.inlayHintsTypes || state.inlayHintsParameters)
            }
        }

        override val semanticTokensCustomizer: LspSemanticTokensCustomizer = object : LspSemanticTokensSupport() {
            override fun getTextAttributesKey(tokenType: String, modifiers: List<String>): TextAttributesKey? {
                return when (tokenType) {
                    "namespace" -> DefaultLanguageHighlighterColors.IDENTIFIER
                    "type", "struct", "enum", "interface", "typeParameter" -> LowlightTextAttributes.TYPE
                    "parameter" -> DefaultLanguageHighlighterColors.PARAMETER
                    "property" -> DefaultLanguageHighlighterColors.INSTANCE_FIELD
                    "enumMember" -> DefaultLanguageHighlighterColors.CONSTANT
                    "function" -> if ("defaultLibrary" in modifiers) LowlightTextAttributes.BUILTIN else DefaultLanguageHighlighterColors.FUNCTION_DECLARATION
                    "method" -> DefaultLanguageHighlighterColors.INSTANCE_METHOD
                    "keyword" -> LowlightTextAttributes.KEYWORD
                    "comment" -> if ("documentation" in modifiers) DefaultLanguageHighlighterColors.DOC_COMMENT else LowlightTextAttributes.LINE_COMMENT
                    "string" -> LowlightTextAttributes.STRING
                    "number" -> LowlightTextAttributes.NUMBER
                    "operator" -> LowlightTextAttributes.OPERATOR
                    "variable" -> if ("readonly" in modifiers) DefaultLanguageHighlighterColors.CONSTANT else DefaultLanguageHighlighterColors.LOCAL_VARIABLE
                    else -> super.getTextAttributesKey(tokenType, modifiers)
                }
            }
        }
    }
}

class LowlightNativeBackendBridge : LowlightLspBackendBridge {
    override val backendId: String = "native"

    override fun isActive(project: Project): Boolean =
        LowlightPaths.isNativeLspAvailable() &&
            LspServerManager.getInstance(project)
                .getServersForProvider(LowlightNativeLspSupportProvider::class.java)
                .isNotEmpty()

    override fun restart(project: Project) {
        if (!LowlightPaths.isNativeLspAvailable()) return
        val manager = LspServerManager.getInstance(project)
        if (LowlightPaths.shouldUseNativeLsp()) {
            manager.stopAndRestartIfNeeded(LowlightNativeLspSupportProvider::class.java)
        } else {
            manager.stopServers(LowlightNativeLspSupportProvider::class.java)
        }
    }

    override fun notifyConfigurationChanged(project: Project, state: LowlightSettingsState) {
        if (!LowlightPaths.isNativeLspAvailable()) return
        val manager = LspServerManager.getInstance(project)
        val payload = mapOf("lowlight" to state.toLspConfigMap())
        for (server in manager.getServersForProvider(LowlightNativeLspSupportProvider::class.java)) {
            server.sendNotification { ls ->
                ls.workspaceService.didChangeConfiguration(DidChangeConfigurationParams(payload))
            }
        }
        @Suppress("DEPRECATION")
        DaemonCodeAnalyzer.getInstance(project).restart()
    }
}
