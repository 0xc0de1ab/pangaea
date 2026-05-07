#!/usr/bin/env bash
set -euo pipefail

export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-api-compatible}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-openai}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"

if [ -n "${PANGAEA_UPSTREAM_API_KEY_FILE:-}" ]; then
  api_key_wait_timeout="${PANGAEA_API_KEY_WAIT_TIMEOUT:-30}"
  case "${api_key_wait_timeout}" in
    ''|*[!0-9]*) api_key_wait_timeout=30 ;;
  esac
  deadline=$((SECONDS + api_key_wait_timeout))
  while [ ! -s "${PANGAEA_UPSTREAM_API_KEY_FILE}" ]; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      echo "provider-entrypoint: api key file not found at ${PANGAEA_UPSTREAM_API_KEY_FILE}" >&2
      exit 1
    fi
    sleep 1
  done
fi

exec /usr/local/bin/pangaeactl provider-shim run "$@"
