#!/usr/bin/env bash
set -euo pipefail

export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-/var/lib/pangaea/auth/claude}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-claude}"
export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${CLAUDE_CONFIG_DIR}/.credentials.json}"
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-claude-credentials-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-anthropic}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-claude-default}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-claude-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-claude -p 'Reply with OK only.' --permission-mode plan --tools '' --output-format text}"
export PANGAEA_REFRESH_LOGIN_SHELL="${PANGAEA_REFRESH_LOGIN_SHELL:-true}"

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
