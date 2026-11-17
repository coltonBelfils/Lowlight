package dev.lowlight.intellij

import com.intellij.lang.Language

/**
 * Singleton language descriptor for Lowlight (`.ll`).
 */
object LowlightLanguage : Language("Lowlight") {
    private fun readResolve(): Any = LowlightLanguage
    override fun getDisplayName(): String = "Lowlight"
    override fun isCaseSensitive(): Boolean = true
}
