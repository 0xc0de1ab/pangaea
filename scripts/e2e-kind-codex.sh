#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster="${PANGAEA_KIND_CLUSTER:-pangaea-e2e}"
namespace="${PANGAEA_KIND_NAMESPACE:-pangaea-e2e}"
router_image="${PANGAEA_ROUTER_IMAGE:-pangaea/router:kind}"
codex_image="${PANGAEA_CODEX_IMAGE:-pangaea/provider-codex:kind}"
codex_npm_version="${PANGAEA_CODEX_NPM_VERSION:-latest}"
router_port="${PANGAEA_ROUTER_PORT:-18080}"
router_api_key="${PANGAEA_ROUTER_API_KEY:-kind-router-key}"
router_peer_token="${PANGAEA_ROUTER_PEER_TOKEN:-kind-peer-token}"
stream_token_key="${PANGAEA_STREAM_TOKEN_KEY:-kind-stream-token-key}"
provider_id="${PANGAEA_PROVIDER_ID:-codex-cli}"
provider_instance_id="${PANGAEA_PROVIDER_INSTANCE_ID:-codex-cli-kind}"
codex_model="${PANGAEA_CODEX_MODEL:-gpt-5.5}"
node_id="${PANGAEA_NODE_ID:-kind-host}"
host_name="${PANGAEA_HOST_NAME:-$(hostname -s 2>/dev/null || hostname)}"
account_hint="${PANGAEA_ACCOUNT_HINT:-}"
work_dir="${PANGAEA_E2E_WORKDIR:-${repo_root}/.tmp/kind-codex-e2e}"
keep="${PANGAEA_E2E_KEEP:-1}"
require_route="${PANGAEA_E2E_REQUIRE_ROUTE:-0}"
invoke="${PANGAEA_E2E_INVOKE:-0}"
success=0
port_forward_pid=""
node_agent_pid=""

require_tool() {
  local tool="$1"
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
}

truthy() {
  case "${1:-}" in
    1|t|T|true|TRUE|yes|YES|y|Y|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

cleanup() {
  if [ "${success}" != "1" ] || ! truthy "${keep}"; then
    if [ -n "${port_forward_pid}" ] && kill -0 "${port_forward_pid}" 2>/dev/null; then
      kill "${port_forward_pid}" 2>/dev/null || true
      wait "${port_forward_pid}" 2>/dev/null || true
    fi
    if [ -n "${node_agent_pid}" ] && kill -0 "${node_agent_pid}" 2>/dev/null; then
      kill "${node_agent_pid}" 2>/dev/null || true
      wait "${node_agent_pid}" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT

resolve_codex_auth_path() {
  if [ -n "${PANGAEA_CODEX_AUTH_PATH:-}" ]; then
    if [ -s "${PANGAEA_CODEX_AUTH_PATH}" ] && [ -f "${PANGAEA_CODEX_AUTH_PATH}" ]; then
      printf '%s\n' "${PANGAEA_CODEX_AUTH_PATH}"
      return 0
    fi
    echo "PANGAEA_CODEX_AUTH_PATH does not point to a non-empty file: ${PANGAEA_CODEX_AUTH_PATH}" >&2
    return 1
  fi
  local assets_auth="${repo_root}/assets/.codex/auth.json"
  if [ -s "${assets_auth}" ] && [ -f "${assets_auth}" ]; then
    printf '%s\n' "${assets_auth}"
    return 0
  fi
  local home_auth="${HOME:-}/.codex/auth.json"
  if [ -n "${HOME:-}" ] && [ -s "${home_auth}" ] && [ -f "${home_auth}" ]; then
    printf '%s\n' "${home_auth}"
    return 0
  fi
  echo "codex auth not found; checked ${assets_auth} then ${home_auth}" >&2
  return 1
}

docker_bridge_gateway() {
  docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null | head -n 1
}

kubectl_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    *) echo "unsupported kubectl architecture: $(uname -m)" >&2; return 1 ;;
  esac
}

ensure_kubectl() {
  if command -v kubectl >/dev/null 2>&1; then
    command -v kubectl
    return 0
  fi
  local target="${work_dir}/kubectl"
  if [ -x "${target}" ]; then
    printf '%s\n' "${target}"
    return 0
  fi
  local version="${PANGAEA_KUBECTL_VERSION:-}"
  if [ -z "${version}" ]; then
    version="$(docker exec "${cluster}-control-plane" kubeadm version -o short 2>/dev/null | sed 's/+.*//')"
  fi
  if [ -z "${version}" ]; then
    echo "could not determine Kubernetes version for kubectl download" >&2
    return 1
  fi
  local os_name
  os_name="$(uname | tr '[:upper:]' '[:lower:]')"
  local arch
  arch="$(kubectl_arch)"
  echo "kubectl not found; downloading ${version} for ${os_name}/${arch} into ${target}" >&2
  curl -fsSL -o "${target}" "https://dl.k8s.io/release/${version}/bin/${os_name}/${arch}/kubectl"
  chmod 0755 "${target}"
  printf '%s\n' "${target}"
}

