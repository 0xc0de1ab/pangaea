#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: tools/bump_up.sh [--dry-run] [--month YYYYMM] [--set VERSION] [--major|--minor|--patch]

Bumps Pangaea's checked-in VERSION_BASE and default pangaeactl version.

Default behavior increments the monthly sequence for the current YYYYMM:
  v0.9.0-202605.1 -> v0.9.0-202605.2

If the current VERSION_BASE month differs from --month/current month, the
sequence resets to 1:
  v0.9.0-202605.2 -> v0.9.0-202606.1

Options:
  --set VERSION   set an explicit version, e.g. v0.9.0-202605.1
  --month YYYYMM  release month used by automatic bumps
  --major         increment semantic major, reset minor/patch/seq
  --minor         increment semantic minor, reset patch/seq
  --patch         increment semantic patch, reset seq
  --dry-run       print the next version without editing files
USAGE
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
makefile="${repo_root}/Makefile"
root_go="${repo_root}/cmd/pangaeactl/root.go"

month="${RELEASE_YYYYMM:-$(date +%Y%m)}"
mode="seq"
target=""
dry_run=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --set)
      target="${2:-}"
      shift 2
      ;;
    --month)
      month="${2:-}"
      shift 2
      ;;
    --major|--minor|--patch)
      mode="${1#--}"
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! "${month}" =~ ^[0-9]{6}$ ]]; then
  echo "invalid --month ${month}; expected YYYYMM" >&2
  exit 2
fi

current="$(
  awk '
    $1 == "VERSION_BASE" && $2 == "?=" {
      print $3
      exit
    }
  ' "${makefile}"
)"

if [[ -z "${current}" ]]; then
  echo "could not find VERSION_BASE in ${makefile}" >&2
  exit 1
fi

validate_version() {
  [[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{6}\.[0-9]+$ ]]
}

if ! validate_version "${current}"; then
  echo "current VERSION_BASE has unsupported format: ${current}" >&2
  exit 1
fi

if [[ -n "${target}" ]]; then
  if ! validate_version "${target}"; then
    echo "invalid --set version ${target}; expected vSEMVER-YYYYMM.seq" >&2
    exit 2
  fi
else
  if [[ "${current}" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)-([0-9]{6})\.([0-9]+)$ ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    patch="${BASH_REMATCH[3]}"
    current_month="${BASH_REMATCH[4]}"
    seq="${BASH_REMATCH[5]}"
  else
    echo "current VERSION_BASE has unsupported format: ${current}" >&2
    exit 1
  fi

  case "${mode}" in
    seq)
      if [[ "${current_month}" == "${month}" ]]; then
        seq="$((seq + 1))"
      else
        seq=1
      fi
      ;;
    patch)
      patch="$((patch + 1))"
      seq=1
      ;;
    minor)
      minor="$((minor + 1))"
      patch=0
      seq=1
      ;;
    major)
      major="$((major + 1))"
      minor=0
      patch=0
      seq=1
      ;;
    *)
      echo "unsupported bump mode: ${mode}" >&2
      exit 2
      ;;
  esac
  target="v${major}.${minor}.${patch}-${month}.${seq}"
fi

if [[ "${dry_run}" == "1" ]]; then
  printf '%s -> %s\n' "${current}" "${target}"
  exit 0
fi

python3 - "${makefile}" "${root_go}" "${target}" <<'PY'
import pathlib
import re
import sys

makefile = pathlib.Path(sys.argv[1])
root_go = pathlib.Path(sys.argv[2])
version = sys.argv[3]

make_text = makefile.read_text()
make_text, make_count = re.subn(
    r"^VERSION_BASE \?= .*$",
    f"VERSION_BASE ?= {version}",
    make_text,
    count=1,
    flags=re.MULTILINE,
)
if make_count != 1:
    raise SystemExit("failed to update VERSION_BASE")
makefile.write_text(make_text)

root_text = root_go.read_text()
root_text, root_count = re.subn(
    r'var version = "[^"]+"',
    f'var version = "{version}"',
    root_text,
    count=1,
)
if root_count != 1:
    raise SystemExit("failed to update cmd/pangaeactl/root.go version")
root_go.write_text(root_text)
PY

printf '%s -> %s\n' "${current}" "${target}"
