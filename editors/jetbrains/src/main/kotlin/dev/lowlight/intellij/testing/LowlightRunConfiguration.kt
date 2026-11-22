package dev.lowlight.intellij.testing

import com.intellij.execution.DefaultExecutionResult
import com.intellij.execution.ExecutionResult
import com.intellij.execution.Executor
import com.intellij.execution.ProgramRunnerUtil
import com.intellij.execution.RunManager
import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.CommandLineState
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeBase
import com.intellij.execution.configurations.ConfigurationTypeUtil
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.configurations.RunConfigurationBase
import com.intellij.execution.configurations.RunConfigurationOptions
import com.intellij.execution.configurations.RunProfileState
import com.intellij.execution.executors.DefaultRunExecutor
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.execution.runners.ProgramRunner
import com.intellij.execution.testframework.sm.SMTestRunnerConnectionUtil
import com.intellij.icons.AllIcons
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement
import com.intellij.util.ui.FormBuilder
import dev.lowlight.intellij.LowlightFileType
import dev.lowlight.intellij.LowlightLanguage
import dev.lowlight.intellij.util.LowlightPaths
import java.io.File
import javax.swing.JCheckBox
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JTextField

class LowlightTestRunConfigurationOptions : RunConfigurationOptions() {
    var targetPath: String? by string("")
    var testFilter: String? by string("")
    var backend: String? by string("")
    var releaseMode: Boolean by property(false)
}

class LowlightTestConfigurationType : ConfigurationTypeBase(
    "LowlightTestRunConfiguration",
    "Lowlight Test",
    "Run Lowlight @test suites",
    AllIcons.RunConfigurations.TestState.Run
) {
    init {
        addFactory(LowlightTestConfigurationFactory(this))
    }

    companion object {
        fun getInstance(): LowlightTestConfigurationType =
            ConfigurationTypeUtil.findConfigurationType(LowlightTestConfigurationType::class.java)
    }
}

class LowlightTestConfigurationFactory(type: LowlightTestConfigurationType) : ConfigurationFactory(type) {
    override fun getId(): String = "LowlightTestConfigurationFactory"

    override fun createTemplateConfiguration(project: Project): RunConfiguration =
        LowlightTestRunConfiguration(project, this, "Lowlight Test")

    override fun getOptionsClass() = LowlightTestRunConfigurationOptions::class.java
}

class LowlightTestRunConfiguration(
    project: Project,
    factory: ConfigurationFactory,
    name: String
) : RunConfigurationBase<LowlightTestRunConfigurationOptions>(project, factory, name) {

    public override fun getOptions(): LowlightTestRunConfigurationOptions =
        super.getOptions() as LowlightTestRunConfigurationOptions

    override fun getOptionsClass() = LowlightTestRunConfigurationOptions::class.java

    var targetPath: String
        get() = options.targetPath.orEmpty()
        set(value) {
            options.targetPath = value
        }

    var testFilter: String
        get() = options.testFilter.orEmpty()
        set(value) {
            options.testFilter = value
        }

    var backend: String
        get() = options.backend.orEmpty()
        set(value) {
            options.backend = value
        }

    var releaseMode: Boolean
        get() = options.releaseMode
        set(value) {
            options.releaseMode = value
        }

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> =
        LowlightTestSettingsEditor()

    override fun getState(executor: Executor, environment: ExecutionEnvironment): RunProfileState {
        return object : CommandLineState(environment) {
            override fun startProcess(): ProcessHandler {
                val args = mutableListOf("test", "--json")
                if (releaseMode) {
                    args.add("--release")
                }
                if (backend.isNotBlank() && backend != "default") {
                    args.add("--backend=$backend")
                }
                if (testFilter.isNotBlank()) {
                    args.add("-run")
                    args.add(testFilter)
                }
                if (targetPath.isNotBlank()) {
                    args.add(targetPath)
                }
                val cmd = LowlightPaths.createCliCommandLine(project, *args.toTypedArray())
                return LowlightTestProcessHandler(cmd)
            }

            override fun execute(executor: Executor, runner: ProgramRunner<*>): ExecutionResult {
                val handler = startProcess()
                val consoleProps = LowlightTestConsoleProperties(this@LowlightTestRunConfiguration, executor)
                val console = SMTestRunnerConnectionUtil.createAndAttachConsole("Lowlight", handler, consoleProps)
                return DefaultExecutionResult(console, handler, *createActions(console, handler, executor))
            }
        }
    }
}

