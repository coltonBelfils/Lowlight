package dev.lowlight.intellij.testing

import com.google.gson.JsonObject
import com.google.gson.JsonParser

/**
 * Translates streaming `lowlight test --json` NDJSON lines into TeamCity service messages
 * consumed by IntelliJ's `SMTestRunner`.
 */
class LowlightTeamCityBridge {
    private var openSuite: String? = null
    private var activeTest: String? = null

    companion object {
        fun escapeTc(value: String): String {
            val sb = StringBuilder(value.length + 8)
            for (ch in value) {
                when (ch) {
                    '|' -> sb.append("||")
                    '\'' -> sb.append("|'")
                    '\n' -> sb.append("|n")
                    '\r' -> sb.append("|r")
                    '[' -> sb.append("|[")
                    ']' -> sb.append("|]")
                    else -> sb.append(ch)
                }
            }
            return sb.toString()
        }
    }

    fun processLine(rawLine: String): List<String> {
        val trimmed = rawLine.trim()
        if (trimmed.isEmpty()) return emptyList()
        if (!trimmed.startsWith("{")) {
            return listOf(rawLine)
        }

        val obj = runCatching { JsonParser.parseString(trimmed) }
            .getOrNull()
            ?.takeIf { it.isJsonObject }
            ?.asJsonObject
            ?: return listOf(rawLine)

        val event = obj.getString("event") ?: return listOf(rawLine)
        val out = mutableListOf<String>()

        when (event) {
            "run" -> {
                val suite = suiteName(obj)
                val suiteLoc = suiteLocationHint(obj)
                ensureSuite(out, suite, suiteLoc)

                val name = testDisplayName(obj)
                activeTest = name
                val loc = testLocationHint(obj)
                if (loc != null) {
                    out.add("##teamcity[testStarted name='${escapeTc(name)}' captureStandardOutput='false' locationHint='${escapeTc(loc)}']")
                } else {
                    out.add("##teamcity[testStarted name='${escapeTc(name)}' captureStandardOutput='false']")
                }
            }

            "pass" -> {
                val name = activeTest ?: testDisplayName(obj)
                val testOut = obj.getString("output")
                if (!testOut.isNullOrEmpty()) {
                    out.add("##teamcity[testStdOut name='${escapeTc(name)}' out='${escapeTc(ensureTrailingNewline(testOut))}']")
                }
                val durUs = obj.getLong("durationUs") ?: 0L
                val durMs = (durUs / 1000L).coerceAtLeast(0L)
                out.add("##teamcity[testFinished name='${escapeTc(name)}' duration='$durMs']")
                activeTest = null
            }

            "fail" -> {
                val name = activeTest ?: testDisplayName(obj)
                val testOut = obj.getString("output")
                if (!testOut.isNullOrEmpty()) {
                    out.add("##teamcity[testStdOut name='${escapeTc(name)}' out='${escapeTc(ensureTrailingNewline(testOut))}']")
                }
                val fullMsg = obj.getString("message") ?: "Assertion failed"
                val shortMsg = fullMsg.lineSequence().firstOrNull()?.takeIf { it.isNotBlank() } ?: "Assertion failed"
                val expected = obj.getStringOrNull("expected")
                val actual = obj.getStringOrNull("actual")
                if (expected != null && actual != null) {
                    out.add(
                        "##teamcity[testFailed name='${escapeTc(name)}' message='${escapeTc(shortMsg)}' details='${escapeTc(fullMsg)}' type='comparisonFailure' expected='${escapeTc(expected)}' actual='${escapeTc(actual)}']"
                    )
                } else {
                    out.add(
                        "##teamcity[testFailed name='${escapeTc(name)}' message='${escapeTc(shortMsg)}' details='${escapeTc(fullMsg)}']"
                    )
                }
                out.add("##teamcity[testFinished name='${escapeTc(name)}']")
                activeTest = null
            }

            "build_error" -> {
                closeSuite(out)
                val pkg = obj.getString("package")?.takeIf { it.isNotBlank() }
                    ?: obj.getString("file")?.takeIf { it.isNotBlank() }
                    ?: "Lowlight Build"
                val file = obj.getString("file")
                val suiteLoc = if (!file.isNullOrBlank()) "lowlight://$file?line=1&col=1" else null
                ensureSuite(out, pkg, suiteLoc)
                val testName = "compile"
                val msg = obj.getString("message") ?: "Compilation failed"
                val locAttr = if (!file.isNullOrBlank()) " locationHint='${escapeTc("lowlight://$file?line=1&col=1")}'" else ""
                out.add("##teamcity[testStarted name='$testName' captureStandardOutput='false'$locAttr]")
                out.add("##teamcity[testFailed name='$testName' message='Compilation failed' details='${escapeTc(msg)}']")
                out.add("##teamcity[testFinished name='$testName']")
                closeSuite(out)
            }

            "summary" -> {
                closeSuite(out)
            }
        }
        return out
    }

