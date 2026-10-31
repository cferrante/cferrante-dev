#!/usr/bin/env bash
# The spike's test. Requests the SSE stream and records when each event
# arrives.
#
# Verdict: streamed if the time from the first arrival to the last is at
# least 3/4 of the time from the first send to the last (about 1.8 s).
# Buffering collapses arrivals to ~0 s. Single bunched gaps from network
# hiccups are reported but don't decide the verdict.
#
#   ./watch.sh [-n RUNS] [--post BODY [--no-hash]] [URL]
#
# URL defaults to the spike function's URL. Requests straight to a Lambda
# Function URL are SigV4-signed with the SSO session; others (CloudFront) are
# sent as a browser would. --post sends BODY as a POST with its SHA-256 in
# x-amz-content-sha256, which CloudFront's origin access control needs for
# Lambda; --no-hash leaves that header out. Every run is saved to
# build/watch/<time>.log.
set -euo pipefail
cd "$(dirname "$0")"

runs=1 body="" hash=1
while (( $# > 0 )); do
  case $1 in
    -n) runs=${2:?-n needs a number}; shift 2 ;;
    --post) body=${2?--post needs a body}; shift 2 ;;
    --no-hash) hash=0; shift ;;
    *) break ;;
  esac
done
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
url=${1:-$(aws lambda get-function-url-config --function-name spike-sse-lambda --query FunctionUrl --output text)}

curl_args=(-sSN)
if [[ -n $body ]]; then
  curl_args+=(-X POST -H "Content-Type: application/json" --data-binary "$body")
  if (( hash )); then
    curl_args+=(-H "x-amz-content-sha256: $(printf '%s' "$body" | sha256sum | cut -d' ' -f1)")
  fi
fi

signed=0
if [[ $url == *.lambda-url.* ]]; then
  signed=1
  # Temporary credentials from the SSO session, for curl's SigV4 signing.
  eval "$(aws configure export-credentials --format env)"
fi

# fetch: curl with the arguments above. Signing credentials go to curl
# through a config on a file descriptor, so they never appear in the process
# list.
fetch() {
  if (( signed )); then
    curl "${curl_args[@]}" --aws-sigv4 "aws:amz:$AWS_REGION:lambda" \
      -K <(printf 'user = "%s:%s"\n' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY") \
      -H "x-amz-security-token: $AWS_SESSION_TOKEN" "$url"
  else
    curl "${curl_args[@]}" "$url"
  fi
}

# Milliseconds since the epoch from bash's own clock. Not `date +%s%3N`:
# Ubuntu 26.04's date (uutils 0.10.0) drops leading zeros from %3N, turning
# .061 s into 610 ms. It's also one fewer process per event.
now_ms() { local t=${EPOCHREALTIME//[.,]/}; echo $((10#$t / 1000)); }

# watch_once: prints the arrival table and a PASS/FAIL line; fails on FAIL.
watch_once() {
  local start now prev=0 n=0 bunched=0 first_at=0 last_at=0 first_sent=0 last_sent=0 sent gap other=""
  start=$(now_ms)
  echo "arrival        gap     event"
  while IFS= read -r line; do
    if [[ $line != data:* ]]; then
      # Keep anything that isn't an event, e.g. an error body, for the report.
      [[ $line == id:* || $line == event:* || -z $line ]] || other+="$line"$'\n'
      continue
    fi
    now=$(now_ms)
    sent=$(sed -n 's/.*"sent_ms":\([0-9]*\).*/\1/p' <<<"$line")
    n=$((n + 1))
    if (( n == 1 )); then
      first_at=$now first_sent=$sent
      printf '%6d ms     —     %s\n' $((now - start)) "$line"
    else
      gap=$((now - prev))
      (( gap < 100 )) && bunched=$((bunched + 1))
      printf '%6d ms  %4d ms  %s\n' $((now - start)) "$gap" "$line"
    fi
    prev=$now last_at=$now last_sent=$sent
  done < <(fetch)

  local arrived=$((last_at - first_at)) sent_span=$((last_sent - first_sent))
  echo
  if (( n == 0 )); then
    echo "no events; response body:"
    printf '%s' "${other:-(empty)}" | head -c 500
    echo
  else
    echo "first event after $((first_at - start)) ms; arrivals spread over $arrived ms, sends over $sent_span ms; $bunched bunched gap(s)"
  fi
  if (( n == 10 && sent_span > 0 && arrived * 4 >= sent_span * 3 )); then
    echo "PASS: streamed one by one"
  elif (( n == 0 )); then
    echo "FAIL: no events (an error response, not a stream)"
    return 1
  else
    echo "FAIL: $n events, arrivals not spread like the sends: buffered, not streamed"
    return 1
  fi
}

mkdir -p build/watch
passed=0
for (( r = 1; r <= runs; r++ )); do
  log=build/watch/$(date +%Y%m%dT%H%M%S)-$r.log
  if watch_once | tee "$log"; then passed=$((passed + 1)); fi
  (( runs > 1 )) && echo "---- run $r saved to $log" && echo
done
(( runs > 1 )) && echo "$passed of $runs runs passed"
(( passed == runs ))