private class LowlightTestSettingsEditor : SettingsEditor<LowlightTestRunConfiguration>() {
    private val targetField = JTextField()
    private val filterField = JTextField()
    private val backendBox = JComboBox(arrayOf("default", "llvm", "c11"))
    private val releaseCheck = JCheckBox("Compile with --release (-O2)")

    override fun resetEditorFrom(s: LowlightTestRunConfiguration) {
        targetField.text = s.targetPath
        filterField.text = s.testFilter
        backendBox.selectedItem = s.backend.takeIf { it == "llvm" || it == "c11" } ?: "default"
        releaseCheck.isSelected = s.releaseMode
    }

    override fun applyEditorTo(s: LowlightTestRunConfiguration) {
        s.targetPath = targetField.text.trim()
        s.testFilter = filterField.text.trim()
        s.backend = (backendBox.selectedItem as? String)?.takeIf { it != "default" } ?: ""
        s.releaseMode = releaseCheck.isSelected
    }

    override fun createEditor(): JComponent = FormBuilder.createFormBuilder()
        .addLabeledComponent("Target file or directory:", targetField, 1, false)
        .addLabeledComponent("Test filter (-run regex):", filterField, 1, false)
        .addLabeledComponent("Backend:", backendBox, 1, false)
        .addComponent(releaseCheck, 1)
        .panel
}

class LowlightTestConfigurationProducer : LazyRunConfigurationProducer<LowlightTestRunConfiguration>() {
    override fun getConfigurationFactory(): ConfigurationFactory =
        LowlightTestConfigurationType.getInstance().configurationFactories.first()

    override fun setupConfigurationFromContext(
        configuration: LowlightTestRunConfiguration,
        context: ConfigurationContext,
        sourceElement: Ref<PsiElement>
    ): Boolean {
        val location = context.location ?: return false
        val psiFile = location.psiElement.containingFile ?: return false
        if (psiFile.language != LowlightLanguage && psiFile.fileType != LowlightFileType) {
            return false
        }
        val vf = psiFile.virtualFile ?: return false
        val leaf = location.psiElement

        val testInfo = LowlightRunLineMarkerContributor.findEnclosingOrAdjacentTest(leaf)
        if (testInfo != null) {
            sourceElement.set(testInfo.anchorElement)
            configuration.targetPath = vf.path
            configuration.testFilter = if (testInfo.isTestMain) "" else "^${Regex.escape(testInfo.fnName)}$"
            configuration.name = "Lowlight Test: ${testInfo.displayName}"
            return true
        }

        configuration.targetPath = vf.path
        configuration.testFilter = ""
        configuration.name = "Lowlight Tests: ${vf.name}"
        return true
    }

    override fun isConfigurationFromContext(
        configuration: LowlightTestRunConfiguration,
        context: ConfigurationContext
    ): Boolean {
        val location = context.location ?: return false
        val vf = location.psiElement.containingFile?.virtualFile ?: return false
        if (configuration.targetPath != vf.path) {
            return false
        }
        val testInfo = LowlightRunLineMarkerContributor.findEnclosingOrAdjacentTest(location.psiElement)
        val expectedFilter = if (testInfo != null && !testInfo.isTestMain) {
            "^${Regex.escape(testInfo.fnName)}$"
        } else {
            ""
        }
        return configuration.testFilter == expectedFilter
    }
}

internal object LowlightTestLauncher {
    fun runTests(project: Project, title: String, targetPath: String?, testFilter: String = "") {
        val runManager = RunManager.getInstance(project)
        val factory = LowlightTestConfigurationType.getInstance().configurationFactories.first()
        val cleanTarget = targetPath.orEmpty()

        val existing = runManager.allSettings.firstOrNull { settings ->
            val cfg = settings.configuration as? LowlightTestRunConfiguration ?: return@firstOrNull false
            cfg.targetPath == cleanTarget && cfg.testFilter == testFilter
        }

        val runnerSettings = existing ?: runManager.createConfiguration(title, factory).also { settings ->
            val cfg = settings.configuration as LowlightTestRunConfiguration
            cfg.targetPath = cleanTarget
            cfg.testFilter = testFilter
            if (title.isBlank()) {
                cfg.name = if (cleanTarget.isNotBlank()) "Lowlight Tests: ${File(cleanTarget).name}" else "Lowlight Tests: Project"
            }
            settings.isTemporary = true
            runManager.addConfiguration(settings)
        }

        runManager.selectedConfiguration = runnerSettings
        ProgramRunnerUtil.executeConfiguration(runnerSettings, DefaultRunExecutor.getRunExecutorInstance())
    }
}