    fun finish(): List<String> {
        val out = mutableListOf<String>()
        if (activeTest != null) {
            out.add("##teamcity[testFinished name='${escapeTc(activeTest!!)}']")
            activeTest = null
        }
        closeSuite(out)
        return out
    }

    private fun ensureSuite(out: MutableList<String>, suite: String, locationHint: String?) {
        if (openSuite == suite) return
        closeSuite(out)
        openSuite = suite
        if (locationHint != null) {
            out.add("##teamcity[testSuiteStarted name='${escapeTc(suite)}' locationHint='${escapeTc(locationHint)}']")
        } else {
            out.add("##teamcity[testSuiteStarted name='${escapeTc(suite)}']")
        }
    }

    private fun closeSuite(out: MutableList<String>) {
        val suite = openSuite ?: return
        out.add("##teamcity[testSuiteFinished name='${escapeTc(suite)}']")
        openSuite = null
    }

    private fun suiteName(obj: JsonObject): String =
        obj.getString("relFile")?.takeIf { it.isNotBlank() }
            ?: obj.getString("package")?.takeIf { it.isNotBlank() }
            ?: "Lowlight Tests"

    private fun suiteLocationHint(obj: JsonObject): String? {
        val file = obj.getString("file")?.takeIf { it.isNotBlank() } ?: return null
        return "lowlight://$file?line=1&col=1"
    }

    private fun testDisplayName(obj: JsonObject): String =
        obj.getString("desc")?.takeIf { it.isNotBlank() }
            ?: obj.getString("name")?.takeIf { it.isNotBlank() }
            ?: "test"

    private fun testLocationHint(obj: JsonObject): String? {
        val file = obj.getString("file")?.takeIf { it.isNotBlank() } ?: return null
        val line = (obj.getInt("line") ?: 1).coerceAtLeast(1)
        val col = (obj.getInt("col") ?: 1).coerceAtLeast(1)
        return "lowlight://$file?line=$line&col=$col"
    }

    private fun ensureTrailingNewline(s: String): String =
        if (s.endsWith("\n")) s else "$s\n"

    private fun JsonObject.getString(key: String): String? {
        val el = get(key) ?: return null
        return if (el.isJsonPrimitive && el.asJsonPrimitive.isString) el.asString else null
    }

    private fun JsonObject.getStringOrNull(key: String): String? {
        if (!has(key)) return null
        val el = get(key) ?: return null
        if (el.isJsonNull) return null
        return if (el.isJsonPrimitive && el.asJsonPrimitive.isString) el.asString else null
    }

    private fun JsonObject.getInt(key: String): Int? {
        val el = get(key) ?: return null
        return if (el.isJsonPrimitive && el.asJsonPrimitive.isNumber) runCatching { el.asInt }.getOrNull() else null
    }

    private fun JsonObject.getLong(key: String): Long? {
        val el = get(key) ?: return null
        return if (el.isJsonPrimitive && el.asJsonPrimitive.isNumber) runCatching { el.asLong }.getOrNull() else null
    }
}
