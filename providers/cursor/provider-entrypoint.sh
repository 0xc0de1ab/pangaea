#!/usr/bin/env bash
set -euo pipefail

export PANGAEA_SHIM_MODE="${PANGAEA_SHIM_MODE:-cli-container}"
export PANGAEA_SERVICE="${PANGAEA_SERVICE:-cursor}"
export PANGAEA_PROVIDER_MODE="${PANGAEA_PROVIDER_MODE:-acp}"
export PANGAEA_UPSTREAM_DIALECT="${PANGAEA_UPSTREAM_DIALECT:-openai}"
export PANGAEA_CURSOR_AGENT_EXE="${PANGAEA_CURSOR_AGENT_EXE:-/home/pangaea/.local/bin/agent}"
export HOME="${HOME:-/var/lib/pangaea/home/cursor}"
export PANGAEA_HOST_NAME="${PANGAEA_HOST_NAME:-${HOSTNAME:-cursor-cli-container}}"

cursor_valid_node_id() {
	[[ "${1:-}" =~ ^[a-z0-9]{6}$ ]]
}

cursor_generate_node_id() {
	local out=""
	while [ "${#out}" -lt 6 ]; do
		out="${out}$(od -An -N8 -tu1 /dev/urandom | tr -d ' \n')"
	done
	printf '%s\n' "${out:0:6}"
}

cursor_runtime_node_id() {
	local settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
	local line value
	[ -s "${settings}" ] || return 1
	while IFS= read -r line; do
		case "${line}" in
		PANGAEA_NODE_ID=*)
			value="${line#PANGAEA_NODE_ID=}"
			value="${value//\"/}"
			value="${value//\'/}"
			if cursor_valid_node_id "${value}"; then
				printf '%s\n' "${value}"
				return 0
			fi
			;;
		esac
	done <"${settings}"
	return 1
}

ensure_cursor_node_id() {
	local existing
	if cursor_valid_node_id "${PANGAEA_NODE_ID:-}"; then
		return 0
	fi
	if existing="$(cursor_runtime_node_id)"; then
		export PANGAEA_NODE_ID="${existing}"
		return 0
	fi
	export PANGAEA_NODE_ID="$(cursor_generate_node_id)"
}
ensure_cursor_node_id

# Cursor CLI auth: setup-provider copies ~/.cursor/cli-config.json into HOME.
# Plain API keys are still accepted for manual direct-http/Cloud API use.
export PANGAEA_AUTH_REQUIRED="${PANGAEA_AUTH_REQUIRED:-true}"
if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
	if [ -z "${PANGAEA_AUTH_PATH:-}" ]; then
		if [ -n "${CURSOR_API_KEY:-${PANGAEA_UPSTREAM_API_KEY:-}}" ]; then
			export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-cursor-api-token-plain-format}"
			export PANGAEA_AUTH_PATH="${PANGAEA_DEFAULT_AUTH_PATH:-/var/lib/pangaea/auth/cursor/api_token}"
		else
			export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-cursor-auth-json-format}"
			export PANGAEA_AUTH_PATH="${PANGAEA_DEFAULT_AUTH_PATH:-${HOME}/.config/cursor/auth.json}"
		fi
	else
		export PANGAEA_AUTH_FORMAT="${PANGAEA_AUTH_FORMAT:-cursor-auth-json-format}"
	fi
	if [ ! -s "${PANGAEA_AUTH_PATH}" ]; then
		cursor_bootstrap_token="${CURSOR_API_KEY:-${PANGAEA_UPSTREAM_API_KEY:-}}"
		if [ -n "${cursor_bootstrap_token}" ] && [ "${PANGAEA_AUTH_FORMAT}" = "cursor-api-token-plain-format" ]; then
			mkdir -p "$(dirname "${PANGAEA_AUTH_PATH}")"
			umask 077
			printf '%s\n' "${cursor_bootstrap_token}" >"${PANGAEA_AUTH_PATH}"
			unset cursor_bootstrap_token
		fi
	fi
fi

write_runtime_settings() {
	local settings="${PANGAEA_RUNTIME_SETTINGS_PATH:-/var/lib/pangaea/runtime/provider.env}"
	local dir
	local existing_node_id=""
	dir="$(dirname "${settings}")"
	mkdir -p "${dir}" 2>/dev/null || return 0
	if existing_node_id="$(cursor_runtime_node_id 2>/dev/null)"; then
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
	if [ "${PANGAEA_AUTH_REQUIRED}" = "false" ]; then
		return 0
	fi
	if [ -n "${CURSOR_API_KEY:-}" ] || [ -n "${PANGAEA_UPSTREAM_API_KEY:-}" ]; then
		return 0
	fi
	if [ -n "${PANGAEA_AUTH_PATH:-}" ] && [ -s "${PANGAEA_AUTH_PATH}" ]; then
		return 0
	fi
	return 1
}

if [ "${PANGAEA_AUTH_REQUIRED}" != "false" ]; then
	auth_wait_timeout="${PANGAEA_AUTH_WAIT_TIMEOUT:-120}"
	case "${auth_wait_timeout}" in
	'' | *[!0-9]*) auth_wait_timeout=120 ;;
	esac
	deadline=$((SECONDS + auth_wait_timeout))
	while ! auth_ready; do
		if [ "${SECONDS}" -ge "${deadline}" ]; then
			echo "provider-entrypoint: need Cursor API key (CURSOR_API_KEY, PANGAEA_UPSTREAM_API_KEY, or non-empty ${PANGAEA_AUTH_PATH:-auth file})" >&2
			exit 1
		fi
		sleep 1
	done
fi

exec /usr/local/bin/pangaeactl provider-shim run "$@"
