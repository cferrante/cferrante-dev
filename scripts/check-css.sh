#!/usr/bin/env bash
# CSS checks (README: performance budget): the built stylesheet stays under
# 15 KB gzipped, and every class in the required list is in it. A class the
# page needs but Tailwind didn't generate (say, one built from pieces at
# runtime) would silently do nothing; this catches it.
#
# usage: check-css.sh [css-file] [required-classes-file]
set -euo pipefail
css=${1:-web/dist/app.css}
required=${2:-web/required-classes.txt}
budget=${CSS_BUDGET_BYTES:-15360}

if [ ! -f "$css" ]; then
  echo "✗ $css is missing: run make css" >&2
  exit 1
fi
size=$(gzip -9 -c "$css" | wc -c)
if [ "$size" -ge "$budget" ]; then
  echo "✗ $css is $size bytes gzipped; the budget is $budget" >&2
  exit 1
fi
echo "✓ $css: $size bytes gzipped (budget $budget)"

missing=0
while IFS= read -r class; do
  case $class in '' | '#'*) continue ;; esac
  # Tailwind escapes these characters in selectors with a backslash.
  selector=".$(printf '%s' "$class" | sed 's/[]:/[().%#,=!@&*+~>]/\\&/g')"
  if ! grep -qF -- "$selector" "$css"; then
    echo "✗ required class $class ($selector) is not in $css" >&2
    missing=1
  fi
done < "$required"
[ "$missing" -eq 0 ] && echo "✓ every class in $required is present"
exit "$missing"
