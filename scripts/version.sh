#!/usr/bin/env bash
set -euo pipefail

version_base="${1:-${VERSION_BASE:-v0.9.0-202604.1}}"
sha_len="${SHA_LEN:-12}"
git_sha="${GIT_SHA:-}"

if [[ -z "${git_sha}" ]]; then
  if git rev-parse --short="${sha_len}" HEAD >/dev/null 2>&1; then
    git_sha="$(git rev-parse --short="${sha_len}" HEAD)"
  else
    git_sha="unknown"
  fi
fi

printf '%s.%s\n' "${version_base}" "${git_sha}"