wait_for_http() {
  local url="$1"
  local header_args=("${@:2}")
  local deadline=$((SECONDS + 90))
  until curl -fsS "${header_args[@]}" "${url}" >/dev/null 2>&1; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      echo "timed out waiting for ${url}" >&2
      return 1
    fi
    sleep 1
  done
}

wait_for_provider_websockets() {
  local url="$1"
  local deadline=$((SECONDS + 120))
  local body=""
  while true; do
    body="$(curl -fsS -H "authorization: Bearer ${router_api_key}" "${url}" 2>/dev/null || true)"
    if printf '%s' "${body}" | grep -q "\"provider_instance_id\":\"${provider_instance_id}\"" \
      && printf '%s' "${body}" | grep -q '"control_session_active":true' \
      && printf '%s' "${body}" | grep -q '"data_session_active":true'; then
      printf '%s\n' "${body}" >"${work_dir}/dashboard-providers.json"
      return 0
    fi
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      printf '%s\n' "${body}" >"${work_dir}/dashboard-providers.last.json"
      echo "timed out waiting for provider control/data websockets; last response saved to ${work_dir}/dashboard-providers.last.json" >&2
      return 1
    fi
    sleep 2
  done
}

require_tool docker
require_tool kind
require_tool curl
require_tool go

mkdir -p "${work_dir}"
auth_path="$(resolve_codex_auth_path)"

if [ "${PANGAEA_E2E_SKIP_BUILD:-0}" != "1" ]; then
  docker build -f "${repo_root}/deploy/kind/router.Dockerfile" -t "${router_image}" --build-arg VERSION=kind "${repo_root}"
  docker build -f "${repo_root}/providers/codex/Dockerfile" -t "${codex_image}" --build-arg VERSION=kind --build-arg CODEX_NPM_VERSION="${codex_npm_version}" "${repo_root}"
fi

if ! kind get clusters | grep -qx "${cluster}"; then
  kind create cluster --name "${cluster}" --config "${repo_root}/deploy/kind/kind-config.yaml"
fi
kubectl_bin="$(ensure_kubectl)"

kind load docker-image --name "${cluster}" "${router_image}"
kind load docker-image --name "${cluster}" "${codex_image}"

"${kubectl_bin}" create namespace "${namespace}" --dry-run=client -o yaml | "${kubectl_bin}" apply -f -
"${kubectl_bin}" -n "${namespace}" create configmap pangaea-router-policy \
  --from-file=router-policy.yaml="${repo_root}/deploy/kind/router-policy.yaml" \
  --dry-run=client -o yaml | "${kubectl_bin}" apply -f -
"${kubectl_bin}" -n "${namespace}" create secret generic pangaea-router-secrets \
  --from-literal=api-key="${router_api_key}" \
  --from-literal=peer-token="${router_peer_token}" \
  --from-literal=stream-token-key="${stream_token_key}" \
  --dry-run=client -o yaml | "${kubectl_bin}" apply -f -
sed "s/namespace: pangaea-e2e/namespace: ${namespace}/g; s/name: pangaea-e2e/name: ${namespace}/g" \
  "${repo_root}/deploy/kind/router.yaml" >"${work_dir}/router.rendered.yaml"
"${kubectl_bin}" apply -f "${work_dir}/router.rendered.yaml"
"${kubectl_bin}" -n "${namespace}" set image deployment/pangaea-router router="${router_image}"
"${kubectl_bin}" -n "${namespace}" rollout restart deployment/pangaea-router
"${kubectl_bin}" -n "${namespace}" rollout status deployment/pangaea-router --timeout=180s

pid_file="${work_dir}/port-forward.pid"
if [ -f "${pid_file}" ] && kill -0 "$(cat "${pid_file}")" 2>/dev/null; then
  kill "$(cat "${pid_file}")" 2>/dev/null || true
fi
if truthy "${keep}"; then
  if command -v setsid >/dev/null 2>&1; then
    setsid "${kubectl_bin}" -n "${namespace}" port-forward --address 0.0.0.0 service/pangaea-router "${router_port}:8080" </dev/null >"${work_dir}/port-forward.log" 2>&1 &
  else
    nohup "${kubectl_bin}" -n "${namespace}" port-forward --address 0.0.0.0 service/pangaea-router "${router_port}:8080" </dev/null >"${work_dir}/port-forward.log" 2>&1 &
  fi
else
  "${kubectl_bin}" -n "${namespace}" port-forward --address 0.0.0.0 service/pangaea-router "${router_port}:8080" >"${work_dir}/port-forward.log" 2>&1 &
fi
port_forward_pid="$!"
printf '%s\n' "${port_forward_pid}" >"${pid_file}"
wait_for_http "http://127.0.0.1:${router_port}/healthz"

provider_router_host="${PANGAEA_PROVIDER_ROUTER_HOST:-$(docker_bridge_gateway)}"
if [ -z "${provider_router_host}" ]; then
  provider_router_host="host.docker.internal"
fi
router_control_url="ws://${provider_router_host}:${router_port}/router/v1/control/ws"
router_data_url="ws://${provider_router_host}:${router_port}/router/v1/data/ws"

