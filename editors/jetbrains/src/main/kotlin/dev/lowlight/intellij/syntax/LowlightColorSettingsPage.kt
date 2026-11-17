package dev.lowlight.intellij.syntax

import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.options.colors.AttributesDescriptor
import com.intellij.openapi.options.colors.ColorDescriptor
import com.intellij.openapi.options.colors.ColorSettingsPage
import dev.lowlight.intellij.LowlightFileType
import javax.swing.Icon

class LowlightColorSettingsPage : ColorSettingsPage {
    companion object {
        private val DESCRIPTORS = arrayOf(
            AttributesDescriptor("Comment//Line comment", LowlightTextAttributes.LINE_COMMENT),
            AttributesDescriptor("Comment//Block comment", LowlightTextAttributes.BLOCK_COMMENT),
            AttributesDescriptor("Keyword", LowlightTextAttributes.KEYWORD),
            AttributesDescriptor("Primitive type", LowlightTextAttributes.PRIMITIVE),
            AttributesDescriptor("Type / Interface", LowlightTextAttributes.TYPE),
            AttributesDescriptor("Built-in function / intrinsic", LowlightTextAttributes.BUILTIN),
            AttributesDescriptor("Attribute (@test, @must_use)", LowlightTextAttributes.ATTRIBUTE),
            AttributesDescriptor("Optional identifier (name?)", LowlightTextAttributes.OPTIONAL),
            AttributesDescriptor("Identifier", LowlightTextAttributes.IDENTIFIER),
            AttributesDescriptor("String literal", LowlightTextAttributes.STRING),
            AttributesDescriptor("Character literal", LowlightTextAttributes.CHAR),
            AttributesDescriptor("Number literal", LowlightTextAttributes.NUMBER),
            AttributesDescriptor("Operator", LowlightTextAttributes.OPERATOR),
            AttributesDescriptor("Targeted unwind (<-)", LowlightTextAttributes.UNWIND),
            AttributesDescriptor("Braces", LowlightTextAttributes.BRACES),
            AttributesDescriptor("Parentheses", LowlightTextAttributes.PARENTHESES),
            AttributesDescriptor("Brackets", LowlightTextAttributes.BRACKETS)
        )
    }

    override fun getIcon(): Icon = LowlightFileType.icon
    override fun getHighlighter(): SyntaxHighlighter = LowlightSyntaxHighlighter()
    override fun getAdditionalHighlightingTagToDescriptorMap(): Map<String, TextAttributesKey>? = null
    override fun getAttributeDescriptors(): Array<AttributesDescriptor> = DESCRIPTORS
    override fun getColorDescriptors(): Array<ColorDescriptor> = ColorDescriptor.EMPTY_ARRAY
    override fun getDisplayName(): String = "Lowlight"

    override fun getDemoText(): String = """
        import "std/fmt"

        // Computes the clamped distance from origin.
        //
        // # Arguments
        // - `x`: Horizontal coordinate
        // - `y`: Vertical coordinate
        //
        // # Returns
        // Euclidean magnitude using [`fmt.format`].
        define Point struct [
        	x: float,
        	y: float,
        ]

        @test
        bind test_point = () void: {
        	let maybe? = Point[x: 3.0, y: 4.0]
        	if(maybe?) println(fmt.format("dist={}", maybe.x))
        }
    """.trimIndent()
}
