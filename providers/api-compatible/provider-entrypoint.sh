#!/usr/bin/env bash
set -euo pipefail

export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-api-compatible}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-openai}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"

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
      printf 'PANGAEA_SERVICE=%q\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%q\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%q\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%q\n' "${PANGAEA_CONTAINER_ID:-}"
    } >"${settings}"
    chmod 0600 "${settings}" 2>/dev/null || true
  fi
}
write_runtime_settings

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
