package dev.lowlight.intellij.lsp.lsp4ij

import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.redhat.devtools.lsp4ij.commands.LSPCommand
import com.redhat.devtools.lsp4ij.commands.LSPCommandAction
import dev.lowlight.intellij.lsp.LowlightCommandHandler

abstract class LowlightBaseLsp4ijCommandAction(private val commandId: String) : LSPCommandAction() {
    override fun commandPerformed(command: LSPCommand, e: AnActionEvent) {
        val project = e.project ?: return
        val vf = e.getData(CommonDataKeys.VIRTUAL_FILE)
        LowlightCommandHandler.execute(project, commandId, command.originalArguments, vf)
    }
}

class LowlightLsp4ijRunMainAction : LowlightBaseLsp4ijCommandAction("lowlight.runMain")
class LowlightLsp4ijRunFileAction : LowlightBaseLsp4ijCommandAction("lowlight.runFile")
class LowlightLsp4ijRunTestAction : LowlightBaseLsp4ijCommandAction("lowlight.runTest")
class LowlightLsp4ijRunAllTestsAction : LowlightBaseLsp4ijCommandAction("lowlight.runAllTests")
class LowlightLsp4ijGoToLocationAction : LowlightBaseLsp4ijCommandAction("lowlight.goToLocation")
class LowlightLsp4ijShowLocationsAction : LowlightBaseLsp4ijCommandAction("lowlight.showLocations")
class LowlightLsp4ijQuickPickAction : LowlightBaseLsp4ijCommandAction("lowlight.showImplementationsQuickPick")
