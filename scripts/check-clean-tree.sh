#!/usr/bin/env bash
# Fails if the checkout has changed: tests and builds must write only to
# build/ or temporary folders, never to tracked or new files in the repo.
set -euo pipefail
changes=$(git status --porcelain)
if [[ -n "$changes" ]]; then
  echo "the checkout changed during the build:" >&2
  echo "$changes" >&2
  exit 1
fi
echo "checkout unchanged"
