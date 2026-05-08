#!/usr/bin/env bash
set -euo pipefail

export HOME="${HOME:-/var/lib/pangaea/home/antigravity}"
export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-sidecar-agent}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-antigravity}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_UPSTREAM_BASE_URL="${PANGAEA_UPSTREAM_BASE_URL:-http://127.0.0.1:8080}"
export PANGAEA_SHIM_PROTOCOLS="${PANGAEA_SHIM_PROTOCOLS:-openai,anthropic,gemini}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-antigravity-default}"
export PANGAEA_MODEL_ALIAS="${PANGAEA_MODEL_ALIAS:-antigravity-default}"
export OPENAI_API_KEY="${OPENAI_API_KEY:-${PANGAEA_UPSTREAM_API_KEY:-pangaea-antigravity-openai}}"
export ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-pangaea-antigravity-anthropic}"
export GOOGLE_API_KEY="${GOOGLE_API_KEY:-pangaea-antigravity-gemini}"
export PANGAEA_UPSTREAM_API_KEY="${PANGAEA_UPSTREAM_API_KEY:-${OPENAI_API_KEY}}"

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
