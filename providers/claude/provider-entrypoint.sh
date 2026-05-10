#!/usr/bin/env bash
set -euo pipefail

export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/var/lib/pangaea/auth/claude}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-claude}"
export PANGAEA_PROVIDER_MODE="${PANGAEA_PROVIDER_MODE:-cli-adapter}"
export PANGAEA_AUTH_REQUIRED="${PANGAEA_AUTH_REQUIRED:-true}"
if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
  export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${CLAUDE_CONFIG_DIR}/.credentials.json}"
fi
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-claude-credentials-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-anthropic}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-claude-default}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-claude-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-claude -p 'Reply with OK only.' --permission-mode plan --tools '' --output-format text}"
export PANGAEA_REFRESH_LOGIN_SHELL="${PANGAEA_REFRESH_LOGIN_SHELL:-true}"

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
  while [ ! -f "${PANGAEA_AUTH_PATH}" ]; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      echo "provider-entrypoint: auth file not found at ${PANGAEA_AUTH_PATH}" >&2
      exit 1
    fi
    sleep 1
  done
fi

provider_pid=""
shim_pid=""

cleanup() {
  if [ -n "${provider_pid}" ] && kill -0 "${provider_pid}" 2>/dev/null; then
    kill "${provider_pid}" 2>/dev/null || true
  fi
  if [ -n "${shim_pid}" ] && kill -0 "${shim_pid}" 2>/dev/null; then
    kill "${shim_pid}" 2>/dev/null || true
  fi
  wait 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 143' INT TERM

if [ "$#" -gt 0 ]; then
  "$@" &
  provider_pid="$!"
fi

/usr/local/bin/pangaeactl provider-shim run &
shim_pid="$!"

if [ -n "${provider_pid}" ]; then
  wait -n "${provider_pid}" "${shim_pid}"
  exit "$?"
fi

wait "${shim_pid}"
