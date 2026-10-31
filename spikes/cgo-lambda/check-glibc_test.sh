#!/usr/bin/env bash
# Self-test for check-glibc.sh: it must pass a binary that needs only old
# glibc and fail one that needs a newer glibc than the limit.
set -euo pipefail
cd "$(dirname "$0")"
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

gcc -o "$out/old" testdata/old_glibc.c
gcc -o "$out/new" testdata/needs_glibc_2_38.c

./check-glibc.sh "$out/old" || { echo "self-test FAILED: old-glibc binary was rejected"; exit 1; }
if ./check-glibc.sh "$out/new"; then
  echo "self-test FAILED: binary needing GLIBC_2.38 was accepted"; exit 1
fi
echo "self-test passed"
