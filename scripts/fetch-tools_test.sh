#!/usr/bin/env bash
# Tests fetch-tools.sh with a fake downloader: no network, no real tools.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0 fail=0
# check NAME COMMAND...: runs the command (no eval) and reports it.
check() {
  local name=$1
  shift
  if "$@"; then echo "  ✓ $name"; pass=$((pass + 1)); else echo "  ✗ $name"; fail=$((fail + 1)); fi
}
empty_dir() { [ -z "$(ls -A "$1" 2>/dev/null)" ]; }

# The fake curl writes whatever FAKE_CONTENT says to the -o file.
cat > "$work/curl" <<'FAKE'
#!/usr/bin/env bash
out=""
while [ $# -gt 0 ]; do [ "$1" = "-o" ] && { out=$2; shift; }; shift; done
printf '%s' "$FAKE_CONTENT" > "$out"
echo "$out" >> "$FAKE_LOG"
FAKE
chmod +x "$work/curl"
export TOOLS_CURL="$work/curl" FAKE_LOG="$work/downloads"

echo "fetch-tools.sh"
# 1. Content that doesn't match the pinned checksum is refused and removed.
export FAKE_CONTENT="not the real tool"
if TOOLS_DIR="$work/bad" "$here/fetch-tools.sh" >/dev/null 2>"$work/err"; then status=0; else status=$?; fi
check "a checksum mismatch fails" [ "$status" -ne 0 ]
check "it names the mismatch" grep -q "checksum mismatch" "$work/err"
check "nothing is left behind" empty_dir "$work/bad"

# 2. Too little free disk stops before any download.
: > "$FAKE_LOG"
if TOOLS_DIR="$work/full" TOOLS_MIN_FREE_KB=9999999999999 "$here/fetch-tools.sh" >/dev/null 2>"$work/err"; then status=0; else status=$?; fi
check "low disk space fails" [ "$status" -ne 0 ]
check "it says why" grep -q "free" "$work/err"
check "and downloads nothing" [ ! -s "$FAKE_LOG" ]

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
