#!/usr/bin/env bash
# Stores the Groq API key in the project account's SSM Parameter Store as a
# SecureString, encrypted with the AWS-managed key. Cost: $0 (standard tier).
#
# The key is typed at a hidden prompt, checked against Groq's API, and never
# shown, logged, written to disk or put on a command line (where any process
# could read it). Re-running offers to replace the stored key.
source "$(dirname "$0")/lib.sh"
require GROQ_KEY_PARAM
ensure_login "$PROJECT_PROFILE"

changed=$(project ssm describe-parameters --parameter-filters "Key=Name,Values=$GROQ_KEY_PARAM" \
  --query 'Parameters[0].LastModifiedDate' --output text)
if [[ $changed != None ]]; then
  ok "a key is already stored at $GROQ_KEY_PARAM (last changed $changed)"
  read -r -p "  Replace it? [y/N] " answer
  [[ $answer == [yY] ]] || exit 0
fi

read -r -s -p "  Paste the Groq API key (input hidden): " key
echo
# Groq keys are "gsk_" plus letters and digits; checking the shape catches a
# bad paste and means the key needs no escaping in the JSON below.
[[ $key =~ ^gsk_[A-Za-z0-9]{20,}$ ]] \
  || die "that doesn't look like a Groq API key (gsk_ followed by letters and digits); nothing stored"

act "checking the key with Groq"
# The Authorization header reaches curl through a config on a file
# descriptor, not the command line.
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -K <(printf 'header = "Authorization: Bearer %s"\n' "$key") \
  https://api.groq.com/openai/v1/models)
[[ $code == 200 ]] || die "Groq didn't accept the key (HTTP $code); nothing stored"

act "storing it as a SecureString"
put_secure_string "$GROQ_KEY_PARAM" "$key" "Groq API key for cferrante-dev"
unset key

# Confirm it round-trips, showing only the prefix and length.
project ssm get-parameter --name "$GROQ_KEY_PARAM" --with-decryption \
  --query Parameter.Value --output text | {
  read -r stored
  ok "stored: ${stored:0:4}… (${#stored} characters)"
}
