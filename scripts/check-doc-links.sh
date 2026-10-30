#!/usr/bin/env bash
# Verify every Markdown link with a #fragment in README.md and docs/*.md points to
# a heading anchor that GitHub actually renders for the target file.
#
# Anchors come from GitHub's contents API with the HTML media type (the same
# anchors github.com shows), so there is no slug guessing. That means the commit
# being checked must already be pushed. Links to external URLs are skipped.
#
# usage: scripts/check-doc-links.sh [ref]   (default: HEAD; needs `gh` logged in
#                                            or GH_TOKEN set)
set -euo pipefail

root=$(git rev-parse --show-toplevel)
ref=${1:-$(git rev-parse HEAD)}
repo=${GITHUB_REPOSITORY:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)}
declare -A anchors   # repo-relative path -> newline-separated anchor ids

anchors_for() {      # fetch and cache rendered anchors for a repo-relative path
  local path=$1
  if [[ -z ${anchors[$path]+x} ]]; then
    local html
    html=$(gh api "repos/$repo/contents/$path?ref=$ref" -H "Accept: application/vnd.github.html+json")
    anchors[$path]=$(grep -oE 'id="user-content-[^"]+"' <<<"$html" |
                     sed 's/^id="user-content-//; s/"$//' | sort -u || true)
  fi
}

fail=0; count=0
for src in "$root/README.md" "$root"/docs/*.md; do
  rel_src=${src#"$root"/}
  while read -r link; do
    [[ -z $link || $link == *://* ]] && continue
    target=${link%%#*} anchor=${link#*#}
    if [[ -z $target ]]; then
      path=$rel_src
    else
      path=$(realpath -m --relative-to="$root" "$(dirname "$src")/$target")
    fi
    anchors_for "$path"
    count=$((count + 1))
    if ! grep -qx -- "$anchor" <<<"${anchors[$path]}"; then
      echo "BROKEN: $rel_src -> $path#$anchor"
      fail=1
    fi
  done < <(grep -oE '\]\([^) ]*#[^) ]+\)' "$src" | sed 's/^](//; s/)$//' || true)
done

echo "checked $count fragment links across ${#anchors[@]} target files at $ref"
exit $fail
