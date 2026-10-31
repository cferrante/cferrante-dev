#!/usr/bin/env bash
# Self-test for check-glibc.sh: it must pass a binary that needs only old
# glibc, and fail one that needs a newer glibc, naming every offending symbol.
set -euo pipefail
cd "$(dirname "$0")"
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

gcc -o "$out/old" testdata/old_glibc.c
gcc -o "$out/new" testdata/needs_newer_glibc.c

./check-glibc.sh "$out/old" || { echo "self-test FAILED: old-glibc binary was rejected"; exit 1; }
if report=$(./check-glibc.sh "$out/new"); then
  echo "self-test FAILED: binary needing newer glibc was accepted"; exit 1
fi
echo "$report"
for sym in arc4random strlcpy; do
  grep -q "$sym" <<<"$report" || { echo "self-test FAILED: report doesn't name $sym"; exit 1; }
done
echo "self-test passed"
