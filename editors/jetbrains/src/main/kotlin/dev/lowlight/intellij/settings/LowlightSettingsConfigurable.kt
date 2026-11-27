package dev.lowlight.intellij.settings

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory
import com.intellij.openapi.options.SearchableConfigurable
import com.intellij.openapi.ui.ComboBox
import com.intellij.openapi.ui.TextComponentAccessor
import com.intellij.openapi.ui.TextFieldWithBrowseButton
import com.intellij.ui.JBIntSpinner
import com.intellij.ui.components.JBCheckBox
import com.intellij.util.ui.FormBuilder
import javax.swing.JComponent
import javax.swing.JPanel

/**
 * Settings panel under Settings/Preferences | Languages & Frameworks | Lowlight.
 */
class LowlightSettingsConfigurable : SearchableConfigurable {
    private var panel: JPanel? = null
    private val binaryPathField = TextFieldWithBrowseButton()
    private val stdlibPathField = TextFieldWithBrowseButton()
    private val formatIndentCombo = ComboBox(arrayOf("tabs", "spaces"))
    private val formatTabWidthSpinner = JBIntSpinner(4, 1, 16)
    private val formatLineWidthSpinner = JBIntSpinner(100, 40, 240)
    private val inlayTypesCheckbox = JBCheckBox("Show inferred type inlay hints")
    private val inlayParamsCheckbox = JBCheckBox("Show parameter name inlay hints at call sites")
    private val lspBackendCombo = ComboBox(arrayOf("auto", "native", "lsp4ij"))
    private val targetTripleCombo = ComboBox(
        arrayOf(
            "",
            "x86_64-unknown-linux-gnu",
            "aarch64-unknown-linux-gnu",
            "aarch64-apple-darwin",
            "x86_64-apple-darwin",
            "x86_64-pc-windows-msvc",
            "wasm32-unknown-unknown",
            "riscv64-unknown-linux-gnu"
        )
    ).apply { isEditable = true }

    override fun getId(): String = "dev.lowlight.intellij.settings"
    override fun getDisplayName(): String = "Lowlight"

    override fun createComponent(): JComponent {
        @Suppress("DEPRECATION")
        binaryPathField.addBrowseFolderListener(
            "Select Lowlight Compiler Binary",
            "Path to the `lowlight` CLI executable (supports `lowlight lsp --stdio`).",
            null,
            FileChooserDescriptorFactory.createSingleFileNoJarsDescriptor(),
            TextComponentAccessor.TEXT_FIELD_WHOLE_TEXT
        )
        @Suppress("DEPRECATION")
        stdlibPathField.addBrowseFolderListener(
            "Select Lowlight Standard Library Root",
            "Directory containing Lowlight's `std/` modules (or the `std` directory itself).",
            null,
            FileChooserDescriptorFactory.createSingleFolderDescriptor(),
            TextComponentAccessor.TEXT_FIELD_WHOLE_TEXT
        )

        val built = FormBuilder.createFormBuilder()
            .addLabeledComponent("Compiler / LSP binary path (empty = auto):", binaryPathField, 1, false)
            .addLabeledComponent("Standard library root (`LOWLIGHT_ROOT`, empty = auto):", stdlibPathField, 1, false)
            .addSeparator(8)
            .addLabeledComponent("Default formatter indent (when `.lowlightfmt` is absent):", formatIndentCombo, 1, false)
            .addLabeledComponent("Formatter tab / indent width:", formatTabWidthSpinner, 1, false)
            .addLabeledComponent("Formatter target line width:", formatLineWidthSpinner, 1, false)
            .addSeparator(8)
            .addComponent(inlayTypesCheckbox)
            .addComponent(inlayParamsCheckbox)
            .addSeparator(8)
            .addLabeledComponent("Preferred LSP backend (`auto` prefers Native LSP when available):", lspBackendCombo, 1, false)
            .addLabeledComponent("Compilation / comptime target triple (empty = host default):", targetTripleCombo, 1, false)
            .addComponentFillVertically(JPanel(), 0)
            .panel

        reset()
        panel = built
        return built
    }

    override fun isModified(): Boolean {
        val state = LowlightSettingsState.getInstance()
        return binaryPathField.text.trim() != state.binaryPath ||
            stdlibPathField.text.trim() != state.stdlibPath ||
            (formatIndentCombo.selectedItem as? String ?: "tabs") != state.formatIndent ||
            formatTabWidthSpinner.number != state.formatTabWidth ||
            formatLineWidthSpinner.number != state.formatLineWidth ||
            inlayTypesCheckbox.isSelected != state.inlayHintsTypes ||
            inlayParamsCheckbox.isSelected != state.inlayHintsParameters ||
            (lspBackendCombo.selectedItem as? String ?: "auto") != state.preferLspBackend ||
            selectedTargetTriple() != state.targetTriple
    }

    private fun selectedTargetTriple(): String {
        val edited = (targetTripleCombo.editor.item as? String) ?: (targetTripleCombo.selectedItem as? String) ?: ""
        return edited.trim()
    }

    override fun apply() {
        val state = LowlightSettingsState.getInstance()
        val newBinary = binaryPathField.text.trim()
        val newStdlib = stdlibPathField.text.trim()
        val newBackend = lspBackendCombo.selectedItem as? String ?: "auto"
        val newTarget = selectedTargetTriple()
        val toolchainChanged =
            newBinary != state.binaryPath ||
                newStdlib != state.stdlibPath ||
                newBackend != state.preferLspBackend

        state.binaryPath = newBinary
        state.stdlibPath = newStdlib
        state.formatIndent = formatIndentCombo.selectedItem as? String ?: "tabs"
        state.formatTabWidth = formatTabWidthSpinner.number
        state.formatLineWidth = formatLineWidthSpinner.number
        state.inlayHintsTypes = inlayTypesCheckbox.isSelected
        state.inlayHintsParameters = inlayParamsCheckbox.isSelected
        state.preferLspBackend = newBackend
        state.targetTriple = newTarget

        ApplicationManager.getApplication().messageBus
            .syncPublisher(LowlightSettingsListener.TOPIC)
            .settingsChanged(state, toolchainChanged)
    }

    override fun reset() {
        val state = LowlightSettingsState.getInstance()
        binaryPathField.text = state.binaryPath
        stdlibPathField.text = state.stdlibPath
        formatIndentCombo.selectedItem = state.formatIndent.ifBlank { "tabs" }
        formatTabWidthSpinner.number = state.formatTabWidth.coerceIn(1, 16)
        formatLineWidthSpinner.number = state.formatLineWidth.coerceIn(40, 240)
        inlayTypesCheckbox.isSelected = state.inlayHintsTypes
        inlayParamsCheckbox.isSelected = state.inlayHintsParameters
        lspBackendCombo.selectedItem = state.preferLspBackend.ifBlank { "auto" }
        targetTripleCombo.selectedItem = state.targetTriple
        targetTripleCombo.editor.item = state.targetTriple
    }

    override fun disposeUIResources() {
        panel = null
    }
}
