package dev.lowlight.intellij.settings

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.Service
import com.intellij.openapi.project.Project
import com.intellij.openapi.startup.ProjectActivity
import dev.lowlight.intellij.lsp.LowlightLspBackendBridge

@Service(Service.Level.PROJECT)
class LowlightProjectBridgeService(private val project: Project) : com.intellij.openapi.Disposable {
    init {
        val conn = ApplicationManager.getApplication().messageBus.connect(this)
        conn.subscribe(
            LowlightSettingsListener.TOPIC,
            object : LowlightSettingsListener {
                override fun settingsChanged(newState: LowlightSettingsState, toolchainChanged: Boolean) {
                    LowlightLspBackendBridge.notifyAllSettingsChanged(project, newState, toolchainChanged)
                }
            }
        )
    }

    override fun dispose() {}
}

class LowlightSettingsSyncActivity : ProjectActivity {
    override suspend fun execute(project: Project) {
        project.getService(LowlightProjectBridgeService::class.java)
    }
}
