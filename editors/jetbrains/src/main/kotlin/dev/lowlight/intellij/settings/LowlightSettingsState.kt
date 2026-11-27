package dev.lowlight.intellij.settings

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.util.messages.Topic
import com.intellij.util.xmlb.XmlSerializerUtil

/**
 * Listener notified whenever Lowlight toolchain, formatting, or inlay-hint settings change.
 */
interface LowlightSettingsListener {
    fun settingsChanged(newState: LowlightSettingsState, toolchainChanged: Boolean)

    companion object {
        val TOPIC: Topic<LowlightSettingsListener> = Topic.create(
            "Lowlight Settings Changed",
            LowlightSettingsListener::class.java
        )
    }
}

/**
 * Persistent application-level settings for the Lowlight JetBrains plugin.
 */
@State(
    name = "LowlightSettings",
    storages = [Storage("lowlight.xml")]
)
@Service(Service.Level.APP)
class LowlightSettingsState : PersistentStateComponent<LowlightSettingsState> {
    var binaryPath: String = ""
    var stdlibPath: String = ""
    var formatIndent: String = "tabs"
    var formatTabWidth: Int = 4
    var formatLineWidth: Int = 100
    var inlayHintsTypes: Boolean = true
    var inlayHintsParameters: Boolean = true
    var preferLspBackend: String = "auto"
    var targetTriple: String = ""

    override fun getState(): LowlightSettingsState = this

    override fun loadState(state: LowlightSettingsState) {
        XmlSerializerUtil.copyBean(state, this)
    }

    /**
     * Converts settings into the nested `lowlight` configuration map expected by
     * `lowlight lsp` (`initializationOptions` and `workspace/didChangeConfiguration`).
     */
    fun toLspConfigMap(): Map<String, Any> = mapOf(
        "format" to mapOf(
            "indent" to formatIndent.ifBlank { "tabs" },
            "tabWidth" to formatTabWidth.coerceIn(1, 16),
            "lineWidth" to formatLineWidth.coerceIn(40, 240)
        ),
        "inlayHints" to mapOf(
            "types" to inlayHintsTypes,
            "parameters" to inlayHintsParameters
        ),
        "target" to targetTriple.trim()
    )

    companion object {
        fun getInstance(): LowlightSettingsState =
            ApplicationManager.getApplication().getService(LowlightSettingsState::class.java)
    }
}
