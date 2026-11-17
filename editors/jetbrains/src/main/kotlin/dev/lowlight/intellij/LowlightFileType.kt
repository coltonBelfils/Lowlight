package dev.lowlight.intellij

import com.intellij.openapi.fileTypes.LanguageFileType
import com.intellij.openapi.util.IconLoader
import javax.swing.Icon

/**
 * File type registration for Lowlight `.ll` source files.
 */
object LowlightFileType : LanguageFileType(LowlightLanguage) {
    private val ICON: Icon = IconLoader.getIcon("/icons/lowlight.svg", LowlightFileType::class.java)

    override fun getName(): String = "Lowlight"
    override fun getDescription(): String = "Lowlight source file"
    override fun getDefaultExtension(): String = "ll"
    override fun getIcon(): Icon = ICON
}
