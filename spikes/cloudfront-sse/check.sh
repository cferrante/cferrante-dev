#!/usr/bin/env bash
# The spike's test: four expectations for CloudFront (with origin access
# control) in front of the spike-sse-lambda Function URL. Prints ✓ or ✗ for
# each; exits non-zero if any fails. Invokes the function; changes nothing.
set -uo pipefail
cd "$(dirname "$0")" || exit 1
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
watch=../sse-lambda/watch.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
ok()  { printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad() { printf '  \033[31m✗\033[0m %s\n' "$*"; fails=$((fails + 1)); }

# stream_check NAME WATCH_ARGS... : runs watch.sh and reports its verdict,
# with the error body when no events arrived.
stream_check() {
  local out=$tmp/$1; shift
  if "$watch" "$@" >"$out" 2>&1; then
    ok "$(tail -1 "$out")"
  else
    bad "$(tail -1 "$out")"
    sed -n '/^no events/,$p' "$out" | head -4 | sed 's/^/      /'
  fi
}

lambda_url=$(aws lambda get-function-url-config --function-name spike-sse-lambda \
  --query FunctionUrl --output text 2>/dev/null) || lambda_url=""
domain=$(aws cloudfront list-distributions \
  --query "DistributionList.Items[?Comment=='spike-cloudfront-sse'].DomainName | [0]" --output text 2>/dev/null)
[[ $domain == None ]] && domain=""
cf_url=${domain:+https://$domain/}

echo "1. The Function URL refuses unsigned requests (only CloudFront and signed callers get in)"
if [[ -z $lambda_url ]]; then
  bad "no Function URL (run ../sse-lambda/deploy.sh)"
else
  code=$(curl -s -o /dev/null -w '%{http_code}' "$lambda_url")
  if [[ $code == 403 ]]; then ok "unsigned request → 403"; else bad "unsigned request → $code, want 403"; fi
fi

echo "2. GET through CloudFront streams events one by one"
if [[ -z $cf_url ]]; then bad "no CloudFront distribution (run ./deploy.sh)"; else stream_check get "$cf_url"; fi

echo "3. POST through CloudFront without the body hash is refused"
if [[ -z $cf_url ]]; then
  bad "no CloudFront distribution"
else
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    --data-binary '{"q":"hi"}' "$cf_url")
  if [[ $code =~ ^4 ]]; then
    ok "POST without x-amz-content-sha256 → $code"
  else
    bad "POST without x-amz-content-sha256 → $code, want a 4xx"
  fi
fi

echo "4. POST through CloudFront with the body hash streams events one by one"
if [[ -z $cf_url ]]; then bad "no CloudFront distribution"; else stream_check post --post '{"q":"hi"}' "$cf_url"; fi

echo
if (( fails == 0 )); then echo "all 4 passed"; else echo "$fails of 4 failed"; exit 1; fi
