#!/usr/bin/env bash
set -euo pipefail

export HOME="${HOME:-/var/lib/pangaea/home/gemini}"
export GEMINI_CLI_TRUST_WORKSPACE="${GEMINI_CLI_TRUST_WORKSPACE:-true}"
if [ -z "${TERM:-}" ] || [ "${TERM:-}" = "dumb" ]; then
  export TERM="xterm-256color"
fi
export COLORTERM="${COLORTERM:-truecolor}"
export FORCE_COLOR="${FORCE_COLOR:-1}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-gemini}"
export PANGAEA_PROVIDER_MODE="${PANGAEA_PROVIDER_MODE:-http-direct}"
export PANGAEA_AUTH_REQUIRED="${PANGAEA_AUTH_REQUIRED:-true}"
if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
  export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${HOME}/.gemini/oauth_creds.json}"
fi
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-gemini-oauth-creds-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-gemini}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-gemini-2.5-flash}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-gemini-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-gemini -p 'Reply with OK only.' --skip-trust --approval-mode plan --output-format json --model gemini-2.5-flash}"
export PANGAEA_REFRESH_LOGIN_SHELL="${PANGAEA_REFRESH_LOGIN_SHELL:-true}"

mkdir -p "${HOME}/.gemini"
if [ ! -s "${HOME}/.gemini/settings.json" ]; then
  printf '%s\n' '{"selectedAuthType":"oauth-personal","security":{"auth":{"selectedType":"oauth-personal"}}}' >"${HOME}/.gemini/settings.json"
  chmod 0600 "${HOME}/.gemini/settings.json" 2>/dev/null || true
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
