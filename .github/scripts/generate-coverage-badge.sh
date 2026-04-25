#!/usr/bin/env bash
set -euo pipefail

coverage="${1:-0}"
out="${2:-.github/badges/coverage.svg}"

cov="$(printf '%.1f' "${coverage}")"
if awk 'BEGIN { exit !('"${coverage}"' >= 80) }'; then
  color="#4c1"
elif awk 'BEGIN { exit !('"${coverage}"' >= 60) }'; then
  color="#dfb317"
else
  color="#e05d44"
fi

mkdir -p "$(dirname "${out}")"
cat > "${out}" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="118" height="20" role="img" aria-label="coverage: ${cov}%">
  <linearGradient id="b" x2="0" y2="100%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="a">
    <rect width="118" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#a)">
    <path fill="#555" d="M0 0h67v20H0z"/>
    <path fill="${color}" d="M67 0h51v20H67z"/>
    <path fill="url(#b)" d="M0 0h118v20H0z"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
    <text x="345" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="570">coverage</text>
    <text x="345" y="140" transform="scale(.1)" textLength="570">coverage</text>
    <text x="915" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="410">${cov}%</text>
    <text x="915" y="140" transform="scale(.1)" textLength="410">${cov}%</text>
  </g>
</svg>
EOF
