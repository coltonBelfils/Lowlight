package dev.lowlight.intellij.lsp.lsp4ij

import com.intellij.codeInsight.daemon.DaemonCodeAnalyzer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.PsiFile
import com.redhat.devtools.lsp4ij.LanguageServerEnablementSupport
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.LanguageServerManager
import com.redhat.devtools.lsp4ij.client.LanguageClientImpl
import com.redhat.devtools.lsp4ij.client.features.LSPClientFeatures
import com.redhat.devtools.lsp4ij.client.features.LSPFormattingFeature
import com.redhat.devtools.lsp4ij.client.features.LSPInlayHintFeature
import com.redhat.devtools.lsp4ij.client.features.LSPSemanticTokensFeature
import com.redhat.devtools.lsp4ij.server.OSProcessStreamConnectionProvider
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider
import dev.lowlight.intellij.lsp.LowlightLspBackendBridge
import dev.lowlight.intellij.settings.LowlightSettingsState
import dev.lowlight.intellij.syntax.LowlightTextAttributes
import dev.lowlight.intellij.util.LowlightPaths
import java.util.Collections
import java.util.WeakHashMap

internal object LowlightLsp4ijClientRegistry {
    private val clients = Collections.synchronizedMap(WeakHashMap<Project, MutableSet<LowlightLsp4ijClient>>())
    private val manualOverrides = Collections.synchronizedMap(WeakHashMap<Project, Boolean>())

    fun register(project: Project, client: LowlightLsp4ijClient) {
        clients.computeIfAbsent(project) { Collections.newSetFromMap(WeakHashMap()) }.add(client)
    }

    fun unregister(project: Project, client: LowlightLsp4ijClient) {
        clients[project]?.remove(client)
    }

    fun triggerConfigChange(project: Project) {
        val snapshot = clients[project]?.toList().orEmpty()
        for (c in snapshot) {
            c.triggerChangeConfiguration()
        }
    }

    fun getManualOverride(project: Project): Boolean? = manualOverrides[project]
    fun setManualOverride(project: Project, enabled: Boolean) {
        manualOverrides[project] = enabled
    }
}

class LowlightLsp4ijServerFactory : LanguageServerFactory, LanguageServerEnablementSupport {
    companion object {
        const val SERVER_ID = "lowlightLanguageServer"
    }

    override fun isEnabled(project: Project): Boolean {
        val override = LowlightLsp4ijClientRegistry.getManualOverride(project)
        return override ?: !LowlightPaths.shouldUseNativeLsp()
    }

    override fun setEnabled(enabled: Boolean, project: Project) {
        LowlightLsp4ijClientRegistry.setManualOverride(project, enabled)
    }

    override fun createConnectionProvider(project: Project): StreamConnectionProvider =
        LowlightLsp4ijConnectionProvider(project)

    override fun createLanguageClient(project: Project): LanguageClientImpl =
        LowlightLsp4ijClient(project)

    override fun createClientFeatures(): LSPClientFeatures {
        val features = LSPClientFeatures()
        features.setFormattingFeature(object : LSPFormattingFeature() {
            override fun isExistingFormatterOverrideable(file: PsiFile): Boolean = true
        })
        features.setInlayHintFeature(object : LSPInlayHintFeature() {
            override fun isEnabled(file: PsiFile): Boolean {
                val state = LowlightSettingsState.getInstance()
                return super.isEnabled(file) && (state.inlayHintsTypes || state.inlayHintsParameters)
            }
        })
        features.setSemanticTokensFeature(object : LSPSemanticTokensFeature() {
            override fun getTextAttributesKey(tokenType: String, modifiers: List<String>, file: PsiFile): TextAttributesKey? {
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
                    else -> super.getTextAttributesKey(tokenType, modifiers, file)
                }
            }
        })
        return features
    }
}

class LowlightLsp4ijConnectionProvider(project: Project) : OSProcessStreamConnectionProvider(
    LowlightPaths.createLspCommandLine(project)
) {
    override fun getInitializationOptions(rootUri: VirtualFile?): Any =
        LowlightSettingsState.getInstance().toLspConfigMap()
}

class LowlightLsp4ijClient(project: Project) : LanguageClientImpl(project) {
    init {
        LowlightLsp4ijClientRegistry.register(project, this)
    }

    override fun createSettings(): Any =
        LowlightSettingsState.getInstance().toLspConfigMap()

    override fun dispose() {
        LowlightLsp4ijClientRegistry.unregister(project, this)
        super.dispose()
    }
}

class LowlightLsp4ijBackendBridge : LowlightLspBackendBridge {
    override val backendId: String = "lsp4ij"

    override fun isActive(project: Project): Boolean = !LowlightPaths.shouldUseNativeLsp()

    override fun restart(project: Project) {
        val manager = LanguageServerManager.getInstance(project)
        val stopOpts = LanguageServerManager.StopOptions().setWillDisable(LowlightPaths.shouldUseNativeLsp())
        manager.stop(LowlightLsp4ijServerFactory.SERVER_ID, stopOpts)
        if (!LowlightPaths.shouldUseNativeLsp()) {
            val startOpts = LanguageServerManager.StartOptions()
                .setWillEnable(true)
                .setForceStart(true)
                .setForceRestart(true)
            manager.start(LowlightLsp4ijServerFactory.SERVER_ID, startOpts)
        }
    }

    override fun notifyConfigurationChanged(project: Project, state: LowlightSettingsState) {
        if (!LowlightPaths.shouldUseNativeLsp()) {
            LowlightLsp4ijClientRegistry.triggerConfigChange(project)
            @Suppress("DEPRECATION")
            DaemonCodeAnalyzer.getInstance(project).restart()
        }
    }
}
