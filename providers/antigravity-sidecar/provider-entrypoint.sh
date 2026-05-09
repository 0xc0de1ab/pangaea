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

if [ -n "${PANGAEA_HOST_HOSTNAME:-${PANGAEA_NODE_HOST_NAME:-}}" ] && { [ -z "${PANGAEA_HOST_NAME:-}" ] || [ "${PANGAEA_HOST_NAME:-}" = "${HOSTNAME:-}" ]; }; then
  export PANGAEA_HOST_NAME="${PANGAEA_HOST_HOSTNAME:-${PANGAEA_NODE_HOST_NAME:-}}"
fi

detect_container_kind() {
  if [ -f /var/run/secrets/kubernetes.io/serviceaccount/token ]; then
    printf '%s\n' "kubernetes"
    return
  fi
  if [ -f /.dockerenv ]; then
    printf '%s\n' "docker"
    return
  fi
  if grep -qaE 'kubepods|containerd|docker' /proc/1/cgroup 2>/dev/null; then
    printf '%s\n' "container"
  fi
}

detect_container_id() {
  awk -F/ '
    /docker|containerd|kubepods/ {
      value=$NF
      sub(/\.scope$/, "", value)
      sub(/^docker-/, "", value)
      sub(/^cri-containerd-/, "", value)
      if (length(value) >= 12) {
        print value
        exit
      }
    }
  ' /proc/self/cgroup 2>/dev/null || true
}

export PANGAEA_CONTAINER_KIND="${PANGAEA_CONTAINER_KIND:-$(detect_container_kind)}"
export PANGAEA_CONTAINER_NAME="${PANGAEA_CONTAINER_NAME:-${PANGAEA_POD_CONTAINER_NAME:-}}"
if [ -z "${PANGAEA_CONTAINER_NAME}" ] && [ "${PANGAEA_CONTAINER_KIND}" != "kubernetes" ]; then
  export PANGAEA_CONTAINER_NAME="${HOSTNAME:-}"
fi
export PANGAEA_CONTAINER_ID="${PANGAEA_CONTAINER_ID:-$(detect_container_id)}"
if [ -z "${PANGAEA_CONTAINER_ID}" ] && [ "${PANGAEA_CONTAINER_KIND}" = "docker" ]; then
  export PANGAEA_CONTAINER_ID="${HOSTNAME:-}"
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
      printf 'PANGAEA_SERVICE=%q\n' "${PANGAEA_SERVICE:-}"
      printf 'PANGAEA_CONTAINER_KIND=%q\n' "${PANGAEA_CONTAINER_KIND:-}"
      printf 'PANGAEA_CONTAINER_NAME=%q\n' "${PANGAEA_CONTAINER_NAME:-}"
      printf 'PANGAEA_CONTAINER_ID=%q\n' "${PANGAEA_CONTAINER_ID:-}"
    } >"${settings}"
    chmod 0600 "${settings}" 2>/dev/null || true
  fi
}
write_runtime_settings

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
