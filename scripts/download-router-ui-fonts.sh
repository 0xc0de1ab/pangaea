#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/.." && pwd)"
font_dir="${repo_root}/web/router-ui/src/assets/fonts"

pretendard_version="v1.3.9"
jetbrains_mono_version="v2.304"
notice_file="${font_dir}/NOTICE.md"
download_date="${DOWNLOAD_DATE:-}"

mkdir -p "${font_dir}"

if [[ -z "${download_date}" && -f "${notice_file}" ]]; then
  download_date="$(sed -n 's/^Downloaded: //p' "${notice_file}" | head -n 1)"
fi

if [[ -z "${download_date}" ]]; then
  download_date="$(date +%Y-%m-%d)"
fi

declare -A urls=(
  ["PretendardVariable.woff2"]="https://raw.githubusercontent.com/orioncactus/pretendard/${pretendard_version}/packages/pretendard/dist/web/variable/woff2/PretendardVariable.woff2"
  ["JetBrainsMono-Regular.woff2"]="https://raw.githubusercontent.com/JetBrains/JetBrainsMono/${jetbrains_mono_version}/fonts/webfonts/JetBrainsMono-Regular.woff2"
  ["JetBrainsMono-SemiBold.woff2"]="https://raw.githubusercontent.com/JetBrains/JetBrainsMono/${jetbrains_mono_version}/fonts/webfonts/JetBrainsMono-SemiBold.woff2"
)

declare -A sha256=(
  ["PretendardVariable.woff2"]="9599f12fd42fc0bce1cd50b47a0c022e108d7aa64dd0d1bb0ed44f3282d900b4"
  ["JetBrainsMono-Regular.woff2"]="a9cb1cd82332b23a47e3a1239d25d13c86d16c4220695e34b243effa999f45f2"
  ["JetBrainsMono-SemiBold.woff2"]="918edad542a1da608fd2ba8daebaff9ac802309103fe760eed465b8b4e47faf1"
)

files=(
  "PretendardVariable.woff2"
  "JetBrainsMono-Regular.woff2"
  "JetBrainsMono-SemiBold.woff2"
)

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

for file in "${files[@]}"; do
  curl -fsSL "${urls[${file}]}" -o "${tmp_dir}/${file}"
done

for file in "${files[@]}"; do
  printf '%s  %s\n' "${sha256[${file}]}" "${file}"
done >"${tmp_dir}/checksums.txt"

(
  cd "${tmp_dir}"
  sha256sum -c checksums.txt
)

for file in "${files[@]}"; do
  install -m 0644 "${tmp_dir}/${file}" "${font_dir}/${file}"
done

cp "${tmp_dir}/checksums.txt" "${font_dir}/checksums.txt"

cat >"${notice_file}" <<EOF
# Router UI Font Assets

Downloaded: ${download_date}

## Pretendard Variable

- File: PretendardVariable.woff2
- Version: ${pretendard_version}
- Source URL: ${urls["PretendardVariable.woff2"]}
- License: SIL Open Font License, Version 1.1
- License URL: https://raw.githubusercontent.com/orioncactus/pretendard/${pretendard_version}/LICENSE
- SHA-256: ${sha256["PretendardVariable.woff2"]}

## JetBrains Mono Regular

- File: JetBrainsMono-Regular.woff2
- Version: ${jetbrains_mono_version}
- Source URL: ${urls["JetBrainsMono-Regular.woff2"]}
- License: SIL Open Font License, Version 1.1
- License URL: https://raw.githubusercontent.com/JetBrains/JetBrainsMono/${jetbrains_mono_version}/OFL.txt
- SHA-256: ${sha256["JetBrainsMono-Regular.woff2"]}

## JetBrains Mono SemiBold

- File: JetBrainsMono-SemiBold.woff2
- Version: ${jetbrains_mono_version}
- Source URL: ${urls["JetBrainsMono-SemiBold.woff2"]}
- License: SIL Open Font License, Version 1.1
- License URL: https://raw.githubusercontent.com/JetBrains/JetBrainsMono/${jetbrains_mono_version}/OFL.txt
- SHA-256: ${sha256["JetBrainsMono-SemiBold.woff2"]}
EOF

printf 'Router UI fonts downloaded to %s\n' "${font_dir}"
