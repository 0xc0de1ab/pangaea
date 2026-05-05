#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -gt 0 ]; then
  exec "$@"
fi

export CODEX_HOME="${CODEX_HOME:-/var/lib/pangaea/auth/codex}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-codex}"
export PANGAEA_AUTH_PATH="${PANGAEA_AUTH_PATH:-${CODEX_HOME}/auth.json}"
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-codex-auth-json-format}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-gpt-5-codex}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-codex-default}"
export PANGAEA_REFRESH_COMMAND="${PANGAEA_REFRESH_COMMAND:-codex exec --skip-git-repo-check --sandbox read-only --ephemeral --ignore-user-config --color never 'Reply with OK only.'}"
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
