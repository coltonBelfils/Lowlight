#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
python3 "${SCRIPT_DIR}/package_vsix.py"

EXT_VERSION="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "${SCRIPT_DIR}/lowlight/package.json")"
EXT_ID="lowlight-lang.lowlight-${EXT_VERSION}"
SRC_DIR="${SCRIPT_DIR}/lowlight"
VSIX_FILE="${SCRIPT_DIR}/lowlight-vscode-${EXT_VERSION}.vsix"

INSTALLED=0

# 1. Prefer official VS Code / Cursor CLI `--install-extension` when available
CODE_CANDIDATES=(
  "$(command -v code 2>/dev/null || true)"
  "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
  "$HOME/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
)
for CODE_BIN in "${CODE_CANDIDATES[@]}"; do
  if [ -n "${CODE_BIN}" ] && [ -x "${CODE_BIN}" ]; then
    echo "Installing into VS Code via: ${CODE_BIN}"
    if "${CODE_BIN}" --install-extension "${VSIX_FILE}" --force; then
      INSTALLED=1
      break
    fi
  fi
done

CURSOR_CANDIDATES=(
  "$(command -v cursor 2>/dev/null || true)"
  "/Applications/Cursor.app/Contents/Resources/app/bin/cursor"
  "$HOME/Applications/Cursor.app/Contents/Resources/app/bin/cursor"
)
for CURSOR_BIN in "${CURSOR_CANDIDATES[@]}"; do
  if [ -n "${CURSOR_BIN}" ] && [ -x "${CURSOR_BIN}" ]; then
    echo "Installing into Cursor via: ${CURSOR_BIN}"
    if "${CURSOR_BIN}" --install-extension "${VSIX_FILE}" --force; then
      INSTALLED=1
      break
    fi
  fi
done

# 2. Fallback: copy unpacked extension directly into ~/.vscode/extensions (and register in extensions.json if present)
if [ "${INSTALLED}" -eq 0 ]; then
  DEST_ROOT="${HOME}/.vscode/extensions"
  DEST="${DEST_ROOT}/${EXT_ID}"
  mkdir -p "${DEST_ROOT}"
  rm -rf "${DEST_ROOT}"/lowlight-lang.lowlight-* "${DEST}"
  cp -R "${SRC_DIR}" "${DEST}"
  chmod +x "${DEST}/bin/lowlight" 2>/dev/null || true

  python3 - "${DEST_ROOT}" "${EXT_ID}" "${EXT_VERSION}" <<'PY'
import json, sys, time
from pathlib import Path

ext_root = Path(sys.argv[1])
ext_id = sys.argv[2]
ext_ver = sys.argv[3]
manifest_file = ext_root / "extensions.json"
if manifest_file.is_file():
    try:
        items = json.loads(manifest_file.read_text(encoding="utf-8"))
        if isinstance(items, list):
            items = [e for e in items if e.get("identifier", {}).get("id", "").lower() != "lowlight-lang.lowlight"]
            items.append({
                "identifier": {"id": "lowlight-lang.lowlight"},
                "version": ext_ver,
                "location": {
                    "$mid": 1,
                    "fsPath": str((ext_root / ext_id).resolve()),
                    "external": (ext_root / ext_id).resolve().as_uri(),
                    "path": str((ext_root / ext_id).resolve()),
                    "scheme": "file",
                },
                "relativeLocation": ext_id,
                "metadata": {
                    "installedTimestamp": int(time.time() * 1000),
                    "source": "vsix",
                },
            })
            manifest_file.write_text(json.dumps(items, indent=2), encoding="utf-8")
    except Exception as exc:
        print(f"warning: could not update extensions.json: {exc}", file=sys.stderr)
PY
  echo "Installed unpacked extension to: ${DEST}"
fi

echo ""
echo "Lowlight VS Code extension (v${EXT_VERSION}) installed!"
echo "Reload VS Code ('Cmd+Shift+P' -> 'Developer: Reload Window') and open any .ll file."
