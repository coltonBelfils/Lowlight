# Lowlight VS Code Extension (Drop-In Setup)

This directory contains a **zero-dependency, self-contained VS Code (and Cursor / VSCodium) extension** for Lowlight, backed by `lowlight lsp --stdio`. No `npm` or `node_modules` installation is required.

## Drop-In Install (Pick Any Option)

### Option 1: Install the Pre-Built `.vsix` Directly in VS Code (3 Clicks)
A pre-packaged `.vsix` (bundling the `lowlight` binary + `std/` library) is ready at:
```text
editors/vscode/lowlight-vscode-1.0.0.vsix
```
1. Open VS Code (or Cursor).
2. Press **`Cmd+Shift+P`** (macOS) or **`Ctrl+Shift+P`** (Linux/Windows).
3. Run **`Extensions: Install from VSIX...`** and select `editors/vscode/lowlight-vscode-1.0.0.vsix`.

*(Or from the terminal if you have the `code` CLI enabled: `code --install-extension editors/vscode/lowlight-vscode-1.0.0.vsix`)*

### Option 2: One-Command Drop-In Installer Script
From the repository root, run:
```sh
./editors/vscode/install.sh
```
This rebuilds `lowlight`, bundles the binary and `std/` modules, updates `editors/vscode/lowlight-vscode-1.0.0.vsix`, and copies the extension straight into `~/.vscode/extensions/lowlight-lang.lowlight-1.0.0` (and `~/.cursor/extensions/...` if Cursor is installed). Reload your editor window (`Developer: Reload Window`) and open any `.ll` file!

### Option 3: Manual Drag/Copy Drop-In
Copy `editors/vscode/lowlight` directly into your VS Code extensions folder:
```sh
cp -R editors/vscode/lowlight ~/.vscode/extensions/lowlight-lang.lowlight-1.0.0
```

---

## Rebuilding the `.vsix` Package

After making compiler or LSP changes, regenerate the bundled binary + `.vsix` with:
```sh
python3 editors/vscode/package_vsix.py
```
