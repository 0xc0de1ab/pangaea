#!/usr/bin/env bash
set -euo pipefail

export ANTIGRAVITY_SERVER_DIR="${ANTIGRAVITY_SERVER_DIR:-/opt/antigravity-server}"
export SERVER_PATH="${SERVER_PATH:-${ANTIGRAVITY_SERVER_DIR}/out/server-main.js}"
case "$(uname -m)" in
  aarch64|arm64) antigravity_arch_suffix="arm64" ;;
  *) antigravity_arch_suffix="x64" ;;
esac
default_core_path="${ANTIGRAVITY_SERVER_DIR}/extensions/antigravity/bin/language_server_linux_${antigravity_arch_suffix}"
export CORE_PATH="${CORE_PATH:-${default_core_path}}"
if [ ! -x "${CORE_PATH}" ] && [ -x "${default_core_path}" ]; then
  export CORE_PATH="${default_core_path}"
fi
export INSTALL_DIR="${INSTALL_DIR:-${ANTIGRAVITY_SERVER_DIR}}"
export ANTIGRAVITY_GEMINI_DIR="${ANTIGRAVITY_GEMINI_DIR:-/root/.antigravity-server}"
export ANTIGRAVITY_APP_DATA_DIR="${ANTIGRAVITY_APP_DATA_DIR:-data}"
export STATE_VSCDB_PATH="${STATE_VSCDB_PATH:-/var/lib/antigravity/state/User/globalStorage/state.vscdb}"
export VSCDB_PATH="${VSCDB_PATH:-${STATE_VSCDB_PATH}}"
export PATH="${ANTIGRAVITY_SERVER_DIR}:${PATH}"

mkdir -p "$(dirname "${STATE_VSCDB_PATH}")" "${ANTIGRAVITY_GEMINI_DIR}/${ANTIGRAVITY_APP_DATA_DIR}/User/globalStorage"

if [ "$#" -eq 0 ]; then
  set -- serve --proxy-addr 0.0.0.0:8080 --db-path "${STATE_VSCDB_PATH}"
elif [ "${1#-}" != "$1" ]; then
  set -- serve "$@"
fi

exec antigravity-compat-proxy "$@"