node_config="${work_dir}/node-agent.codex.yaml"
account_hint_line=""
if [ -n "${account_hint}" ]; then
  account_hint_line="    account_hint: ${account_hint}"
fi
cat >"${node_config}" <<EOF
version: node-agent/v1
node:
  id: ${node_id}
  host_name: ${host_name}
runtime:
  kind: docker
providers:
  - id: ${provider_id}
    instance_id: ${provider_instance_id}
    kind: cli-container
    image: ${codex_image}
    image_pull_policy: never
    host_name: ${host_name}
${account_hint_line}
    service: codex
    models:
      - id: ${codex_model}
        aliases: [codex-default]
        capabilities: [api.openai.chat, stream.sse]
    auth:
      mode: file
      format: codex-auth-json-format
      bootstrap: copy
      host_path: ${auth_path}
      container_path: /var/lib/pangaea/auth/codex/auth.json
      owner_uid: 10001
      owner_gid: 10001
      file_mode: "0600"
    refresh:
      threshold: 5m
      cooldown: 5m
      timeout: 2m
      command: [codex, exec, --skip-git-repo-check, --sandbox, read-only, --ephemeral, --ignore-user-config, --color, never, "Reply with OK only."]
    shim:
      protocols: [openai]
      capabilities: [api.openai.chat, stream.sse, usage.read, models.read, auth.file, auth.refresh.oneshot]
      entrypoint: [/usr/local/bin/provider-entrypoint]
      command: [codex, app-server, --listen, ws://127.0.0.1:8080]
      working_dir: /work
    upstream:
      adapter: websocket
      base_url: ws://127.0.0.1:8080
      compat: openai
EOF

docker ps -aq \
  --filter "label=pangaea.provider_id=${provider_id}" \
  --filter "label=pangaea.provider_instance_id=${provider_instance_id}" \
  | xargs -r docker rm -f >/dev/null

(cd "${repo_root}" && go build -trimpath -o "${work_dir}/pangaeactl" ./cmd/pangaeactl)
node_agent_pid_file="${work_dir}/node-agent.pid"
if [ -f "${node_agent_pid_file}" ] && kill -0 "$(cat "${node_agent_pid_file}")" 2>/dev/null; then
  kill "$(cat "${node_agent_pid_file}")" 2>/dev/null || true
fi
node_agent_args=(
  "${work_dir}/pangaeactl"
  node-agent
  run
  --config "${node_config}"
  --node-id "${node_id}"
  --host-name "${host_name}"
  --router-control "${router_control_url}"
  --router-data "${router_data_url}"
  --router-peer-token "${router_peer_token}"
  --stream-token-key "${stream_token_key}"
  --runtime-kind docker
  --reconcile-containers
  --heartbeat-interval 10s
  --reconcile-interval 30s
)
if truthy "${keep}"; then
  if command -v setsid >/dev/null 2>&1; then
    setsid "${node_agent_args[@]}" </dev/null >"${work_dir}/node-agent.log" 2>&1 &
  else
    nohup "${node_agent_args[@]}" </dev/null >"${work_dir}/node-agent.log" 2>&1 &
  fi
else
  "${node_agent_args[@]}" >"${work_dir}/node-agent.log" 2>&1 &
fi
node_agent_pid="$!"
printf '%s\n' "${node_agent_pid}" >"${node_agent_pid_file}"

wait_for_provider_websockets "http://127.0.0.1:${router_port}/router/v1/dashboard/providers"

dry_run_body='{"model":"codex-default","api_dialect":"openai","stream":true}'
dry_run_response="$(curl -fsS -H "authorization: Bearer ${router_api_key}" -H "content-type: application/json" \
  -d "${dry_run_body}" "http://127.0.0.1:${router_port}/router/v1/routes/dry-run" || true)"
printf '%s\n' "${dry_run_response}" >"${work_dir}/dry-run.json"
if truthy "${require_route}" && ! printf '%s' "${dry_run_response}" | grep -q '"allowed":true'; then
  echo "route dry-run did not allow codex-default; response saved to ${work_dir}/dry-run.json" >&2
  exit 1
fi

if truthy "${invoke}"; then
  curl -fsS -H "authorization: Bearer ${router_api_key}" -H "content-type: application/json" \
    -d '{"model":"codex-default","messages":[{"role":"user","content":"Reply with OK only."}],"stream":false}' \
    "http://127.0.0.1:${router_port}/v1/chat/completions" >"${work_dir}/chat-completions.json"
fi

success=1
cat <<EOF
kind codex e2e is ready
  cluster: ${cluster}
  namespace: ${namespace}
  router: http://127.0.0.1:${router_port}
  dashboard: http://127.0.0.1:${router_port}/router/ui
  provider instance: ${provider_instance_id}
  host name reported: ${host_name}
  auth source copied from: ${auth_path}
  provider container: pangaea-${provider_id}-${provider_instance_id}
  artifacts: ${work_dir}
EOF
if truthy "${keep}"; then
  echo "  port-forward pid: ${port_forward_pid}"
  echo "  node-agent pid: ${node_agent_pid}"
else
  echo "  port-forward will stop on exit because PANGAEA_E2E_KEEP=${keep}"
fi
