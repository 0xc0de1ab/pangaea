#!/usr/bin/env bash
set -euo pipefail

export CODEX_HOME="${CODEX_HOME:-/var/lib/pangaea/auth/codex}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-codex}"
export PANGAEA_AUTH_REQUIRED="${PANGAEA_AUTH_REQUIRED:-true}"
if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
  export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${CODEX_HOME}/auth.json}"
fi
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-codex-auth-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-gpt-5.5}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-codex-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-codex exec --skip-git-repo-check --sandbox read-only --ephemeral --ignore-user-config --color never 'Reply with OK only.'}"
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
      printf 'PANGAEA_PROVIDER_ID=%q\n' "${PANGAEA_PROVIDER_ID:-}"
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

wait_for_app_server_ready() {
  local raw="${PANGAEA_UPSTREAM_BASE_URL:-}"
  local ready_url=""
  case "${raw}" in
    ws://*) ready_url="http://${raw#ws://}/readyz" ;;
    wss://*) ready_url="https://${raw#wss://}/readyz" ;;
    *) return 0 ;;
  esac

  local timeout="${PANGAEA_UPSTREAM_READY_TIMEOUT:-30}"
  case "${timeout}" in
    ''|*[!0-9]*) timeout=30 ;;
  esac
  local ready_deadline=$((SECONDS + timeout))
  while true; do
    if command -v wget >/dev/null 2>&1; then
      wget -q -O /dev/null "${ready_url}" >/dev/null 2>&1 && return 0
    else
      local hostport="${ready_url#http://}"
      hostport="${hostport#https://}"
      hostport="${hostport%%/*}"
      local host="${hostport%:*}"
      local port="${hostport##*:}"
      { true >/dev/tcp/"${host}"/"${port}"; } >/dev/null 2>&1 && return 0
    fi
    if [ "${SECONDS}" -ge "${ready_deadline}" ]; then
      echo "provider-entrypoint: app server not ready at ${ready_url}" >&2
      return 1
    fi
    sleep 1
  done
}

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
  wait_for_app_server_ready
fi

/usr/local/bin/pangaeactl provider-shim run &
shim_pid="$!"

if [ -n "${provider_pid}" ]; then
  wait -n "${provider_pid}" "${shim_pid}"
  exit "$?"
fi

wait "${shim_pid}"
