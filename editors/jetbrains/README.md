# Lowlight for JetBrains IDEs

Official JetBrains IDE plugin for **Lowlight** (`.ll`), compatible with WebStorm, IntelliJ IDEA (Ultimate & Community 2024.1+), CLion, GoLand, RustRover, PyCharm, and Android Studio.

## Features

- **Dual LSP Backend Support**:
  - Uses **JetBrains Native LSP** (`com.intellij.modules.lsp`) automatically when running in commercial / unified 2025+ JetBrains IDEs.
  - Uses **LSP4IJ** (`com.redhat.devtools.lsp4ij`) automatically when installed in Community Edition or Android Studio (and automatically disables duplicate attachment when Native LSP is active).
- **Full Lowlight LSP Capabilities**:
  - Real-time diagnostics across open & imported modules
  - Incremental document sync & live dependency invalidation
  - Semantic token highlighting (`readonly`, `declaration`, `documentation`, `defaultLibrary`)
  - Rendered Iteration B documentation with clickable ``[`Symbol`]`` & `[View source]` links
  - Context-aware completion, UFCS signature help, inlay hints (`types`, `parameters`)
  - Go-to-definition, type definition, declaration, implementation, cross-file find references, workspace symbols, folding ranges, selection ranges, and cross-file rename
  - Quick fixes, code generators (`Implement <Iface> for <Type>`, `<Type>.new`, doc skeletons, `@test`), and refactorings
  - Canonical Lowlight AST formatter (`lowlight fmt`) & tab-indented default code style (`\t`, 100-column right margin)
- **IDE Actions & Settings**:
  - `Tools | Lowlight` / Editor context menu: **Run Lowlight File**, **Run Lowlight Tests in File**, **Run All Lowlight Tests**, **Format with Lowlight**, **Restart Lowlight Language Server**
  - `Settings | Languages & Frameworks | Lowlight`: configure compiler binary, `LOWLIGHT_ROOT`, formatter style, inlay hints, and preferred LSP backend (`auto`, `native`, `lsp4ij`).

## Building & Testing

```bash
cd editors/jetbrains
./gradlew test buildPlugin
```

The installable plugin archive is written to `editors/jetbrains/build/distributions/lowlight-intellij-<version>.zip` (`Install Plugin from Disk...` in JetBrains IDEs).

## Quick Install (macOS / Linux)

```bash
./editors/jetbrains/install.sh
```

Then restart WebStorm or your JetBrains IDE. On 2024.1 IDEs (such as WebStorm 2024.1), install/keep the **LSP4IJ** plugin from JetBrains Marketplace so the plugin automatically provides full LSP features (`semanticTokens`, `inlayHints`, references, rename, formatting, code actions, CodeLens). On 2024.2+ / 2025+ / 2026+ JetBrains IDEs with built-in LSP support, Native LSP activates automatically (or uses LSP4IJ when Native LSP is unavailable or overridden in **Settings | Languages & Frameworks | Lowlight**).
