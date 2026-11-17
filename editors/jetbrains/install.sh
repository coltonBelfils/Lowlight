#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="$(grep '^pluginVersion=' "${SCRIPT_DIR}/gradle.properties" | cut -d= -f2- | tr -d '[:space:]')"
ZIP_FILE="${SCRIPT_DIR}/build/distributions/lowlight-intellij-${VERSION}.zip"

if [ ! -f "${ZIP_FILE}" ] || [ "${1:-}" = "--build" ]; then
  echo "Building Lowlight JetBrains plugin (v${VERSION})..."
  "${SCRIPT_DIR}/gradlew" -p "${SCRIPT_DIR}" --no-daemon buildPlugin
fi

if [ ! -f "${ZIP_FILE}" ]; then
  echo "error: plugin archive not found at ${ZIP_FILE}" >&2
  exit 1
fi

JB_CONFIG_ROOT=""
case "$(uname -s)" in
  Darwin)
    JB_CONFIG_ROOT="${HOME}/Library/Application Support/JetBrains"
    ;;
  Linux)
    JB_CONFIG_ROOT="${XDG_DATA_HOME:-${HOME}/.local/share}/JetBrains"
    ;;
  *)
    echo "Install ${ZIP_FILE} via JetBrains IDE: Settings -> Plugins -> Gear -> Install Plugin from Disk..."
    exit 0
    ;;
esac

if [ ! -d "${JB_CONFIG_ROOT}" ]; then
  echo "No JetBrains config directory found at ${JB_CONFIG_ROOT}."
  echo "Install manually via Settings -> Plugins -> Install Plugin from Disk...: ${ZIP_FILE}"
  exit 0
fi

INSTALLED_COUNT=0
for IDE_DIR in "${JB_CONFIG_ROOT}"/*; do
  [ -d "${IDE_DIR}" ] || continue
  BASE="$(basename "${IDE_DIR}")"
  case "${BASE}" in
    WebStorm*|IntelliJIdea*|IdeaIC*|CLion*|GoLand*|PyCharm*|RustRover*|PhpStorm*|RubyMine*|Rider*|DataGrip*|DataSpell*)
      ;;
    *)
      continue
      ;;
  esac
  # Ensure it's an actual IDE profile directory (has options/ or plugins/), not an empty marker dir
  if [ ! -d "${IDE_DIR}/options" ] && [ ! -d "${IDE_DIR}/plugins" ]; then
    continue
  fi
  PLUGINS_DIR="${IDE_DIR}/plugins"
  mkdir -p "${PLUGINS_DIR}"
  rm -rf "${PLUGINS_DIR}/lowlight-intellij"
  unzip -q -o "${ZIP_FILE}" -d "${PLUGINS_DIR}"
  chmod 0755 "${PLUGINS_DIR}/lowlight-intellij/bin/lowlight" 2>/dev/null || true
  echo "Installed Lowlight v${VERSION} into ${PLUGINS_DIR}/lowlight-intellij"
  INSTALLED_COUNT=$((INSTALLED_COUNT + 1))
done

if [ "${INSTALLED_COUNT}" -eq 0 ]; then
  echo "No active JetBrains IDE profile directories found under ${JB_CONFIG_ROOT}."
  echo "Install manually via Settings -> Plugins -> Install Plugin from Disk...: ${ZIP_FILE}"
else
  echo ""
  echo "Restart WebStorm / your JetBrains IDE to activate Lowlight v${VERSION}."
fi
