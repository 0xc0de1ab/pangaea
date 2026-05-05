#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -gt 0 ]; then
  exec "$@"
fi

export HOME="${HOME:-/var/lib/pangaea/home/gemini}"
export GEMINI_CLI_TRUST_WORKSPACE="${GEMINI_CLI_TRUST_WORKSPACE:-true}"
export TERM="${TERM:-xterm-256color}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-gemini}"
export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${HOME}/.gemini/oauth_creds.json}"
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-gemini-oauth-creds-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-gemini}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-gemini-2.5-flash}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-gemini-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-gemini -p 'Reply with OK only.' --skip-trust --approval-mode plan --output-format json --model gemini-2.5-flash}"
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

exec /usr/local/bin/pangaeactl provider-shim run
