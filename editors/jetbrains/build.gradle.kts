import org.jetbrains.intellij.platform.gradle.TestFrameworkType

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "2.1.21"
    id("org.jetbrains.intellij.platform") version "2.20.1"
}

group = providers.gradleProperty("pluginGroup").get()
version = providers.gradleProperty("pluginVersion").get()

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        intellijIdea(providers.gradleProperty("platformVersion").get())
        plugins("com.redhat.devtools.lsp4ij:${providers.gradleProperty("lsp4ijVersion").get()}")
        testFramework(TestFrameworkType.Platform)
    }

    testImplementation("junit:junit:4.13.2")
}

kotlin {
    jvmToolchain(21)
}

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
    }
}

tasks.withType<JavaCompile>().configureEach {
    sourceCompatibility = "17"
    targetCompatibility = "17"
}

tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
    compilerOptions.jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
}

val repoRoot = layout.projectDirectory.dir("../..").asFile
val bundledStdStage = layout.buildDirectory.dir("generated-assets/std")

val stageLowlightAssets = tasks.register<Copy>("stageLowlightAssets") {
    from(File(repoRoot, "std")) {
        include("**/*.ll")
    }
    into(bundledStdStage)
}

val buildLowlightBinary = tasks.register<Exec>("buildLowlightBinary") {
    workingDir = repoRoot
    val exeName = if (System.getProperty("os.name").lowercase().contains("win")) "lowlight.exe" else "lowlight"
    val outFile = layout.buildDirectory.file("generated-assets/bin/$exeName").get().asFile
    val goExe = listOf("/opt/homebrew/bin/go", "/usr/local/go/bin/go")
        .firstOrNull { File(it).canExecute() } ?: "go"
    outputs.file(outFile)
    doFirst {
        outFile.parentFile.mkdirs()
    }
    commandLine(goExe, "build", "-ldflags=-s -w", "-o", outFile.absolutePath, "./cmd/lowlight")
    isIgnoreExitValue = true
}

tasks {
    processResources {
        dependsOn(stageLowlightAssets, buildLowlightBinary)
        from(bundledStdStage) {
            into("lowlight-assets/std")
        }
        val binDir = layout.buildDirectory.dir("generated-assets/bin")
        from(binDir) {
            into("lowlight-assets/bin")
        }
    }

    prepareSandbox {
        dependsOn(stageLowlightAssets, buildLowlightBinary)
        from(bundledStdStage) {
            into("${project.name}/std")
        }
        from(layout.buildDirectory.dir("generated-assets/bin")) {
            into("${project.name}/bin")
            filePermissions { unix("rwxr-xr-x") }
        }
    }

    buildPlugin {
        dependsOn(prepareSandbox)
    }
}

intellijPlatform {
    pluginConfiguration {
        name = providers.gradleProperty("pluginName")
        version = providers.gradleProperty("pluginVersion")
        description = """
            Official Lowlight language support for JetBrains IDEs.
            Powered by the native Lowlight Language Server (`lowlight lsp --stdio`) with
            automatic dual support for JetBrains Native LSP (Ultimate / Pro / 2025+) and
            LSP4IJ (Community Edition / Android Studio).
        """.trimIndent()
        ideaVersion {
            sinceBuild = "241"
            untilBuild = provider { null }
        }
    }

    buildSearchableOptions = false
}
