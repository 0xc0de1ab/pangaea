#!/usr/bin/env bash
set -euo pipefail

export HOME="${HOME:-/var/lib/pangaea/home/copilot}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-sidecar-agent}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-github-copilot}"
export PANGAEA_PROVIDER_MODE="${PANGAEA_PROVIDER_MODE:-sdk}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_UPSTREAM_BASE_URL="${PANGAEA_UPSTREAM_BASE_URL:-http://127.0.0.1:4141}"
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-github-copilot-config-json-format}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-github-copilot-default}"
export PANGAEA_MODELS="${PANGAEA_MODELS:-${PANGAEA_MODEL}}"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
export PANGAEA_AUTH_REQUIRED="${PANGAEA_AUTH_REQUIRED:-true}"
if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
  export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${HOME}/.copilot/config.json}"
  auth_dir="$(dirname "${PANGAEA_AUTH_PATH}")"
  mkdir -p "${auth_dir}" 2>/dev/null || true
  chmod 0700 "${auth_dir}" 2>/dev/null || true
  if [ -e "${PANGAEA_AUTH_PATH}" ]; then
    chmod 0600 "${PANGAEA_AUTH_PATH}" 2>/dev/null || true
  fi
fi

write_runtime_settings() {
  local settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
  local dir
  dir="$(dirname "${settings}")"
  mkdir -p "${dir}" 2>/dev/null || return 0
  if [ ! -s "${settings}" ]; then
    {
      printf 'PANGAEA_NODE_ID=%q\n' "${PANGAEA_NODE_ID:-}"
      printf 'PANGAEA_HOST_NAME=%q\n' "${PANGAEA_HOST_NAME:-}"
      printf 'PANGAEA_PROVIDER_TYPE=%q\n' "${PANGAEA_PROVIDER_TYPE:-}"
      printf 'PANGAEA_PROVIDER_INSTANCE_ID=%q\n' "${PANGAEA_PROVIDER_INSTANCE_ID:-}"
      printf 'PANGAEA_PROVIDER_MODE=%q\n' "${PANGAEA_PROVIDER_MODE:-}"
      printf 'PANGAEA_SERVICE=%q\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%q\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%q\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%q\n' "${PANGAEA_CONTAINER_ID:-}"
    } >"${settings}"
    chmod 0600 "${settings}" 2>/dev/null || true
  fi
}
write_runtime_settings

if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
  auth_wait_timeout="${PANGAEA_AUTH_WAIT_TIMEOUT:-30}"
  case "${auth_wait_timeout}" in
    ''|*[!0-9]*) auth_wait_timeout=30 ;;
  esac
  deadline=$((SECONDS + auth_wait_timeout))
  while [ ! -s "${PANGAEA_AUTH_PATH}" ]; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      echo "provider-entrypoint: auth file not found at ${PANGAEA_AUTH_PATH}" >&2
      exit 1
    fi
    sleep 1
  done
fi

sidecar_pid=""
shim_pid=""

cleanup() {
  if [ -n "${sidecar_pid}" ] && kill -0 "${sidecar_pid}" 2>/dev/null; then
    kill "${sidecar_pid}" 2>/dev/null || true
  fi
  if [ -n "${shim_pid}" ] && kill -0 "${shim_pid}" 2>/dev/null; then
    kill "${shim_pid}" 2>/dev/null || true
  fi
  wait 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 143' INT TERM

if [ "$#" -eq 0 ] && [ "${PANGAEA_PROVIDER_MODE}" = "sdk" ]; then
  set -- /usr/local/bin/copilot-relay --listen "${COPILOT_RELAY_LISTEN:-127.0.0.1:4141}"
fi

if [ "$#" -gt 0 ]; then
  "$@" &
  sidecar_pid="$!"
fi

/usr/local/bin/pangaeactl provider-shim run &
shim_pid="$!"

if [ -n "${sidecar_pid}" ]; then
  wait -n "${sidecar_pid}" "${shim_pid}"
  exit "$?"
fi

wait "${shim_pid}"
