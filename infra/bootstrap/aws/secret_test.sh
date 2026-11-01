#!/usr/bin/env bash
# Tests put_secure_string with the real AWS CLI, fake credentials and an
# endpoint where nothing listens, so nothing reaches AWS. "Could not connect"
# means the CLI parsed the request and tried to send it.
set -uo pipefail
cd "$(dirname "$0")" || exit 1
real_aws=$(command -v aws) || { echo "skip: no aws CLI"; exit 0; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
ok()  { printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad() { printf '  \033[31m✗\033[0m %s\n' "$*"; fails=$((fails + 1)); }

# A shim ahead of the real CLI records its arguments and, for a file://
# argument, the file's mode, its directory's mode and its filesystem type.
mkdir "$tmp/bin"
cat >"$tmp/bin/aws" <<'SHIM'
#!/usr/bin/env bash
printf 'args: %s\n' "$*" >>"$SHIM_LOG"
for a; do
  if [[ $a == file://* ]]; then
    f=${a#file://}
    printf 'file: %s %s %s %s\n' "$f" "$(stat -L -c %a "$f")" "$(stat -c %a "$(dirname "$f")")" \
      "$(stat -f -L -c %T "$f")" >>"$SHIM_LOG"
  fi
done
exec "$REAL_AWS" "$@"
SHIM
chmod +x "$tmp/bin/aws"

secret=gsk_TESTONLY0123456789abcdefghij
out=$(
  export PATH="$tmp/bin:$PATH" REAL_AWS="$real_aws" SHIM_LOG="$tmp/log"
  export AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_REGION=us-east-1 \
    AWS_ACCESS_KEY_ID=AKIATESTONLY AWS_SECRET_ACCESS_KEY=test AWS_MAX_ATTEMPTS=1
  unset AWS_PROFILE AWS_SESSION_TOKEN
  die() { echo "die: $*"; exit 1; }
  project() { aws --endpoint-url http://127.0.0.1:9 --cli-connect-timeout 2 "$@"; }
  # shellcheck source=secret.sh
  source ./secret.sh
  put_secure_string /test/param "$secret" "test only" 2>&1
)
log=$(cat "$tmp/log" 2>/dev/null)

echo "put_secure_string"
if [[ $out == *"Could not connect"* ]]; then
  ok "the CLI parsed the request"
else
  bad "the CLI didn't get as far as sending: ${out##*$'\n'}"
fi
if [[ $log == *"$secret"* ]]; then bad "the secret was on the CLI's command line"; else ok "the secret isn't on the command line"; fi
read -r _ path mode dmode fs < <(grep '^file: ' <<<"$log") || true
if [[ $mode == 600 && $dmode == 700 ]]; then ok "file 600 in a 700 directory"; else bad "file mode ${mode:-?}, directory ${dmode:-?}; want 600 and 700"; fi
if [[ $fs == tmpfs ]]; then ok "on tmpfs (memory only)"; else bad "on ${fs:-?}, want tmpfs"; fi
if [[ -n ${path:-} && ! -e $(dirname "$path") ]]; then ok "deleted afterwards"; else bad "${path:-the file} still exists"; fi
(( fails == 0 ))
