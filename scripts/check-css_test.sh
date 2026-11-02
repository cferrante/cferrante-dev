#!/usr/bin/env bash
# Tests check-css.sh against small hand-made stylesheets.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0 fail=0
check() {
  local name=$1
  shift
  if "$@"; then echo "  ✓ $name"; pass=$((pass + 1)); else echo "  ✗ $name"; fail=$((fail + 1)); fi
}
passes() { "$here/check-css.sh" "$@" >/dev/null 2>&1; }
fails() { ! passes "$@"; }

printf 'grid\nmd:grid-cols-2\ndata-[state=active]:bg-accent\n' > "$work/required"
good='.grid{display:grid}@media (width>=48rem){.md\:grid-cols-2{grid-template-columns:repeat(2,minmax(0,1fr))}}.data-\[state\=active\]\:bg-accent[data-state=active]{background:var(--color-accent)}'
printf '%s' "$good" > "$work/good.css"
printf '%s' "${good//md\\:grid-cols-2/md\\:grid-cols-3}" > "$work/missing.css"
head -c 200000 /dev/urandom | base64 > "$work/big.css" # random text barely compresses

echo "check-css.sh"
check "a small stylesheet with every required class passes" passes "$work/good.css" "$work/required"
check "a missing file fails" fails "$work/absent.css" "$work/required"
check "a missing required class fails (escaped variant)" fails "$work/missing.css" "$work/required"
check "over 15 KB gzipped fails" fails "$work/big.css" /dev/null
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
