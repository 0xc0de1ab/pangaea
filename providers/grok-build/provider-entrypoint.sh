#!/usr/bin/env bash
set -euo pipefail

export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-grok-build}"
export PANGAEA_PROVIDER_MODE="${PANGAEA_PROVIDER_MODE:-acp}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_GROK_CLI_EXE="${PANGAEA_GROK_CLI_EXE:-/usr/local/bin/grok}"
export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-grok-auth-json-format}"
export PANGAEA_MODEL="${PANGAEA_MODEL:-grok-build}"
export PANGAEA_MODELS="${PANGAEA_MODELS:-grok-build=grok-build-default|grok-build-0.1|grok-default}"
export HOME="${HOME:-/var/lib/pangaea/home/grok}"
export PANGAEA_HOST_NAME="${PANGAEA_HOST_NAME:-${HOSTNAME:-grok-build-cli-container}}"

if [ -z "${XAI_API_KEY:-}" ] && [ -n "${GROK_CODE_XAI_API_KEY:-}" ]; then
	export XAI_API_KEY="${GROK_CODE_XAI_API_KEY}"
fi

grok_valid_node_id() {
	[[ "${1:-}" =~ ^[a-z0-9]{6}$ ]]
}

grok_generate_node_id() {
	local out=""
	while [ "${#out}" -lt 6 ]; do
		out="${out}$(od -An -N8 -tu1 /dev/urandom | tr -d ' \n')"
	done
	printf '%s\n' "${out:0:6}"
}

grok_runtime_node_id() {
	local settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
	local line value
	[ -s "${settings}" ] || return 1
	while IFS= read -r line; do
		case "${line}" in
		PANGAEA_NODE_ID=*)
			value="${line#PANGAEA_NODE_ID=}"
			value="${value//\"/}"
			value="${value//\'/}"
			if grok_valid_node_id "${value}"; then
				printf '%s\n' "${value}"
				return 0
			fi
			;;
		esac
	done <"${settings}"
	return 1
}

ensure_grok_node_id() {
	local existing
	if grok_valid_node_id "${PANGAEA_NODE_ID:-}"; then
		return 0
	fi
	if existing="$(grok_runtime_node_id)"; then
		export PANGAEA_NODE_ID="${existing}"
		return 0
	fi
	export PANGAEA_NODE_ID="$(grok_generate_node_id)"
}
ensure_grok_node_id

write_runtime_settings() {
	local settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
	local dir
	local existing_node_id=""
	dir="$(dirname "${settings}")"
	mkdir -p "${dir}" 2>/dev/null || return 0
	if existing_node_id="$(grok_runtime_node_id 2>/dev/null)"; then
		:
	fi
	if [ ! -s "${settings}" ] || [ "${existing_node_id}" != "${PANGAEA_NODE_ID:-}" ]; then
		{
			printf 'PANGAEA_NODE_ID=%q\n' "${PANGAEA_NODE_ID:-}"
			printf 'PANGAEA_HOST_NAME=%q\n' "${PANGAEA_HOST_NAME:-}"
			printf 'PANGAEA_PROVIDER_TYPE=%q\n' "${PANGAEA_PROVIDER_TYPE:-}"
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

auth_ready() {
	if [ "${PANGAEA_AUTH_REQUIRED:-true}" = "false" ]; then
		return 0
	fi
	if [ -n "${XAI_API_KEY:-}" ]; then
		return 0
	fi
	if [ -n "${PANGAEA_AUTH_PATH:-}" ] && [ -s "${PANGAEA_AUTH_PATH}" ]; then
		return 0
	fi
	if [ -s "${HOME}/.grok/auth.json" ]; then
		return 0
	fi
	return 1
}

if [ "${PANGAEA_AUTH_REQUIRED:-true}" != "false" ]; then
	auth_wait_timeout="${PANGAEA_AUTH_WAIT_TIMEOUT:-120}"
	case "${auth_wait_timeout}" in
	'' | *[!0-9]*) auth_wait_timeout=120 ;;
	esac
	deadline=$((SECONDS + auth_wait_timeout))
	while ! auth_ready; do
		if [ "${SECONDS}" -ge "${deadline}" ]; then
			echo "provider-entrypoint: need Grok auth (${HOME}/.grok/auth.json, PANGAEA_AUTH_PATH, or XAI_API_KEY)" >&2
			exit 1
		fi
		sleep 1
	done
fi

exec /usr/local/bin/pangaeactl provider-shim run "$@"
