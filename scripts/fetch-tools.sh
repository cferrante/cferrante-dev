#!/usr/bin/env bash
# Fetch the pinned frontend tools into build/tools (git-ignored), verifying
# each download's SHA-256 before keeping it. Idempotent: a tool already
# present with the right checksum is not downloaded again.
#
# Versions are releases at least two weeks old; checksums are the ones each
# project publishes with the release (plan Phase 4). To upgrade, change a
# version and its checksum together, in one reviewed commit.
set -euo pipefail

TAILWIND_VERSION=v4.3.3
TAILWIND_SHA256=dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a # tailwindcss-linux-x64
DENO_VERSION=v2.9.7
DENO_SHA256=c6527f24f4b16031d3ae4fa9f658d5f11534c8d84ce7dc8502420280919c3490 # deno-x86_64-unknown-linux-gnu.zip

dir=${TOOLS_DIR:-build/tools}
curl_cmd=${TOOLS_CURL:-curl}
min_free_kb=${TOOLS_MIN_FREE_KB:-5242880} # 5 GB: stop and ask below this

verified() { [ -f "$2" ] && echo "$1  $2" | sha256sum -c --status; }

# fetch URL FILE SHA256: download to FILE.part, keep it only if it verifies.
fetch() {
  local url=$1 out=$2 sha=$3
  if verified "$sha" "$out"; then
    return
  fi
  local free_kb
  free_kb=$(df --output=avail -k "$(dirname "$out")" | tail -1)
  if [ "$free_kb" -lt "$min_free_kb" ]; then
    echo "fetch-tools: only $((free_kb / 1024)) MB free; stopping before downloading $(basename "$out")" >&2
    exit 1
  fi
  echo "fetch-tools: downloading $url"
  rm -f "$out.part"
  "$curl_cmd" -fsSL -o "$out.part" "$url"
  if ! verified "$sha" "$out.part"; then
    rm -f "$out.part"
    echo "fetch-tools: checksum mismatch for $url; nothing kept" >&2
    exit 1
  fi
  mv "$out.part" "$out"
}

mkdir -p "$dir"
fetch "https://github.com/tailwindlabs/tailwindcss/releases/download/$TAILWIND_VERSION/tailwindcss-linux-x64" \
  "$dir/tailwindcss" "$TAILWIND_SHA256"
chmod +x "$dir/tailwindcss"

fetch "https://github.com/denoland/deno/releases/download/$DENO_VERSION/deno-x86_64-unknown-linux-gnu.zip" \
  "$dir/deno.zip" "$DENO_SHA256"
if [ ! -x "$dir/deno" ] || [ "$dir/deno.zip" -nt "$dir/deno" ]; then
  unzip -o -q "$dir/deno.zip" deno -d "$dir"
  touch "$dir/deno"
fi
echo "fetch-tools: tailwindcss $TAILWIND_VERSION and deno $DENO_VERSION ready in $dir"
