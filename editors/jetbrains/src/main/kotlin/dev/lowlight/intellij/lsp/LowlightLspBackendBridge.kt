package dev.lowlight.intellij.lsp

import com.intellij.openapi.extensions.ExtensionPointName
import com.intellij.openapi.project.Project
import dev.lowlight.intellij.settings.LowlightSettingsState

/**
 * Optional backend bridge implemented independently by Native LSP and LSP4IJ modules so
 * core plugin code never directly links against optional IDE modules.
 */
interface LowlightLspBackendBridge {
    val backendId: String
    fun isActive(project: Project): Boolean
    fun restart(project: Project)
    fun notifyConfigurationChanged(project: Project, state: LowlightSettingsState)

    companion object {
        val EP_NAME: ExtensionPointName<LowlightLspBackendBridge> =
            ExtensionPointName.create("dev.lowlight.intellij.lspBackendBridge")

        fun restartAll(project: Project) {
            for (bridge in EP_NAME.extensionList) {
                runCatching { bridge.restart(project) }
            }
        }

        fun notifyAllSettingsChanged(project: Project, state: LowlightSettingsState, toolchainChanged: Boolean) {
            for (bridge in EP_NAME.extensionList) {
                runCatching {
                    if (toolchainChanged) {
                        bridge.restart(project)
                    } else {
                        bridge.notifyConfigurationChanged(project, state)
                    }
                }
            }
        }
    }
}
